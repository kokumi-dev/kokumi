// Package attestation signs in-toto statements about artifacts, attaches them
// as Sigstore bundle referrers in the shape cosign v3 writes, and finds
// verified ones again. Evidence is always read back from the registry and
// verified; it is never trusted from where it is recorded.
package attestation

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

const (
	statementType = "https://in-toto.io/Statement/v1"
	sha256Prefix  = "sha256:"

	// SignaturePredicateType is the predicate type of the statement that
	// signs an artifact, as "cosign sign" v3 writes it.
	SignaturePredicateType = "https://sigstore.dev/cosign/sign/v1"
)

// ErrNotFound reports that a subject has no Sigstore bundle referrer of the
// requested predicate type.
var ErrNotFound = errors.New("attestation not found")

// Statement is an in-toto v1 statement.
type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     any       `json:"predicate"`
}

// Subject identifies the attested artifact.
type Subject struct {
	Name   string            `json:"name,omitempty"`
	Digest map[string]string `json:"digest"`
}

// NewStatement returns the JSON-encoded statement that predicate holds for
// the artifact subjectName with the given "sha256:<hex>" digest. An empty
// subjectName is left out. Encoding is deterministic for deterministic
// predicates.
func NewStatement(subjectName, digest, predicateType string, predicate any) ([]byte, error) {
	hex, ok := strings.CutPrefix(digest, sha256Prefix)
	if !ok || hex == "" {
		return nil, fmt.Errorf("unsupported subject digest %q: only sha256 is supported", digest)
	}
	data, err := json.Marshal(Statement{
		Type:          statementType,
		Subject:       []Subject{{Name: subjectName, Digest: map[string]string{"sha256": hex}}},
		PredicateType: predicateType,
		Predicate:     predicate,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding in-toto statement: %w", err)
	}
	return data, nil
}

// SignatureStatement returns the statement that signs the artifact with
// digest, as "cosign sign" v3 writes it: a subject with only the digest, and
// an empty predicate.
func SignatureStatement(digest string) ([]byte, error) {
	return NewStatement("", digest, SignaturePredicateType, struct{}{})
}

// Sign attaches the signature of the manifest at subject: the statement
// "cosign sign" v3 writes, signed by signer. It returns the referrer's
// digest. Retries are idempotent.
func Sign(ctx context.Context, client oci.Client, signer *signing.Signer, subject oci.Reference, created time.Time) (string, error) {
	statement, err := SignatureStatement(subject.Digest)
	if err != nil {
		return "", err
	}
	return Push(ctx, client, signer, subject, SignaturePredicateType, statement, created)
}

// Push signs statement, an in-toto statement of predicateType about the
// manifest at subject, and attaches the bundle as a referrer of subject. It
// returns the referrer's digest. created is recorded as
// org.opencontainers.image.created; together with deterministic signing,
// equal statements yield equal referrers.
//
// A referrer that the signer's key verifies and that carries the same
// statement is reused instead of pushing another one. This keeps retries
// cheap and survives a crash between the push and recording it.
func Push(ctx context.Context, client oci.Client, signer *signing.Signer, subject oci.Reference, predicateType string, statement []byte, created time.Time) (string, error) {
	existing, err := find(ctx, client, signer.TrustSet(), subject, predicateType, func(v *signing.Verification) bool {
		return bytes.Equal(v.Statement, statement)
	})
	switch {
	case err == nil:
		return existing.Digest, nil
	case !errors.Is(err, ErrNotFound) && !errors.Is(err, signing.ErrNotVerified):
		return "", err
	}

	bundle, err := signer.Sign(statement)
	if err != nil {
		return "", err
	}

	digest, err := client.PushReferrer(ctx, subject, oci.ReferrerArtifact{
		ArtifactType: oci.SigstoreBundleMediaType,
		MediaType:    oci.SigstoreBundleMediaType,
		Payload:      bundle,
		Annotations: map[string]string{
			ocispec.AnnotationCreated:         created.UTC().Format(time.RFC3339),
			oci.AnnotationBundleContent:       oci.BundleContentDSSEEnvelope,
			oci.AnnotationBundlePredicateType: predicateType,
			oci.AnnotationInTotoPredicateType: predicateType,
		},
	})
	if err != nil {
		return "", fmt.Errorf("pushing attestation for %s: %w", subject, err)
	}
	return digest, nil
}

// Evidence is a verified attestation found in the registry.
type Evidence struct {
	// Digest is the digest of the referrer manifest.
	Digest string
	// KeyID identifies the key that signed it.
	KeyID string
	// Statement is the signed in-toto statement.
	Statement []byte
}

// Predicate decodes the predicate of the statement into v.
func (e *Evidence) Predicate(v any) error {
	var statement struct {
		Predicate json.RawMessage `json:"predicate"`
	}
	if err := json.Unmarshal(e.Statement, &statement); err != nil {
		return fmt.Errorf("decoding statement: %w", err)
	}
	if err := json.Unmarshal(statement.Predicate, v); err != nil {
		return fmt.Errorf("decoding predicate: %w", err)
	}
	return nil
}

// Find returns the newest referrer of the manifest at subject that is a
// Sigstore bundle signed by a key in trust over an in-toto statement of
// predicateType about subject. It fails with ErrNotFound when subject has no
// bundle of predicateType at all, and with an error wrapping
// signing.ErrNotVerified when none of them verifies.
func Find(ctx context.Context, client oci.Client, trust *signing.TrustSet, subject oci.Reference, predicateType string) (*Evidence, error) {
	return find(ctx, client, trust, subject, predicateType, func(*signing.Verification) bool { return true })
}

func find(ctx context.Context, client oci.Client, trust *signing.TrustSet, subject oci.Reference, predicateType string, accept func(*signing.Verification) bool) (*Evidence, error) {
	referrers, err := client.ListReferrers(ctx, subject, oci.SigstoreBundleMediaType)
	if err != nil {
		return nil, fmt.Errorf("listing attestations of %s: %w", subject, err)
	}

	// The annotation only narrows the candidates; the signed statement
	// decides. A bundle without it is still tried.
	referrers = slices.DeleteFunc(referrers, func(r oci.Descriptor) bool {
		annotated, ok := r.Annotations[oci.AnnotationBundlePredicateType]
		return ok && annotated != predicateType
	})
	if len(referrers) == 0 {
		return nil, fmt.Errorf("%w: %s has no %s attestation", ErrNotFound, subject, predicateType)
	}

	// Newest first, so that Find returns the latest of several.
	slices.SortFunc(referrers, func(a, b oci.Descriptor) int {
		return cmp.Or(
			strings.Compare(b.Annotations[ocispec.AnnotationCreated], a.Annotations[ocispec.AnnotationCreated]),
			strings.Compare(b.Digest, a.Digest),
		)
	})

	var errs []error
	for _, referrer := range referrers {
		evidence, err := verifyReferrer(ctx, client, trust, subject, referrer.Digest, predicateType)
		switch {
		case err == nil && accept(evidence.verification):
			return &evidence.Evidence, nil
		case err == nil:
			errs = append(errs, fmt.Errorf("%w: %s carries another statement", signing.ErrNotVerified, referrer.Digest))
		case errors.Is(err, signing.ErrNotVerified):
			errs = append(errs, err)
		default:
			return nil, err
		}
	}

	return nil, fmt.Errorf("no %s attestation of %s verified: %w", predicateType, subject, errors.Join(errs...))
}

type verifiedEvidence struct {
	Evidence
	verification *signing.Verification
}

// verifyReferrer fetches the referrer with digest of subject and verifies its
// bundle.
func verifyReferrer(ctx context.Context, client oci.Client, trust *signing.TrustSet, subject oci.Reference, digest, predicateType string) (*verifiedEvidence, error) {
	ref := subject
	ref.Tag = ""
	ref.Digest = digest

	fetched, err := client.FetchReferrer(ctx, ref)
	switch {
	case errors.Is(err, oci.ErrPayloadTooLarge):
		return nil, fmt.Errorf("%w: %s: %w", signing.ErrNotVerified, digest, err)
	case err != nil:
		return nil, fmt.Errorf("fetching attestation %s: %w", digest, err)
	case fetched.MediaType != oci.SigstoreBundleMediaType:
		return nil, fmt.Errorf("%w: %s holds %q, not a Sigstore bundle", signing.ErrNotVerified, digest, fetched.MediaType)
	}

	verification, err := trust.Verify(fetched.Payload, subject.Digest, predicateType)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", digest, err)
	}

	return &verifiedEvidence{
		Digest: digest, KeyID: verification.KeyID, Statement: verification.Statement,
		verification: verification,
	}, nil
}
