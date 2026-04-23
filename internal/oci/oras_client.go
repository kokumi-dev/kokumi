package oci

import (
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
	ctrl "sigs.k8s.io/controller-runtime"
)

// ORASClient implements Client using the ORAS library.
// It automatically uses plain HTTP for in-cluster Kubernetes service URLs
// (hosts ending in .svc, .svc.<domain>, or bare IP/localhost) and HTTPS
// for all other hosts.
type ORASClient struct{}

var _ Client = (*ORASClient)(nil)

// NewORASClient returns an ORASClient.
func NewORASClient() *ORASClient {
	return &ORASClient{}
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
//   - anything else:           uses oras.Copy, which writes manifest.yaml
//
// The first return value is the layer media type (empty string for non-Helm artifacts).
func (c *ORASClient) Pull(ctx context.Context, ref, tag, targetDir string) (string, string, error) {
	log := ctrl.LoggerFrom(ctx)

	repo, err := remote.NewRepository(ref)
	if err != nil {
		return "", "", fmt.Errorf("create repository for %q: %w", ref, err)
	}

	repo.PlainHTTP = isPlainHTTP(ref)

	log.Info("Resolving OCI manifest", "ref", fmt.Sprintf("%s:%s", ref, tag))

	manifestDesc, err := repo.Resolve(ctx, tag)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s:%s: %w", ref, tag, err)
	}

	rc, err := repo.Fetch(ctx, manifestDesc)
	if err != nil {
		return "", "", fmt.Errorf("fetch manifest %s: %w", manifestDesc.Digest, err)
	}
	defer rc.Close() //nolint:errcheck

	manifestBytes, err := io.ReadAll(rc)
	if err != nil {
		return "", "", fmt.Errorf("read manifest %s: %w", manifestDesc.Digest, err)
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return "", "", fmt.Errorf("parse manifest: %w", err)
	}

	digest := manifestDesc.Digest.String()

	if len(manifest.Layers) > 0 && manifest.Layers[0].MediaType == HelmChartLayerMediaType {
		log.Info("Pulling Helm chart blob", "ref", fmt.Sprintf("%s:%s", ref, tag))

		if err := c.fetchBlob(ctx, repo, manifest.Layers[0], filepath.Join(targetDir, "chart.tgz")); err != nil {
			return "", "", fmt.Errorf("fetch helm chart blob: %w", err)
		}

		return HelmChartLayerMediaType, digest, nil
	}

	log.Info("Pulling OCI artifact", "ref", fmt.Sprintf("%s:%s", ref, tag))

	fs, err := file.New(targetDir)
	if err != nil {
		return "", "", fmt.Errorf("create file store at %q: %w", targetDir, err)
	}
	defer fs.Close() //nolint:errcheck

	if _, err := oras.Copy(ctx, repo, tag, fs, "", oras.DefaultCopyOptions); err != nil {
		return "", "", fmt.Errorf("pull artifact %s:%s: %w", ref, tag, err)
	}

	return "", digest, nil
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
func (c *ORASClient) ListTags(ctx context.Context, ref string) ([]string, error) {
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return nil, fmt.Errorf("create repository for %q: %w", ref, err)
	}

	repo.PlainHTTP = isPlainHTTP(ref)

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
func (c *ORASClient) Push(ctx context.Context, ref, tag, sourceDir string, annotations map[string]string) (string, error) {
	log := ctrl.LoggerFrom(ctx)

	repo, err := remote.NewRepository(ref)
	if err != nil {
		return "", fmt.Errorf("failed to create repository for %q: %w", ref, err)
	}

	repo.PlainHTTP = isPlainHTTP(ref)

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
