package oci

import (
	"context"
	"errors"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
)

// MaxReferrerPayloadSize is the largest referrer payload FetchReferrer reads.
// Anyone who can push to a repository can attach a referrer of any size, so
// the bound keeps a hostile referrer from exhausting memory. It allows for
// signed SBOMs and vulnerability reports, which run to megabytes for large
// images.
const MaxReferrerPayloadSize = 16 << 20

// ErrPayloadTooLarge is returned by FetchReferrer for a payload larger than
// MaxReferrerPayloadSize.
var ErrPayloadTooLarge = errors.New("referrer payload too large")

// ReferrerArtifact is a single-blob OCI artifact attached to a subject.
type ReferrerArtifact struct {
	// ArtifactType is the manifest artifactType.
	ArtifactType string
	// MediaType is the media type of the payload blob.
	MediaType string
	// Payload is the blob content.
	Payload []byte
	// Annotations are the manifest annotations. Set
	// org.opencontainers.image.created for a deterministic digest.
	Annotations map[string]string
}

// PushReferrer pushes artifact as an OCI 1.1 referrer of subject. Registries
// without the referrers API are handled by oras via the referrers tag schema.
func (c *ORASClient) PushReferrer(ctx context.Context, subject Reference, artifact ReferrerArtifact) (string, error) {
	repo, err := c.newRepository(subject.RepositoryReference())
	if err != nil {
		return "", fmt.Errorf("create repository for %q: %w", subject, err)
	}

	subjectDesc, err := repo.Resolve(ctx, subject.GetReference())
	if err != nil {
		return "", fmt.Errorf("resolve subject %s: %w", subject, err)
	}

	layerDesc, err := oras.PushBytes(ctx, repo, artifact.MediaType, artifact.Payload)
	if err != nil {
		return "", fmt.Errorf("push referrer blob: %w", err)
	}

	manifestDesc, err := oras.PackManifest(ctx, repo, oras.PackManifestVersion1_1, artifact.ArtifactType, oras.PackManifestOptions{
		Subject:             &subjectDesc,
		Layers:              []ocispec.Descriptor{layerDesc},
		ManifestAnnotations: artifact.Annotations,
	})
	if err != nil {
		return "", fmt.Errorf("push referrer manifest for %s: %w", subject, err)
	}

	return manifestDesc.Digest.String(), nil
}

// ListReferrers lists the referrers of subject. An empty artifactType lists
// all of them.
func (c *ORASClient) ListReferrers(ctx context.Context, subject Reference, artifactType string) ([]Descriptor, error) {
	repo, err := c.newRepository(subject.RepositoryReference())
	if err != nil {
		return nil, fmt.Errorf("create repository for %q: %w", subject, err)
	}

	subjectDesc, err := repo.Resolve(ctx, subject.GetReference())
	if err != nil {
		return nil, fmt.Errorf("resolve subject %s: %w", subject, err)
	}

	var referrers []Descriptor
	err = repo.Referrers(ctx, subjectDesc, artifactType, func(page []ocispec.Descriptor) error {
		referrers = append(referrers, descriptorsOf(page)...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list referrers of %s: %w", subject, err)
	}

	return referrers, nil
}

// FetchReferrer returns the single-blob payload of the referrer at ref.
func (c *ORASClient) FetchReferrer(ctx context.Context, ref Reference) (ReferrerArtifact, error) {
	repo, err := c.newRepository(ref.RepositoryReference())
	if err != nil {
		return ReferrerArtifact{}, fmt.Errorf("create repository for %q: %w", ref, err)
	}

	manifest, err := fetchManifest(ctx, repo, ref.GetReference())
	if err != nil {
		return ReferrerArtifact{}, fmt.Errorf("fetch referrer %s: %w", ref, err)
	}

	if len(manifest.Layers) != 1 {
		return ReferrerArtifact{}, fmt.Errorf("referrer %s has %d layers, want 1", ref, len(manifest.Layers))
	}

	if size := manifest.Layers[0].Size; size > MaxReferrerPayloadSize {
		return ReferrerArtifact{}, fmt.Errorf("fetch referrer %s: %d bytes: %w", ref, size, ErrPayloadTooLarge)
	}

	payload, err := fetchBlobBytes(ctx, repo, manifest.Layers[0], MaxReferrerPayloadSize)
	if err != nil {
		return ReferrerArtifact{}, fmt.Errorf("fetch referrer %s: %w", ref, err)
	}

	return ReferrerArtifact{
		ArtifactType: manifest.ArtifactType,
		MediaType:    manifest.Layers[0].MediaType,
		Payload:      payload,
		Annotations:  manifest.Annotations,
	}, nil
}
