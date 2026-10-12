package oci

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/registry/remote"
)

const (
	// maxBlobBytes bounds the config and referrer blobs read into memory.
	maxBlobBytes = 4 * 1024 * 1024

	dockerManifestListMediaType = "application/vnd.docker.distribution.manifest.list.v2+json"
)

// isIndex reports whether mediaType identifies an image index.
func isIndex(mediaType string) bool {
	return mediaType == ocispec.MediaTypeImageIndex || mediaType == dockerManifestListMediaType
}

// fetchManifest fetches and decodes the single manifest at reference. It
// returns ErrIsIndex when reference resolves to an image index.
func fetchManifest(ctx context.Context, repo *remote.Repository, reference string) (ocispec.Manifest, error) {
	desc, data, err := oras.FetchBytes(ctx, repo, reference, oras.DefaultFetchBytesOptions)
	if err != nil {
		return ocispec.Manifest{}, fmt.Errorf("fetch manifest %s: %w", reference, err)
	}

	if isIndex(desc.MediaType) {
		return ocispec.Manifest{}, ErrIsIndex
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ocispec.Manifest{}, fmt.Errorf("parse manifest %s: %w", desc.Digest, err)
	}

	return manifest, nil
}

// fetchBlobBytes reads the blob described by desc into memory, rejecting blobs
// larger than maxBlobBytes.
func fetchBlobBytes(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor) ([]byte, error) {
	if desc.Size > maxBlobBytes {
		return nil, fmt.Errorf("blob %s is %d bytes, limit is %d", desc.Digest, desc.Size, maxBlobBytes)
	}

	data, err := content.FetchAll(ctx, repo.Blobs(), desc)
	if err != nil {
		return nil, fmt.Errorf("fetch blob %s: %w", desc.Digest, err)
	}

	return data, nil
}

// FetchConfig returns the config blob of the manifest at ref.
func (c *ORASClient) FetchConfig(ctx context.Context, ref Reference) (string, []byte, error) {
	repo, err := c.newRepository(ref.RepositoryReference())
	if err != nil {
		return "", nil, fmt.Errorf("create repository for %q: %w", ref, err)
	}

	manifest, err := fetchManifest(ctx, repo, ref.GetReference())
	if err != nil {
		return "", nil, fmt.Errorf("fetch config of %s: %w", ref, err)
	}

	data, err := fetchBlobBytes(ctx, repo, manifest.Config)
	if err != nil {
		return "", nil, fmt.Errorf("fetch config of %s: %w", ref, err)
	}

	return manifest.Config.MediaType, data, nil
}

// PushIndex pushes an OCI image index whose children already exist in ref's
// repository. The index is tagged with ref's tag when one is set.
func (c *ORASClient) PushIndex(ctx context.Context, ref Reference, artifactType string, entries []IndexEntry, annotations map[string]string) (string, error) {
	repo, err := c.newRepository(ref.RepositoryReference())
	if err != nil {
		return "", fmt.Errorf("create repository for %q: %w", ref, err)
	}

	index := ocispec.Index{
		SchemaVersion: 2,
		MediaType:     ocispec.MediaTypeImageIndex,
		ArtifactType:  artifactType,
		Manifests:     make([]ocispec.Descriptor, 0, len(entries)),
		Annotations:   annotations,
	}

	for _, entry := range entries {
		child, err := childDescriptor(ctx, repo, entry.Digest)
		if err != nil {
			return "", fmt.Errorf("resolve index child %s: %w", entry.Digest, err)
		}
		child.Annotations = entry.Annotations
		index.Manifests = append(index.Manifests, child)
	}

	data, err := json.Marshal(index)
	if err != nil {
		return "", fmt.Errorf("encode index: %w", err)
	}

	if ref.Tag == "" {
		desc, err := oras.PushBytes(ctx, repo, ocispec.MediaTypeImageIndex, data)
		if err != nil {
			return "", fmt.Errorf("push index to %s: %w", ref, err)
		}
		return desc.Digest.String(), nil
	}

	desc, err := oras.TagBytes(ctx, repo, ocispec.MediaTypeImageIndex, data, ref.Tag)
	if err != nil {
		return "", fmt.Errorf("push index to %s: %w", ref, err)
	}

	return desc.Digest.String(), nil
}

// childDescriptor builds the index descriptor of the manifest with the given
// digest, including its artifact type.
func childDescriptor(ctx context.Context, repo *remote.Repository, dgst string) (ocispec.Descriptor, error) {
	if _, err := digest.Parse(dgst); err != nil {
		return ocispec.Descriptor{}, err
	}

	desc, data, err := oras.FetchBytes(ctx, repo, dgst, oras.DefaultFetchBytesOptions)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	// Both manifests and indexes carry an optional top-level artifactType;
	// manifests without one are identified by their config media type.
	var header struct {
		ArtifactType string             `json:"artifactType"`
		Config       ocispec.Descriptor `json:"config"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("parse manifest: %w", err)
	}

	return ocispec.Descriptor{
		MediaType:    desc.MediaType,
		Digest:       desc.Digest,
		Size:         desc.Size,
		ArtifactType: cmp.Or(header.ArtifactType, header.Config.MediaType),
	}, nil
}

// FetchIndex returns the children of the index at ref, or ErrNotAnIndex.
func (c *ORASClient) FetchIndex(ctx context.Context, ref Reference) ([]IndexEntry, error) {
	repo, err := c.newRepository(ref.RepositoryReference())
	if err != nil {
		return nil, fmt.Errorf("create repository for %q: %w", ref, err)
	}

	desc, data, err := oras.FetchBytes(ctx, repo, ref.GetReference(), oras.DefaultFetchBytesOptions)
	if err != nil {
		return nil, fmt.Errorf("fetch index %s: %w", ref, err)
	}

	if !isIndex(desc.MediaType) {
		return nil, fmt.Errorf("fetch index %s: %w", ref, ErrNotAnIndex)
	}

	var index ocispec.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parse index %s: %w", desc.Digest, err)
	}

	entries := make([]IndexEntry, 0, len(index.Manifests))
	for _, child := range index.Manifests {
		entries = append(entries, IndexEntry{
			Digest:       child.Digest.String(),
			Annotations:  child.Annotations,
			MediaType:    child.MediaType,
			ArtifactType: child.ArtifactType,
			Size:         child.Size,
		})
	}

	return entries, nil
}
