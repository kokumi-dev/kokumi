package oci

import (
	"context"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
)

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
