package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/renderer"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/afero"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// OrderResult holds the outcome of processing an Order artifact.
type OrderResult struct {
	SourceRef    string
	SourceDigest string
	DestRef      string
	DestDigest   string
}

// OrderService handles the FS and OCI operations for an Order.
type OrderService struct {
	client   oci.Client
	fs       afero.Fs
	cacheDir string // empty string disables pull caching
}

// NewOrderService returns a new OrderService.
// cacheDir is the directory used to cache pulled OCI blobs between reconciles.
// Pass an empty string to disable caching.
func NewOrderService(client oci.Client, fs afero.Fs, cacheDir string) *OrderService {
	if cacheDir != "" {
		_ = fs.MkdirAll(cacheDir, 0700)
	}

	return &OrderService{
		client:   client,
		fs:       fs,
		cacheDir: cacheDir,
	}
}

// ProcessOrder pulls the source artifact, applies patches and edits or normalizes YAML,
// pushes the result to the destination, and returns the source/dest refs and digests.
// The effective source, render, patches, and edits are passed explicitly so that
// Menu-based Orders can supply merged values.
// destination is the fully-qualified OCI URL to push the result to; the caller
// is responsible for supplying the default when the Order has none configured.
// commitMessage is attached as org.opencontainers.image.description on the OCI manifest.
// An empty string defaults to "automatically generated".
// parentDigest is the digest of the artifact produced by the immediately preceding
// Preparation for this Order. When non-empty it is stored as the kokumi.dev/parent
// annotation on the pushed OCI manifest. Pass an empty string for the first Preparation.
func (rs *OrderService) ProcessOrder(
	ctx context.Context,
	order *deliveryv1alpha1.Order,
	source deliveryv1alpha1.OCISource,
	render *deliveryv1alpha1.Render,
	patches []deliveryv1alpha1.Patch,
	edits []deliveryv1alpha1.Patch,
	destination string,
	commitMessage string,
	parentDigest string,
) (*OrderResult, error) {
	logger := log.FromContext(ctx)

	sourceRef := strings.TrimPrefix(source.OCI, "oci://")
	destRef := strings.TrimPrefix(destination, "oci://")

	logger.Info("Processing artifact", "source", sourceRef, "destination", destRef, "version", source.Version)

	tempDir, err := afero.TempDir(rs.fs, "", "order-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer rs.fs.RemoveAll(tempDir) //nolint:errcheck

	logger.Info("Fetching artifact from source")

	mediaType, sourceDigest, err := rs.pullWithCache(ctx, sourceRef, source.Version, tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to pull artifact: %w", err)
	}

	manifestPath := filepath.Join(tempDir, "manifest.yaml")

	logger.Info("Pulled source artifact", "digest", sourceDigest, "mediaType", mediaType)

	if render != nil && render.Helm != nil {
		if mediaType != oci.HelmChartLayerMediaType {
			return nil, fmt.Errorf("source is not a Helm chart (got media type %q)", mediaType)
		}

		logger.Info("Applying Helm renderer")

		vals, err := jsonToMap(render.Helm.Values)
		if err != nil {
			return nil, fmt.Errorf("failed convert values: %w", err)
		}

		releaseName := render.Helm.ReleaseName
		if releaseName == "" {
			releaseName = order.Name
		}
		helmNamespace := render.Helm.Namespace
		if helmNamespace == "" {
			helmNamespace = order.Namespace
		}

		chartPath := filepath.Join(tempDir, "chart.tgz")

		manifest, err := renderer.RenderChart(
			ctx,
			chartPath,
			releaseName,
			helmNamespace,
			render.Helm.IncludeCRDs,
			vals,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to render Helm chart: %w", err)
		}

		if err := afero.WriteFile(rs.fs, manifestPath, []byte(manifest), 0600); err != nil {
			return nil, fmt.Errorf("failed to write manifest: %w", err)
		}
	}

	content, err := afero.ReadFile(rs.fs, manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	processedContent, err := rs.processManifest(ctx, content, patches, edits)
	if err != nil {
		return nil, err
	}

	if err := afero.WriteFile(rs.fs, manifestPath, processedContent, 0600); err != nil {
		return nil, fmt.Errorf("failed to write manifest: %w", err)
	}

	logger.Info("Pushing artifact to destination")

	ociAnnotations := map[string]string{
		ocispec.AnnotationDescription: commitMessage,
	}
	if parentDigest != "" {
		ociAnnotations[oci.AnnotationParentDigest] = parentDigest
	}

	destDigest, err := rs.client.Push(ctx, destRef, source.Version, tempDir, ociAnnotations)
	if err != nil {
		return nil, fmt.Errorf("failed to push artifact: %w", err)
	}

	logger.Info("Successfully processed artifact", "digest", destDigest)

	return &OrderResult{
		SourceRef:    sourceRef,
		SourceDigest: sourceDigest,
		DestRef:      destRef,
		DestDigest:   destDigest,
	}, nil
}

// PreviewOrder pulls the source artifact, applies patches and edits (or normalises YAML),
// and returns the processed manifest bytes without pushing anything to a registry.
// name and namespace are used only as Helm releaseName/namespace fallbacks when the
// Render config does not specify them explicitly.
func (rs *OrderService) PreviewOrder(
	ctx context.Context,
	source deliveryv1alpha1.OCISource,
	render *deliveryv1alpha1.Render,
	patches []deliveryv1alpha1.Patch,
	edits []deliveryv1alpha1.Patch,
	name string,
	namespace string,
) ([]byte, error) {
	logger := log.FromContext(ctx)

	sourceRef := strings.TrimPrefix(source.OCI, "oci://")

	logger.Info("Previewing artifact", "source", sourceRef, "version", source.Version)

	tempDir, err := afero.TempDir(rs.fs, "", "order-preview-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer rs.fs.RemoveAll(tempDir) //nolint:errcheck

	mediaType, _, err := rs.pullWithCache(ctx, sourceRef, source.Version, tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to pull artifact: %w", err)
	}

	manifestPath := filepath.Join(tempDir, "manifest.yaml")

	if render != nil && render.Helm != nil {
		if mediaType != oci.HelmChartLayerMediaType {
			return nil, fmt.Errorf("source is not a Helm chart (got media type %q)", mediaType)
		}

		vals, err := jsonToMap(render.Helm.Values)
		if err != nil {
			return nil, fmt.Errorf("failed convert values: %w", err)
		}

		releaseName := render.Helm.ReleaseName
		if releaseName == "" {
			releaseName = name
		}
		helmNamespace := render.Helm.Namespace
		if helmNamespace == "" {
			helmNamespace = namespace
		}

		chartPath := filepath.Join(tempDir, "chart.tgz")

		manifest, err := renderer.RenderChart(ctx, chartPath, releaseName, helmNamespace, render.Helm.IncludeCRDs, vals)
		if err != nil {
			return nil, fmt.Errorf("failed to render Helm chart: %w", err)
		}

		if err := afero.WriteFile(rs.fs, manifestPath, []byte(manifest), 0600); err != nil {
			return nil, fmt.Errorf("failed to write manifest: %w", err)
		}
	}

	content, err := afero.ReadFile(rs.fs, manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	return rs.processManifest(ctx, content, patches, edits)
}

// processManifest applies patches and edits when present, otherwise normalizes YAML formatting.
// Patches are applied first, then edits on top.
func (rs *OrderService) processManifest(ctx context.Context, content []byte, patches, edits []deliveryv1alpha1.Patch) ([]byte, error) {
	logger := log.FromContext(ctx)

	if len(patches) == 0 && len(edits) == 0 {
		logger.Info("Normalizing YAML formatting")

		processed, err := renderer.NormalizeYAML(content)
		if err != nil {
			return nil, fmt.Errorf("failed to normalize YAML: %w", err)
		}

		return processed, nil
	}

	result := content

	if len(patches) > 0 {
		logger.Info("Applying patches", "count", len(patches))

		processed, err := renderer.ApplyPatches(ctx, result, patches)
		if err != nil {
			return nil, fmt.Errorf("failed to apply patches: %w", err)
		}

		result = processed
	}

	if len(edits) > 0 {
		logger.Info("Applying edits", "count", len(edits))

		processed, err := renderer.ApplyPatches(ctx, result, edits)
		if err != nil {
			return nil, fmt.Errorf("failed to apply edits: %w", err)
		}

		result = processed
	}

	return result, nil
}

// cacheEntry is the metadata written alongside a cached artifact blob.
type cacheEntry struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
}

// pullCacheKey returns a filesystem-safe directory name for the given OCI ref + version.
func pullCacheKey(ref, version string) string {
	sum := sha256.Sum256([]byte(ref + "@" + version))
	return fmt.Sprintf("%x", sum)
}

// artifactFilename returns the filename used for a cached artifact based on its media type.
func artifactFilename(mediaType string) string {
	if mediaType == oci.HelmChartLayerMediaType {
		return "chart.tgz"
	}

	return "manifest.yaml"
}

// pullWithCache returns a previously cached artifact when available, otherwise
// pulls from the OCI registry and caches the result for future reconciles.
// Version tags are treated as immutable — if a tag is re-pushed with different
// content, remove the cache directory to force a fresh pull.
func (rs *OrderService) pullWithCache(ctx context.Context, ref, version, workDir string) (string, string, error) {
	logger := log.FromContext(ctx)

	if rs.cacheDir == "" {
		return rs.client.Pull(ctx, ref, version, workDir)
	}

	key := pullCacheKey(ref, version)
	entryDir := filepath.Join(rs.cacheDir, key)
	metaPath := filepath.Join(entryDir, "meta.json")

	if metaBytes, err := afero.ReadFile(rs.fs, metaPath); err == nil {
		var entry cacheEntry
		if err := json.Unmarshal(metaBytes, &entry); err == nil {
			src := filepath.Join(entryDir, artifactFilename(entry.MediaType))
			dst := filepath.Join(workDir, artifactFilename(entry.MediaType))

			if data, err := afero.ReadFile(rs.fs, src); err == nil {
				if err := afero.WriteFile(rs.fs, dst, data, 0600); err == nil {
					logger.Info("Pulled source artifact from cache", "ref", ref, "version", version, "digest", entry.Digest)
					return entry.MediaType, entry.Digest, nil
				}
			}
		}

		logger.Info("Cache entry invalid, re-pulling source artifact", "ref", ref, "version", version)
	}

	mediaType, digest, err := rs.client.Pull(ctx, ref, version, workDir)
	if err != nil {
		return "", "", err
	}

	rs.populateCache(ctx, entryDir, metaPath, mediaType, digest, workDir)

	return mediaType, digest, nil
}

// populateCache writes the pulled artifact and its metadata to the cache entry
// directory. Errors are non-fatal and only logged as informational messages.
func (rs *OrderService) populateCache(ctx context.Context, entryDir, metaPath, mediaType, digest, workDir string) {
	logger := log.FromContext(ctx)

	if err := rs.fs.MkdirAll(entryDir, 0700); err != nil {
		logger.Info("Could not create cache entry directory, skipping cache", "error", err)
		return
	}

	src := filepath.Join(workDir, artifactFilename(mediaType))
	dst := filepath.Join(entryDir, artifactFilename(mediaType))

	data, err := afero.ReadFile(rs.fs, src)
	if err != nil {
		logger.Info("Could not read artifact for caching, skipping cache", "error", err)
		return
	}

	if err := afero.WriteFile(rs.fs, dst, data, 0600); err != nil {
		logger.Info("Could not write artifact to cache, skipping cache", "error", err)
		return
	}

	metaBytes, err := json.Marshal(cacheEntry{MediaType: mediaType, Digest: digest})
	if err != nil {
		return
	}

	if err := afero.WriteFile(rs.fs, metaPath, metaBytes, 0600); err != nil {
		logger.Info("Could not write cache metadata, skipping cache", "error", err)
	}
}

func jsonToMap(j *apiextensionsv1.JSON) (map[string]any, error) {
	if j == nil || len(j.Raw) == 0 {
		return map[string]any{}, nil
	}

	var vals map[string]any
	if err := json.Unmarshal(j.Raw, &vals); err != nil {
		return nil, fmt.Errorf("unmarshal helm values: %w", err)
	}

	return vals, nil
}
