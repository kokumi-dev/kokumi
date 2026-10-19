package oci

import (
	"context"
	"fmt"
	"os"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content/oci"
)

// FetchBlob returns the blob desc describes from ref's repository.
func (c *ORASClient) FetchBlob(ctx context.Context, ref Reference, desc Descriptor, maxSize int64) ([]byte, error) {
	repo, err := c.newRepository(ref.RepositoryReference())
	if err != nil {
		return nil, fmt.Errorf("create repository for %q: %w", ref, err)
	}

	data, err := fetchBlobBytes(ctx, repo, ocispec.Descriptor{
		MediaType: desc.MediaType,
		Digest:    digest.Digest(desc.Digest),
		Size:      desc.Size,
	}, maxSize)
	if err != nil {
		return nil, fmt.Errorf("fetch blob of %s: %w", ref, err)
	}
	return data, nil
}

// CopyToLayout copies the manifest at ref and everything it references into
// an OCI image layout at dir, which it creates, and tags it with tag. For an
// image index and a non-nil platform it copies only the manifest of that
// platform. It returns the descriptor of the copied manifest.
func (c *ORASClient) CopyToLayout(ctx context.Context, ref Reference, dir, tag string, platform *ocispec.Platform) (Descriptor, error) {
	repo, err := c.newRepository(ref.RepositoryReference())
	if err != nil {
		return Descriptor{}, fmt.Errorf("create repository for %q: %w", ref, err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Descriptor{}, fmt.Errorf("create layout %s: %w", dir, err)
	}
	store, err := oci.NewWithContext(ctx, dir)
	if err != nil {
		return Descriptor{}, fmt.Errorf("open layout %s: %w", dir, err)
	}

	opts := oras.CopyOptions{CopyGraphOptions: oras.DefaultCopyGraphOptions}
	if platform != nil {
		opts.WithTargetPlatform(platform)
	}
	desc, err := oras.Copy(ctx, repo, ref.GetReference(), store, tag, opts)
	if err != nil {
		return Descriptor{}, fmt.Errorf("copy %s to layout: %w", ref, err)
	}
	return Descriptor{MediaType: desc.MediaType, ArtifactType: desc.ArtifactType, Digest: desc.Digest.String(), Size: desc.Size}, nil
}

// ExtractLayers extracts every tar layer of the image manifest at ref, plain
// or gzip-compressed, into targetDir. It streams the layers instead of
// reading them into memory, and rejects entries that would escape
// targetDir. It returns the descriptor of the manifest.
func (c *ORASClient) ExtractLayers(ctx context.Context, ref Reference, targetDir string) (Descriptor, error) {
	repo, err := c.newRepository(ref.RepositoryReference())
	if err != nil {
		return Descriptor{}, fmt.Errorf("create repository for %q: %w", ref, err)
	}

	desc, err := repo.Resolve(ctx, ref.GetReference())
	if err != nil {
		return Descriptor{}, fmt.Errorf("resolve %s: %w", ref, err)
	}
	manifest, err := fetchManifest(ctx, repo, desc.Digest.String())
	if err != nil {
		return Descriptor{}, err
	}

	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return Descriptor{}, fmt.Errorf("create %s: %w", targetDir, err)
	}
	for _, layer := range manifest.Layers {
		if err := c.extractLayer(ctx, repo, layer, targetDir); err != nil {
			return Descriptor{}, fmt.Errorf("extract %s: %w", ref, err)
		}
	}
	return Descriptor{MediaType: desc.MediaType, ArtifactType: manifest.ArtifactType, Digest: desc.Digest.String(), Size: desc.Size}, nil
}
