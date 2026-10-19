// Package sbom finds the SBOMs of images that Kokumi may reuse instead of
// generating one. It reuses only SBOMs that cannot be forged: a scan of a
// forged SBOM listing no packages would come back clean. Anyone who can push
// to a repository can attach referrers to its images, so unsigned SBOM
// referrers are never reused.
package sbom

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

// PredicateType is the in-toto predicate type of the CycloneDX SBOMs Kokumi
// attaches. It is unversioned, as Trivy and cosign match it.
const PredicateType = "https://cyclonedx.org/bom"

// Origin says where a reused or generated SBOM came from.
type Origin string

const (
	// OriginGenerated is an SBOM the scanner generated and Kokumi signed.
	OriginGenerated Origin = "Generated"
	// OriginImage is an SBOM attestation BuildKit wrote into the image index.
	OriginImage Origin = "Image"
	// OriginSigned is an SBOM referrer signed by a key Kokumi trusts.
	OriginSigned Origin = "Signed"
)

const (
	// AnnotationPlatform records on the SBOMs and reports Kokumi attaches
	// which platform of the image index they describe.
	AnnotationPlatform = "kokumi.dev/platform"
	// AnnotationSBOMOrigin records on the reports Kokumi attaches where the
	// SBOM they were computed from came from.
	AnnotationSBOMOrigin = "kokumi.dev/sbom-origin"

	// Annotations BuildKit sets on the attestation manifests of an image
	// index, and on their layers.
	annotationReferenceType   = "vnd.docker.reference.type"
	annotationReferenceDigest = "vnd.docker.reference.digest"
	referenceTypeAttestation  = "attestation-manifest"
	annotationLayerPredicate  = "in-toto.io/predicate-type"

	cycloneDXPredicatePrefix = "https://cyclonedx.org/bom"
	spdxPredicatePrefix      = "https://spdx.dev/Document"
)

// ErrNotFound is returned by Load when the image has no such SBOM.
var ErrNotFound = errors.New("SBOM not found")

// Target is the image whose SBOM is looked for.
type Target struct {
	// Image is the image by digest: an image index or a manifest. Kokumi
	// attaches its SBOMs and reports to it.
	Image oci.Reference
	// Index is the image index, nil for a single-platform image.
	Index *oci.Manifest
	// Platform is the platform of Index that is scanned, "" without one.
	Platform string
	// Manifest is the digest of the manifest that is scanned: the manifest
	// of Platform in Index, or Image itself.
	Manifest string
}

// Found is an SBOM Kokumi may reuse.
type Found struct {
	// Document is the SBOM, CycloneDX or SPDX JSON.
	Document json.RawMessage
	// Digest is the digest of the referrer or the attestation manifest that
	// holds it.
	Digest string
	// Origin says where it came from.
	Origin Origin
}

// Select returns the Target of the image at ref: for an image index, the
// manifest of preferred when the index lists it, else of its first platform.
// A preferred platform without a variant matches any variant, as oras-go
// matches platforms: linux/arm64 selects linux/arm64/v8. Attestation
// manifests are no platforms.
func Select(ctx context.Context, client oci.Client, ref oci.Reference, preferred string) (Target, error) {
	manifest, err := client.FetchManifest(ctx, ref)
	if err != nil {
		return Target{}, err
	}
	target := Target{Image: ref, Manifest: manifest.Digest}
	if len(manifest.Manifests) == 0 {
		return target, nil
	}

	var first *oci.Descriptor
	for i := range manifest.Manifests {
		m := &manifest.Manifests[i]
		if m.Platform == "" || m.Annotations[annotationReferenceType] == referenceTypeAttestation || strings.HasPrefix(m.Platform, "unknown/") {
			continue
		}
		if first == nil {
			first = m
		}
		if platformMatches(m.Platform, preferred) {
			first = m
			break
		}
	}
	if first == nil {
		return Target{}, fmt.Errorf("image index %s lists no platform", ref)
	}
	target.Index = &manifest
	target.Platform = first.Platform
	target.Manifest = first.Digest
	return target, nil
}

// Find returns an SBOM of target that Kokumi may reuse, or nil when there is
// none, first found wins:
//
//  1. an SBOM Kokumi attached for the target's platform, signed by a key in
//     trust;
//  2. an SBOM attestation BuildKit wrote into the image index for the
//     scanned manifest, which the index digest covers;
//  3. another SBOM attached to the image, signed by a key in trust.
//
// Unsigned SBOM referrers and SBOMs signed by other keys are never reused.
func Find(ctx context.Context, client oci.Client, trust *signing.TrustSet, target Target) (*Found, error) {
	signed, err := signedSBOMs(ctx, client, trust, target.Image)
	if err != nil {
		return nil, err
	}

	for _, s := range signed {
		if s.own && s.platform == target.Platform {
			return &s.Found, nil
		}
	}

	if found, err := buildKitSBOM(ctx, client, target); err != nil || found != nil {
		return found, err
	}

	for _, s := range signed {
		if !s.own {
			return &s.Found, nil
		}
	}
	return nil, nil
}

// signedSBOM is a verified SBOM referrer.
type signedSBOM struct {
	Found
	// own is set for the SBOMs Kokumi attaches: CycloneDX under Kokumi's
	// predicate type, with the platform it describes.
	own      bool
	platform string
}

// signedSBOMs returns the SBOM referrers of image that a key in trust
// signed, newest first. Referrers that do not verify are skipped.
func signedSBOMs(ctx context.Context, client oci.Client, trust *signing.TrustSet, image oci.Reference) ([]signedSBOM, error) {
	referrers, err := client.ListReferrers(ctx, image, oci.SigstoreBundleMediaType)
	if err != nil {
		return nil, fmt.Errorf("listing referrers of %s: %w", image, err)
	}

	referrers = slices.DeleteFunc(referrers, func(r oci.Descriptor) bool {
		return !isSBOMPredicate(r.Annotations[oci.AnnotationBundlePredicateType])
	})
	slices.SortFunc(referrers, func(a, b oci.Descriptor) int {
		return cmp.Or(
			strings.Compare(b.Annotations[ocispec.AnnotationCreated], a.Annotations[ocispec.AnnotationCreated]),
			strings.Compare(b.Digest, a.Digest),
		)
	})

	var signed []signedSBOM
	for _, r := range referrers {
		predicateType := r.Annotations[oci.AnnotationBundlePredicateType]
		evidence, err := attestation.VerifyReferrer(ctx, client, trust, image, r.Digest, predicateType)
		if err != nil {
			if errors.Is(err, signing.ErrNotVerified) {
				continue
			}
			return nil, err
		}

		var predicate json.RawMessage
		if err := evidence.Predicate(&predicate); err != nil || len(predicate) == 0 {
			continue
		}
		// Kokumi attaches its SBOMs under its unversioned predicate type,
		// annotated with the platform they describe; the annotation is
		// absent for single-platform images.
		own := predicateType == PredicateType
		origin := OriginSigned
		if own {
			origin = OriginGenerated
		}
		signed = append(signed, signedSBOM{
			Document: predicate,
			Digest:   r.Digest,
			Origin:   origin,
			own:      own,
			platform: r.Annotations[AnnotationPlatform],
		})
	}
	return signed, nil
}

// buildKitSBOM returns the SBOM attestation BuildKit wrote into target's
// index for the scanned manifest, nil when there is none.
func buildKitSBOM(ctx context.Context, client oci.Client, target Target) (*Found, error) {
	if target.Index == nil {
		return nil, nil
	}

	for _, m := range target.Index.Manifests {
		if m.Annotations[annotationReferenceType] != referenceTypeAttestation || m.Annotations[annotationReferenceDigest] != target.Manifest {
			continue
		}

		document, err := attestationSBOM(ctx, client, target.Image, m.Digest)
		if err != nil || document == nil {
			return nil, err
		}
		return &Found{Document: document, Digest: m.Digest, Origin: OriginImage}, nil
	}
	return nil, nil
}

// attestationSBOM returns the SBOM in the attestation manifest digest of
// image's index, nil when it holds none.
func attestationSBOM(ctx context.Context, client oci.Client, image oci.Reference, digest string) (json.RawMessage, error) {
	ref := image
	ref.Tag, ref.Digest = "", digest
	attestations, err := client.FetchManifest(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("fetching attestations %s: %w", digest, err)
	}
	for _, layer := range attestations.Layers {
		if !isSBOMPredicate(layer.Annotations[annotationLayerPredicate]) {
			continue
		}
		data, err := client.FetchBlob(ctx, ref, layer, oci.MaxReferrerPayloadSize)
		if err != nil {
			return nil, fmt.Errorf("fetching SBOM attestation %s: %w", layer.Digest, err)
		}
		var statement struct {
			Predicate json.RawMessage `json:"predicate"`
		}
		if err := json.Unmarshal(data, &statement); err != nil || len(statement.Predicate) == 0 {
			continue
		}
		return statement.Predicate, nil
	}
	return nil, nil
}

// Load returns the SBOM a scan of image recorded: the document of the
// referrer or attestation manifest digest, accepted only as Find accepts it.
// It fails with ErrNotFound when image has no such SBOM.
func Load(ctx context.Context, client oci.Client, trust *signing.TrustSet, image oci.Reference, digest string, origin Origin) (json.RawMessage, error) {
	if origin == OriginImage {
		index, err := client.FetchManifest(ctx, image)
		if err != nil {
			return nil, err
		}
		for _, m := range index.Manifests {
			if m.Digest != digest || m.Annotations[annotationReferenceType] != referenceTypeAttestation {
				continue
			}
			document, err := attestationSBOM(ctx, client, image, digest)
			if err != nil || document != nil {
				return document, err
			}
		}
		return nil, fmt.Errorf("%w: %s lists no SBOM attestation %s", ErrNotFound, image, digest)
	}

	referrers, err := client.ListReferrers(ctx, image, oci.SigstoreBundleMediaType)
	if err != nil {
		return nil, fmt.Errorf("listing referrers of %s: %w", image, err)
	}
	for _, r := range referrers {
		predicateType := r.Annotations[oci.AnnotationBundlePredicateType]
		if r.Digest != digest || !isSBOMPredicate(predicateType) {
			continue
		}
		verified, err := attestation.VerifyReferrer(ctx, client, trust, image, digest, predicateType)
		if err != nil {
			return nil, err
		}
		var document json.RawMessage
		if err := verified.Predicate(&document); err != nil {
			return nil, fmt.Errorf("%w: %w", signing.ErrNotVerified, err)
		}
		return document, nil
	}
	return nil, fmt.Errorf("%w: %s has no SBOM referrer %s", ErrNotFound, image, digest)
}

// platformMatches reports whether the listed platform, os/arch[/variant],
// is the preferred one. A preferred platform without a variant matches any
// variant.
func platformMatches(listed, preferred string) bool {
	if listed == preferred {
		return true
	}
	l, p := strings.Split(listed, "/"), strings.Split(preferred, "/")
	return len(p) == 2 && len(l) == 3 && l[0] == p[0] && l[1] == p[1]
}

// isSBOMPredicate reports whether predicateType is a CycloneDX or SPDX
// document, of any version.
func isSBOMPredicate(predicateType string) bool {
	return strings.HasPrefix(predicateType, cycloneDXPredicatePrefix) || strings.HasPrefix(predicateType, spdxPredicatePrefix)
}
