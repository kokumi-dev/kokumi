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
func (c *ORASClient) PushIndex(ctx context.Context, ref Reference, artifactType string, children []Descriptor, annotations map[string]string) (string, error) {
	repo, err := c.newRepository(ref.RepositoryReference())
	if err != nil {
		return "", fmt.Errorf("create repository for %q: %w", ref, err)
	}

	index := ocispec.Index{
		SchemaVersion: 2,
		MediaType:     ocispec.MediaTypeImageIndex,
		ArtifactType:  artifactType,
		Manifests:     make([]ocispec.Descriptor, 0, len(children)),
		Annotations:   annotations,
	}

	for _, child := range children {
		desc, err := childDescriptor(ctx, repo, child.Digest)
		if err != nil {
			return "", fmt.Errorf("resolve index child %s: %w", child.Digest, err)
		}
		desc.Annotations = child.Annotations
		index.Manifests = append(index.Manifests, desc)
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

// FetchManifest returns the image manifest or image index at ref.
func (c *ORASClient) FetchManifest(ctx context.Context, ref Reference) (Manifest, error) {
	repo, err := c.newRepository(ref.RepositoryReference())
	if err != nil {
		return Manifest{}, fmt.Errorf("create repository for %q: %w", ref, err)
	}

	desc, data, err := oras.FetchBytes(ctx, repo, ref.GetReference(), oras.DefaultFetchBytesOptions)
	if err != nil {
		return Manifest{}, fmt.Errorf("fetch manifest %s: %w", ref, err)
	}

	manifest, err := decodeManifest(desc, data)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", desc.Digest, err)
	}

	return manifest, nil
}

// decodeManifest decodes the image manifest or image index data described by
// desc.
func decodeManifest(desc ocispec.Descriptor, data []byte) (Manifest, error) {
	// Image manifests and indexes share artifactType, subject and
	// annotations; only one of config/layers and manifests is set.
	var body struct {
		ArtifactType string               `json:"artifactType"`
		Config       *ocispec.Descriptor  `json:"config"`
		Layers       []ocispec.Descriptor `json:"layers"`
		Manifests    []ocispec.Descriptor `json:"manifests"`
		Subject      *ocispec.Descriptor  `json:"subject"`
		Annotations  map[string]string    `json:"annotations"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{
		MediaType:    desc.MediaType,
		ArtifactType: body.ArtifactType,
		Digest:       desc.Digest.String(),
		Size:         desc.Size,
		Annotations:  body.Annotations,
		Subject:      optionalDescriptor(body.Subject),
	}

	if isIndex(desc.MediaType) {
		manifest.Manifests = descriptorsOf(body.Manifests)
		return manifest, nil
	}

	manifest.Config = optionalDescriptor(body.Config)
	manifest.Layers = descriptorsOf(body.Layers)
	if manifest.ArtifactType == "" && manifest.Config != nil {
		manifest.ArtifactType = manifest.Config.MediaType
	}

	return manifest, nil
}

// descriptorOf converts an OCI descriptor.
func descriptorOf(desc ocispec.Descriptor) Descriptor {
	return Descriptor{
		MediaType:    desc.MediaType,
		ArtifactType: desc.ArtifactType,
		Digest:       desc.Digest.String(),
		Size:         desc.Size,
		Annotations:  desc.Annotations,
	}
}

func optionalDescriptor(desc *ocispec.Descriptor) *Descriptor {
	if desc == nil {
		return nil
	}
	converted := descriptorOf(*desc)
	return &converted
}

func descriptorsOf(descs []ocispec.Descriptor) []Descriptor {
	if len(descs) == 0 {
		return nil
	}
	converted := make([]Descriptor, 0, len(descs))
	for _, desc := range descs {
		converted = append(converted, descriptorOf(desc))
	}
	return converted
}
