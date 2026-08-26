package oci

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	ctrl "sigs.k8s.io/controller-runtime"
)

// ORASClient implements Client using the ORAS library.
// It automatically uses plain HTTP for in-cluster Kubernetes service URLs
// (hosts ending in .svc, .svc.<domain>, or bare IP/localhost) and HTTPS
// for all other hosts.
type ORASClient struct {
	credStore credentials.Store
}

var _ Client = (*ORASClient)(nil)

// NewORASClient returns an anonymous ORASClient.
func NewORASClient() *ORASClient {
	return &ORASClient{}
}

// NewAuthenticatedORASClient returns an ORASClient that authenticates requests
// using the provided credential store.
func NewAuthenticatedORASClient(credStore credentials.Store) *ORASClient {
	return &ORASClient{credStore: credStore}
}

// newRepository creates a configured remote.Repository for the given ref,
// applying plain-HTTP and credential settings.
func (c *ORASClient) newRepository(ref string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return nil, err
	}

	repo.PlainHTTP = isPlainHTTP(ref)

	if c.credStore != nil {
		repo.Client = &auth.Client{
			Credential: c.credStore.Get,
		}
	}

	return repo, nil
}

// isPlainHTTP reports whether ref should be accessed over plain HTTP.
// In-cluster Kubernetes service hostnames (*.svc, *.svc.*) and loopback /
// bare-IP addresses are treated as plain HTTP; everything else uses HTTPS.
func isPlainHTTP(ref string) bool {
	host := ref
	if before, _, ok := strings.Cut(ref, "/"); ok {
		host = before
	}

	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	if host == "localhost" {
		return true
	}

	if net.ParseIP(host) != nil {
		return true
	}

	parts := strings.Split(host, ".")
	for i, p := range parts {
		if p == "svc" && i > 0 {
			return true
		}
	}
	return false
}

// Pull fetches an OCI artifact from ref:tag into targetDir.
// It inspects the manifest's first layer media type and branches accordingly:
//   - HelmChartLayerMediaType: fetches the blob directly to targetDir/chart.tgz
//   - FluxContentMediaType:    extracts the layer's tar archive into targetDir
//     (Flux layers carry no io.deis.oras.content.unpack annotation, so ORAS
//     would otherwise store them as a raw blob)
//   - anything else:           uses oras.Copy with a file store, which unpacks
//     layers annotated with io.deis.oras.content.unpack (e.g. ORAS "file"
//     artifacts) and otherwise stores raw blobs
//
// The first return value is the layer media type (empty string for non-Helm artifacts).
// The third return value is the manifest annotations map (may be nil/empty).
func (c *ORASClient) Pull(ctx context.Context, ref Reference, targetDir string) (string, string, map[string]string, error) {
	log := ctrl.LoggerFrom(ctx)

	repository := ref.RepositoryReference()
	reference := ref.GetReference()

	repo, err := c.newRepository(repository)
	if err != nil {
		return "", "", nil, fmt.Errorf("create repository for %q: %w", ref, err)
	}
	log.Info("Resolving OCI manifest", "ref", fmt.Sprintf("%s:%s", ref, reference))

	manifestDesc, err := repo.Resolve(ctx, reference)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve %s:%s: %w", ref, reference, err)
	}

	rc, err := repo.Fetch(ctx, manifestDesc)
	if err != nil {
		return "", "", nil, fmt.Errorf("fetch manifest %s: %w", manifestDesc.Digest, err)
	}
	defer rc.Close() //nolint:errcheck

	manifestBytes, err := io.ReadAll(rc)
	if err != nil {
		return "", "", nil, fmt.Errorf("read manifest %s: %w", manifestDesc.Digest, err)
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return "", "", nil, fmt.Errorf("parse manifest: %w", err)
	}

	digest := manifestDesc.Digest.String()
	annotations := manifest.Annotations

	if len(manifest.Layers) == 0 {
		return "", "", nil, fmt.Errorf("artifact %s:%s has no layers", ref, reference)
	}

	if manifest.Layers[0].MediaType == HelmChartLayerMediaType {
		log.Info("Pulling Helm chart blob", "ref", fmt.Sprintf("%s:%s", ref, reference))

		if err := c.fetchBlob(ctx, repo, manifest.Layers[0], filepath.Join(targetDir, "chart.tgz")); err != nil {
			return "", "", nil, fmt.Errorf("fetch helm chart blob: %w", err)
		}

		return HelmChartLayerMediaType, digest, annotations, nil
	}

	if manifest.Layers[0].MediaType == FluxContentMediaType {
		log.Info("Pulling Flux content layer", "ref", fmt.Sprintf("%s:%s", ref, reference))

		for _, layer := range manifest.Layers {
			if err := c.extractLayer(ctx, repo, layer, targetDir); err != nil {
				return "", "", nil, fmt.Errorf("extract layer %s: %w", layer.Digest, err)
			}
		}

		return "", digest, annotations, nil
	}

	log.Info("Pulling OCI artifact", "ref", fmt.Sprintf("%s:%s", ref, reference))

	fs, err := file.New(targetDir)
	if err != nil {
		return "", "", nil, fmt.Errorf("create file store at %q: %w", targetDir, err)
	}
	defer fs.Close() //nolint:errcheck

	if _, err := oras.Copy(ctx, repo, reference, fs, "", oras.DefaultCopyOptions); err != nil {
		return "", "", nil, fmt.Errorf("pull artifact %s:%s: %w", ref, reference, err)
	}

	return "", digest, annotations, nil
}

// extractLayer fetches a Flux content layer blob (gzip-compressed tar) and
// extracts its archive into targetDir, preserving file names. It is only ever
// called for layers with media type FluxContentMediaType.
func (c *ORASClient) extractLayer(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, targetDir string) error {
	rc, err := repo.Blobs().Fetch(ctx, desc)
	if err != nil {
		return fmt.Errorf("fetch blob %s: %w", desc.Digest, err)
	}
	defer rc.Close() //nolint:errcheck

	return extractGzipTar(rc, desc.MediaType, targetDir)
}

// extractGzipTar decompresses a gzip-compressed tar stream (or a plain tar
// stream when the media type is not gzip) and extracts it into targetDir.
func extractGzipTar(r io.Reader, mediaType string, targetDir string) error {
	if strings.HasSuffix(mediaType, "+gzip") || mediaType == ocispec.MediaTypeImageLayerGzip {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("decompress blob: %w", err)
		}
		defer gz.Close() //nolint:errcheck
		r = gz
	}

	return extractTar(r, targetDir)
}

// extractTar extracts a tar archive into targetDir, rejecting entries that
// would escape the target directory.
func extractTar(r io.Reader, targetDir string) error {
	tr := tar.NewReader(r)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		name := filepath.Clean(filepath.FromSlash(header.Name))
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("tar entry %q escapes target directory", header.Name)
		}

		destPath := filepath.Join(targetDir, name)
		if err := os.MkdirAll(filepath.Dir(destPath), 0700); err != nil {
			return fmt.Errorf("create directory for %q: %w", name, err)
		}

		f, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600) //nolint:gosec
		if err != nil {
			return fmt.Errorf("create %q: %w", name, err)
		}

		if _, err := io.Copy(f, tr); err != nil {
			f.Close() //nolint:errcheck
			return fmt.Errorf("write %q: %w", name, err)
		}

		if err := f.Close(); err != nil {
			return fmt.Errorf("close %q: %w", name, err)
		}
	}
}

// fetchBlob streams a single OCI layer blob to the given file path.
func (c *ORASClient) fetchBlob(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, destPath string) error {
	rc, err := repo.Blobs().Fetch(ctx, desc)
	if err != nil {
		return fmt.Errorf("fetch blob %s: %w", desc.Digest, err)
	}
	defer rc.Close() //nolint:errcheck

	f, err := os.Create(destPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	defer f.Close() //nolint:errcheck

	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("write blob: %w", err)
	}

	return nil
}

// ListTags returns all tags available for the repository at ref.
func (c *ORASClient) ListTags(ctx context.Context, ref Reference) ([]string, error) {
	repository := ref.RepositoryReference()

	repo, err := c.newRepository(repository)
	if err != nil {
		return nil, fmt.Errorf("create repository for %q: %w", ref, err)
	}
	var tags []string
	err = repo.Tags(ctx, "", func(t []string) error {
		tags = append(tags, t...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list tags for %q: %w", ref, err)
	}

	return tags, nil
}

// Push packages sourceDir as an OCI artifact and pushes it to ref:tag, returning its digest.
// annotations are attached as OCI manifest annotations; pass nil for none.
func (c *ORASClient) Push(ctx context.Context, ref Reference, sourceDir string, annotations map[string]string) (string, error) {
	log := ctrl.LoggerFrom(ctx)

	repository := ref.RepositoryReference()
	reference := ref.GetReference()

	repo, err := c.newRepository(repository)
	if err != nil {
		return "", fmt.Errorf("failed to create repository for %q: %w", ref, err)
	}
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
		Layers:              []ocispec.Descriptor{layerDesc},
		ManifestAnnotations: annotations,
	}

	manifest, err := oras.PackManifest(ctx, fs, oras.PackManifestVersion1_1, oras.MediaTypeUnknownArtifact, packOpts)
	if err != nil {
		return "", fmt.Errorf("failed to pack manifest: %w", err)
	}

	if err := fs.Tag(ctx, manifest, reference); err != nil {
		return "", fmt.Errorf("failed to tag manifest as %q: %w", reference, err)
	}

	log.Info("Pushing OCI artifact", "ref", fmt.Sprintf("%s:%s", ref, reference))

	desc, err := oras.Copy(ctx, fs, reference, repo, reference, oras.DefaultCopyOptions)
	if err != nil {
		return "", fmt.Errorf("failed to push artifact %s:%s: %w", ref, reference, err)
	}

	return desc.Digest.String(), nil
}
