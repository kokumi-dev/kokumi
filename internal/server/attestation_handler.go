package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

// preparationFromRequest reads the Preparation named by the request path with
// the caller's identity. It writes the error response and returns nil when
// the Preparation cannot be read.
func preparationFromRequest(deps *apiDeps, w http.ResponseWriter, r *http.Request) (*deliveryv1alpha1.Preparation, *userClient) {
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")

	uc, err := deps.resolveUserClient(r)
	if err != nil {
		respondForbiddenOrError(w, err, "failed to resolve identity")
		return nil, nil
	}

	prep := &deliveryv1alpha1.Preparation{}
	if err := uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, prep); err != nil {
		if client.IgnoreNotFound(err) == nil {
			respondError(w, http.StatusNotFound, fmt.Sprintf("preparation %s/%s not found", namespace, name))
			return nil, nil
		}
		respondForbiddenOrError(w, err, "failed to get preparation")
		return nil, nil
	}

	return prep, uc
}

// objectGetter reads one object with the caller's identity.
type objectGetter interface {
	get(ctx context.Context, key client.ObjectKey, obj client.Object) error
}

// handleListPreparationAttestations handles
// GET /api/v1/preparations/{namespace}/{name}/attestations[?payload=true].
// It lists the OCI referrers of the Preparation's artifact live from the
// registry, oldest first, and verifies each Sigstore bundle against the keys
// the Kitchen trusts. With payload=true the verified entries carry their
// signed statement.
func handleListPreparationAttestations(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		prep, _ := preparationFromRequest(deps, w, r)
		if prep == nil {
			return
		}

		trust, err := deps.trustSet()
		if err != nil {
			deps.logger.Error(err, "Failed to read the trusted keys")
			respondError(w, http.StatusServiceUnavailable, "the trusted keys are not available: "+err.Error())
			return
		}

		attestations, err := listAttestations(r.Context(), deps.ociClient, trust, prep, r.URL.Query().Get("payload") == "true")
		if err != nil {
			deps.logger.Error(err, "Failed to list attestations", "ociRef", prep.Spec.Artifact.OCIRef)
			respondError(w, http.StatusBadGateway, "could not list attestations: "+err.Error())
			return
		}

		respondJSON(w, http.StatusOK, attestations)
	}
}

// listAttestations lists the referrers of prep's artifact, oldest first.
// Anyone who can push to the repository can attach any number of referrers,
// so each Sigstore bundle is verified against trust, and only verified
// entries carry their statement. Other referrers are listed without content.
func listAttestations(ctx context.Context, ociClient oci.Client, trust *signing.TrustSet, prep *deliveryv1alpha1.Preparation, withPayload bool) ([]AttestationDTO, error) {
	subject, err := oci.Parse(prep.Spec.Artifact.OCIRef)
	if err != nil {
		return nil, err
	}

	referrers, err := ociClient.ListReferrers(ctx, subject, "")
	if err != nil {
		return nil, err
	}

	attestations := make([]AttestationDTO, 0, len(referrers))
	for _, referrer := range referrers {
		dto := AttestationDTO{
			ArtifactType:  referrer.ArtifactType,
			Digest:        referrer.Digest,
			PredicateType: referrer.Annotations[oci.AnnotationBundlePredicateType],
			CreatedAt:     referrer.Annotations[ocispec.AnnotationCreated],
			Size:          referrer.Size,
		}

		verification, err := verifyReferrer(ctx, ociClient, trust, subject, referrer)
		if err != nil {
			dto.Verification.Error = err.Error()
		} else {
			dto.PredicateType = verification.PredicateType
			dto.Verification = AttestationVerificationDTO{Verified: true, KeyID: verification.KeyID}
			if withPayload {
				dto.Payload = verification.Statement
			}
		}

		attestations = append(attestations, dto)
	}

	slices.SortStableFunc(attestations, func(a, b AttestationDTO) int {
		return strings.Compare(a.CreatedAt, b.CreatedAt)
	})

	return attestations, nil
}

// verifyReferrer fetches the referrer of subject and verifies it as a
// Sigstore bundle about subject, of any predicate type.
func verifyReferrer(ctx context.Context, ociClient oci.Client, trust *signing.TrustSet, subject oci.Reference, referrer oci.Referrer) (*signing.Verification, error) {
	if referrer.ArtifactType != oci.SigstoreBundleMediaType {
		return nil, errors.New("not a Sigstore bundle")
	}

	ref := subject
	ref.Tag = ""
	ref.Digest = referrer.Digest

	fetched, err := ociClient.FetchReferrer(ctx, ref)
	if err != nil {
		return nil, err
	}

	return trust.Verify(fetched.Payload, subject.Digest, "")
}

// handleGetPreparationIntent handles
// GET /api/v1/preparations/{namespace}/{name}/intent.
// It returns the config blob of the Preparation's artifact: the intent it was
// rendered from and what rendered it.
func handleGetPreparationIntent(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		prep, uc := preparationFromRequest(deps, w, r)
		if prep == nil {
			return
		}

		if err := authorizeIntent(r.Context(), uc, prep); err != nil {
			respondForbiddenOrError(w, err, "failed to get order")
			return
		}

		subject, err := oci.Parse(prep.Spec.Artifact.OCIRef)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "preparation has an invalid artifact reference")
			return
		}

		mediaType, data, err := deps.ociClient.FetchConfig(r.Context(), subject)
		if err != nil {
			deps.logger.Error(err, "Failed to fetch intent", "ociRef", prep.Spec.Artifact.OCIRef)
			respondError(w, http.StatusBadGateway, "could not fetch intent: "+err.Error())
			return
		}

		config, err := artifact.ParseConfig(mediaType, data)
		if err != nil {
			respondError(w, http.StatusNotFound, "the artifact records no intent: "+err.Error())
			return
		}

		respondJSON(w, http.StatusOK, config)
	}
}

// authorizeIntent reports whether the caller may read the intent of prep.
// The intent repeats the Order's configuration, so the caller must also be
// allowed to read the Order. A deleted Order no longer protects anything; its
// Preparations stay readable to whoever may read them.
func authorizeIntent(ctx context.Context, getter objectGetter, prep *deliveryv1alpha1.Preparation) error {
	order := &deliveryv1alpha1.Order{}
	key := types.NamespacedName{Namespace: prep.Namespace, Name: prep.Spec.OrderName}

	return client.IgnoreNotFound(getter.get(ctx, key, order))
}
