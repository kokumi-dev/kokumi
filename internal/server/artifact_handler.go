package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
	signingsource "github.com/kokumi-dev/kokumi/internal/signing/source"
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

// trustSetOrRespond returns the keys the Kitchen trusts. It writes the error
// response and returns nil when they are not available.
func trustSetOrRespond(deps *apiDeps, w http.ResponseWriter) *signing.TrustSet {
	trust, err := deps.trustSet()
	if err != nil {
		deps.logger.Error(err, "Failed to read the trusted keys")
		respondError(w, http.StatusServiceUnavailable, "the trusted keys are not available: "+err.Error())
		return nil
	}
	return trust
}

// handleGetPreparationArtifact handles
// GET /api/v1/preparations/{namespace}/{name}/artifact.
// It returns the graph of the Preparation's artifact, read live from the
// registry: its manifest and everything attached to it.
func handleGetPreparationArtifact(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		prep, uc := preparationFromRequest(deps, w, r)
		if prep == nil {
			return
		}

		trust := trustSetOrRespond(deps, w)
		if trust == nil {
			return
		}

		conn, err := deps.preparationConnection(r.Context(), uc, prep)
		if err != nil {
			respondConnectError(w, err)
			return
		}

		graph, err := artifactGraph(r.Context(), conn.Client, trust, prep.Spec.Artifact.OCIRef, nil)
		if err != nil {
			deps.logger.Error(err, "Failed to read artifact graph", "ociRef", prep.Spec.Artifact.OCIRef)
			respondError(w, http.StatusBadGateway, "could not read the artifact: "+err.Error())
			return
		}

		respondJSON(w, http.StatusOK, graph)
	}
}

// handleGetProductSourceArtifact handles
// GET /api/v1/products/{namespace}/{name}/source/artifact.
// It returns the graph of the Product's source at its source location and,
// when the Product vendors it, the graph of the vendored copy. A graph that
// cannot be read carries the error instead of failing the response. The
// signatures carry the verdicts the Product controller recorded against
// spec.source.verify; the copy keeps the digests, so they apply to it too.
func handleGetProductSourceArtifact(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		product, ok := getProduct(w, r, uc)
		if !ok {
			return
		}

		trust := trustSetOrRespond(deps, w)
		if trust == nil {
			return
		}

		var result ProductSourceArtifactDTO
		resolved := product.Status.Source
		verdicts := map[string]deliveryv1alpha1.SignatureVerdict{}
		if resolved != nil {
			for _, v := range resolved.Signatures {
				verdicts[v.Digest] = v
			}
		}

		// The source at its source location, by the digest Orders render.
		// The Product's Registries connect to it and to the vendored copy.
		source := product.Spec.Source
		ref := source.URL + ":" + source.Version
		if resolved != nil {
			ref = source.URL + ":" + resolved.Version + "@" + resolved.Digest
		}
		result.Source = deps.graphThrough(r, product, trust, ref, verdicts)

		if product.Spec.Vendor != nil && resolved != nil {
			vendored := deps.graphThrough(r, product, trust, resolved.URL+":"+resolved.Version+"@"+resolved.Digest, verdicts)
			result.Vendored = &vendored
		}

		respondJSON(w, http.StatusOK, result)
	}
}

// graphThrough returns the graph of the artifact at ref, read through the
// Product's Registries as the user of r, or a graph that carries why it
// could not be read.
func (deps *apiDeps) graphThrough(r *http.Request, product *deliveryv1alpha1.Product, trust *signing.TrustSet, ref string, verdicts map[string]deliveryv1alpha1.SignatureVerdict) ArtifactGraphDTO {
	conn, err := deps.connectFor(r, product.Namespace, product.Spec.RegistryRefs, ref)
	if err != nil {
		repository := ref
		if parsed, perr := oci.Parse(ref); perr == nil {
			repository = parsed.OCIRepositoryReference()
		}
		return ArtifactGraphDTO{Repository: repository, Error: err.Error()}
	}
	return graphOrError(r.Context(), conn.Client, trust, ref, verdicts)
}

// handleGetRepositoryArtifact handles
// GET /api/v1/repositories/{namespace}/{name}/artifact?tag=<tag>.
// It returns the graph of the artifact behind a tag of the Repository, read
// through its Registry.
func handleGetRepositoryArtifact(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")
		tag := r.URL.Query().Get("tag")
		if tag == "" {
			respondError(w, http.StatusBadRequest, "tag query parameter is required")
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		repo := &deliveryv1alpha1.Repository{}
		if err := uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, repo); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("repository %s/%s not found", namespace, name))
				return
			}
			respondForbiddenOrError(w, err, "failed to get repository")
			return
		}

		trust := trustSetOrRespond(deps, w)
		if trust == nil {
			return
		}

		conn, err := deps.connectFor(r, namespace, repositoryRegistryRefs(repo), repo.Spec.URL)
		if err != nil {
			respondConnectError(w, err)
			return
		}

		graph, err := artifactGraph(r.Context(), conn.Client, trust, repo.Spec.URL+":"+tag, nil)
		if err != nil {
			deps.logger.Error(err, "Failed to read artifact graph", "repository", name, "tag", tag)
			respondError(w, http.StatusBadGateway, "could not read the artifact: "+err.Error())
			return
		}

		respondJSON(w, http.StatusOK, graph)
	}
}

// graphOrError returns the graph of the artifact at ref, or a graph that
// carries why it could not be read.
func graphOrError(ctx context.Context, ociClient oci.Client, trust *signing.TrustSet, ref string, verdicts map[string]deliveryv1alpha1.SignatureVerdict) ArtifactGraphDTO {
	graph, err := artifactGraph(ctx, ociClient, trust, ref, verdicts)
	if err == nil {
		return graph
	}
	repository := ref
	if parsed, perr := oci.Parse(ref); perr == nil {
		repository = parsed.OCIRepositoryReference()
	}
	return ArtifactGraphDTO{Repository: repository, Error: err.Error()}
}

// artifactGraph walks the artifact at ref and verifies every Sigstore bundle
// in it against trust. Anyone who can push to the repository can attach any
// referrer, so only verified bundles carry their statement. When verdicts
// is not nil, each signature that trust does not verify carries its
// recorded verdict, or NotChecked when it has none.
func artifactGraph(ctx context.Context, ociClient oci.Client, trust *signing.TrustSet, ref string, verdicts map[string]deliveryv1alpha1.SignatureVerdict) (ArtifactGraphDTO, error) {
	parsed, err := oci.Parse(ref)
	if err != nil {
		return ArtifactGraphDTO{}, err
	}

	graph, err := oci.Walk(ctx, ociClient, parsed)
	if err != nil {
		return ArtifactGraphDTO{}, err
	}

	repo := parsed
	repo.Tag = ""
	repo.Digest = ""
	root := artifactNodeToDTO(ctx, ociClient, trust, verdicts, repo, graph.Root, "")

	return ArtifactGraphDTO{
		Repository: graph.Repository,
		Tag:        graph.Tag,
		TagDigest:  graph.TagDigest,
		Root:       &root,
	}, nil
}

// artifactNodeToDTO converts node, attached to the manifest with
// parentDigest, and verifies it when it is a Sigstore bundle.
func artifactNodeToDTO(ctx context.Context, ociClient oci.Client, trust *signing.TrustSet, verdicts map[string]deliveryv1alpha1.SignatureVerdict, repo oci.Reference, node *oci.Node, parentDigest string) ArtifactNodeDTO {
	dto := ArtifactNodeDTO{
		DescriptorDTO: descriptorToDTO(node.Descriptor),
		Link:          string(node.Link),
		Tag:           node.Tag,
		Layers:        descriptorsToDTO(node.Layers),
		Truncated:     node.Truncated,
	}
	if node.Config != nil {
		config := descriptorToDTO(*node.Config)
		dto.Config = &config
	}
	if node.Subject != nil {
		subject := descriptorToDTO(*node.Subject)
		dto.Subject = &subject
	}
	if node.Err != nil {
		dto.Error = node.Err.Error()
	}

	if node.ArtifactType == oci.SigstoreBundleMediaType && parentDigest != "" {
		dto.PredicateType = cmp.Or(node.Annotations[oci.AnnotationBundlePredicateType], node.Annotations[oci.AnnotationInTotoPredicateType])
		dto.Verification = &VerificationDTO{}

		verification, err := verifyBundle(ctx, ociClient, trust, repo, node.Digest, parentDigest)
		var untrusted *signing.UntrustedSignerError
		switch {
		case errors.As(err, &untrusted):
			dto.Verification.Reason = reasonUntrustedSigner
			dto.Verification.Error = err.Error()
		case err != nil:
			dto.Verification.Error = err.Error()
		default:
			dto.PredicateType = verification.PredicateType
			dto.Verification = &VerificationDTO{Verified: true, KeyID: verification.KeyID, KeyName: trust.KeyName(verification.KeyID)}
			dto.Payload = verification.Statement
		}
	}

	if verdicts != nil && signingsource.IsSignature(node) && (dto.Verification == nil || !dto.Verification.Verified) {
		dto.Verification = recordedVerification(trust, verdicts, node.Digest)
	}

	for _, child := range node.Children {
		dto.Manifests = append(dto.Manifests, artifactNodeToDTO(ctx, ociClient, trust, verdicts, repo, child, node.Digest))
	}
	for _, referrer := range node.Referrers {
		dto.Referrers = append(dto.Referrers, artifactNodeToDTO(ctx, ociClient, trust, verdicts, repo, referrer, node.Digest))
	}

	return dto
}

// VerificationDTO reasons.
const (
	// reasonUntrustedSigner is a signature by a key or identity that is not
	// trusted. The signature itself may be valid.
	reasonUntrustedSigner = "UntrustedSigner"
	// reasonInvalid is a signature that does not verify.
	reasonInvalid = "Invalid"
	// reasonUnsupported is a signature in a format that is not verified.
	reasonUnsupported = "Unsupported"
	// reasonNotChecked is a signature the Product controller has not
	// checked yet, for example one attached after its last check.
	reasonNotChecked = "NotChecked"
)

// recordedVerification returns the verification of the signature with
// digest that the Product controller recorded in verdicts.
func recordedVerification(trust *signing.TrustSet, verdicts map[string]deliveryv1alpha1.SignatureVerdict, digest string) *VerificationDTO {
	verdict, ok := verdicts[digest]
	if !ok {
		return &VerificationDTO{Reason: reasonNotChecked}
	}

	switch verdict.Result {
	case deliveryv1alpha1.SignatureVerified:
		dto := &VerificationDTO{Verified: true}
		if signer := verdict.Signer; signer != nil {
			dto.KeyID, dto.Issuer, dto.Subject = signer.KeyID, signer.Issuer, signer.Subject
			dto.KeyName = trust.KeyName(signer.KeyID)
		}
		return dto
	case deliveryv1alpha1.SignatureUntrustedSigner:
		return &VerificationDTO{Reason: reasonUntrustedSigner, Error: verdict.Message}
	case deliveryv1alpha1.SignatureUnsupported:
		return &VerificationDTO{Reason: reasonUnsupported, Error: verdict.Message}
	default:
		return &VerificationDTO{Reason: reasonInvalid, Error: verdict.Message}
	}
}

// verifyBundle fetches the Sigstore bundle with digest from repo and verifies
// it as a statement about the manifest with subjectDigest, of any predicate
// type.
func verifyBundle(ctx context.Context, ociClient oci.Client, trust *signing.TrustSet, repo oci.Reference, digest, subjectDigest string) (*signing.Verification, error) {
	ref := repo
	ref.Digest = digest

	fetched, err := ociClient.FetchReferrer(ctx, ref)
	if err != nil {
		return nil, err
	}

	return trust.Verify(fetched.Payload, subjectDigest, "")
}

func descriptorToDTO(desc oci.Descriptor) DescriptorDTO {
	return DescriptorDTO{
		MediaType:    desc.MediaType,
		ArtifactType: desc.ArtifactType,
		Digest:       desc.Digest,
		Size:         desc.Size,
		Annotations:  desc.Annotations,
	}
}

func descriptorsToDTO(descs []oci.Descriptor) []DescriptorDTO {
	if len(descs) == 0 {
		return nil
	}
	dtos := make([]DescriptorDTO, 0, len(descs))
	for _, desc := range descs {
		dtos = append(dtos, descriptorToDTO(desc))
	}
	return dtos
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

		conn, err := deps.preparationConnection(r.Context(), uc, prep)
		if err != nil {
			respondConnectError(w, err)
			return
		}

		mediaType, data, err := conn.Client.FetchConfig(r.Context(), subject)
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
