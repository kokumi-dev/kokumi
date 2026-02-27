package oci

import (
	"context"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/registry/remote"
	ctrl "sigs.k8s.io/controller-runtime"
)

// ORASClient implements Client using the ORAS library.
type ORASClient struct {
	// PlainHTTP disables TLS when communicating with the registry.
	PlainHTTP bool
}

var _ Client = (*ORASClient)(nil)

// NewORASClient returns an ORASClient.
func NewORASClient(plainHTTP bool) *ORASClient {
	return &ORASClient{PlainHTTP: plainHTTP}
}

// Pull fetches an OCI artifact from ref:tag into targetDir and returns its digest.
func (c *ORASClient) Pull(ctx context.Context, ref, tag, targetDir string) (string, error) {
	log := ctrl.LoggerFrom(ctx)

	repo, err := remote.NewRepository(ref)
	if err != nil {
		return "", fmt.Errorf("failed to create repository for %q: %w", ref, err)
	}

	repo.PlainHTTP = c.PlainHTTP

	fs, err := file.New(targetDir)
	if err != nil {
		return "", fmt.Errorf("failed to create file store at %q: %w", targetDir, err)
	}
	defer fs.Close() //nolint:errcheck

	log.Info("Pulling OCI artifact", "ref", fmt.Sprintf("%s:%s", ref, tag))

	desc, err := oras.Copy(ctx, repo, tag, fs, "", oras.DefaultCopyOptions)
	if err != nil {
		return "", fmt.Errorf("failed to pull artifact %s:%s: %w", ref, tag, err)
	}

	return desc.Digest.String(), nil
}

// Push packages sourceDir as an OCI artifact and pushes it to ref:tag, returning its digest.
func (c *ORASClient) Push(ctx context.Context, ref, tag, sourceDir string) (string, error) {
	log := ctrl.LoggerFrom(ctx)

	repo, err := remote.NewRepository(ref)
	if err != nil {
		return "", fmt.Errorf("failed to create repository for %q: %w", ref, err)
	}

	repo.PlainHTTP = c.PlainHTTP

	fs, err := file.New(sourceDir)
	if err != nil {
		return "", fmt.Errorf("failed to create file store at %q: %w", sourceDir, err)
	}
	defer fs.Close() //nolint:errcheck

	layerDesc, err := fs.Add(ctx, ".", "application/vnd.oci.image.layer.v1.tar+gzip", ".")
	if err != nil {
		return "", fmt.Errorf("failed to add directory to file store: %w", err)
	}

	packOpts := oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{layerDesc},
	}

	manifest, err := oras.PackManifest(ctx, fs, oras.PackManifestVersion1_1, oras.MediaTypeUnknownArtifact, packOpts)
	if err != nil {
		return "", fmt.Errorf("failed to pack manifest: %w", err)
	}

	if err := fs.Tag(ctx, manifest, tag); err != nil {
		return "", fmt.Errorf("failed to tag manifest as %q: %w", tag, err)
	}

	log.Info("Pushing OCI artifact", "ref", fmt.Sprintf("%s:%s", ref, tag))

	desc, err := oras.Copy(ctx, fs, tag, repo, tag, oras.DefaultCopyOptions)
	if err != nil {
		return "", fmt.Errorf("failed to push artifact %s:%s: %w", ref, tag, err)
	}

	return desc.Digest.String(), nil
}
