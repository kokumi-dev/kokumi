package service

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/renderer"
	"github.com/kokumi-dev/kokumi/internal/scmlink"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/afero"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// OrderResult holds the outcome of processing an Order artifact.
type OrderResult struct {
	SourceRef     oci.Reference
	DestRef       oci.Reference
	GitRepo       string
	GitTag        string
	GitCommitHash string
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
// sourceClient and destClient are optional authenticated OCI clients. When nil, the
// service's default client (usually anonymous) is used for that operation.
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
	sourceClient oci.Client,
	destClient oci.Client,
) (*OrderResult, error) {
	logger := log.FromContext(ctx)

	srcClient := cmp.Or(sourceClient, rs.client)
	dstClient := cmp.Or(destClient, rs.client)

	sourceRef, err := oci.Parse(source.OCI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse OCI Ref: %w", err)
	}
	sourceRef.Tag = source.Version

	destRef, err := oci.Parse(destination)
	if err != nil {
		return nil, fmt.Errorf("failed to parse OCI Ref: %w", err)
	}
	destRef.Tag = source.Version

	logger.Info("Processing artifact", "source", sourceRef.RepositoryReference(), "destination", destRef.RepositoryReference(), "version", source.Version)

	tempDir, err := afero.TempDir(rs.fs, "", "order-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer rs.fs.RemoveAll(tempDir) //nolint:errcheck

	logger.Info("Fetching artifact from source")

	layout := layoutPolicy(render)
	mediaType, sourceDigest, sourceAnnotations, err := rs.pullWithCache(ctx, srcClient, sourceRef, tempDir, layout)
	if err != nil {
		return nil, fmt.Errorf("failed to pull artifact: %w", err)
	}
	sourceRef.Digest = sourceDigest

	manifestPath := filepath.Join(tempDir, "manifest.yaml")

	logger.Info("Pulled source artifact", "digest", sourceDigest, "mediaType", mediaType)

	repo, tag, commitHash := scmlink.Resolve(sourceAnnotations)

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

	if err := rs.processManifestFiles(ctx, tempDir, manifestPath, patches, edits); err != nil {
		return nil, err
	}

	logger.Info("Pushing artifact to destination")

	ociAnnotations := map[string]string{
		ocispec.AnnotationDescription: commitMessage,
	}
	if parentDigest != "" {
		ociAnnotations[oci.AnnotationParentDigest] = parentDigest
	}

	if repo != "" {
		ociAnnotations[ocispec.AnnotationSource] = repo
	}
	if tag != "" {
		ociAnnotations[ocispec.AnnotationVersion] = tag
	}
	if commitHash != "" {
		ociAnnotations[ocispec.AnnotationRevision] = commitHash
	}
	ociAnnotations[ocispec.AnnotationBaseImageName] = sourceRef.RepositoryReference()
	ociAnnotations[ocispec.AnnotationBaseImageDigest] = sourceRef.Digest

	destDigest, err := dstClient.Push(ctx, destRef, tempDir, ociAnnotations)
	if err != nil {
		return nil, fmt.Errorf("failed to push artifact: %w", err)
	}
	destRef.Digest = destDigest

	logger.Info("Successfully processed artifact", "digest", destDigest)

	return &OrderResult{
		SourceRef:     sourceRef,
		DestRef:       destRef,
		GitRepo:       repo,
		GitTag:        tag,
		GitCommitHash: commitHash,
	}, nil
}

// PreviewOrder pulls the source artifact, applies patches and edits (or normalises YAML),
// and returns the processed manifest bytes without pushing anything to a registry.
// name and namespace are used only as Helm releaseName/namespace fallbacks when the
// Render config does not specify them explicitly.
// sourceClient is an optional authenticated OCI client; when nil the service's
// default client is used.
func (rs *OrderService) PreviewOrder(
	ctx context.Context,
	source deliveryv1alpha1.OCISource,
	render *deliveryv1alpha1.Render,
	patches []deliveryv1alpha1.Patch,
	edits []deliveryv1alpha1.Patch,
	name string,
	namespace string,
	sourceClient oci.Client,
) ([]byte, error) {
	logger := log.FromContext(ctx)

	srcClient := cmp.Or(sourceClient, rs.client)
	sourceRef, err := oci.Parse(source.OCI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse OCI Ref: %w", err)
	}
	sourceRef.Tag = source.Version

	logger.Info("Previewing artifact", "source", sourceRef.RepositoryReference(), "version", source.Version)

	tempDir, err := afero.TempDir(rs.fs, "", "order-preview-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer rs.fs.RemoveAll(tempDir) //nolint:errcheck

	layout := layoutPolicy(render)
	mediaType, _, _, err := rs.pullWithCache(ctx, srcClient, sourceRef, tempDir, layout)
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

	if err := rs.renderToDir(ctx, tempDir, render, mediaType, patches, edits, name, namespace); err != nil {
		return nil, err
	}

	return concatYAMLFiles(rs.fs, tempDir)
}

// PreviewFile is a single rendered YAML file of a previewed artifact.
type PreviewFile struct {
	Path    string
	Content string
}

// PreviewFiles runs the same pull/render/patch pipeline as PreviewOrder but
// returns the rendered files individually, preserving the artifact's file
// layout. Multi-document files are returned as-is.
func (rs *OrderService) PreviewFiles(
	ctx context.Context,
	source deliveryv1alpha1.OCISource,
	render *deliveryv1alpha1.Render,
	patches []deliveryv1alpha1.Patch,
	edits []deliveryv1alpha1.Patch,
	name string,
	namespace string,
	sourceClient oci.Client,
) ([]PreviewFile, error) {
	srcClient := cmp.Or(sourceClient, rs.client)
	sourceRef, err := oci.Parse(source.OCI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse OCI Ref: %w", err)
	}
	sourceRef.Tag = source.Version

	tempDir, err := afero.TempDir(rs.fs, "", "order-preview-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer rs.fs.RemoveAll(tempDir) //nolint:errcheck

	layout := layoutPolicy(render)
	mediaType, _, _, err := rs.pullWithCache(ctx, srcClient, sourceRef, tempDir, layout)
	if err != nil {
		return nil, fmt.Errorf("failed to pull artifact: %w", err)
	}

	if err := rs.renderToDir(ctx, tempDir, render, mediaType, patches, edits, name, namespace); err != nil {
		return nil, err
	}

	files, err := yamlFiles(rs.fs, tempDir)
	if err != nil {
		return nil, err
	}

	out := make([]PreviewFile, 0, len(files))
	for _, file := range files {
		content, err := afero.ReadFile(rs.fs, file)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", file, err)
		}
		out = append(out, PreviewFile{Path: filepath.Base(file), Content: string(content)})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no YAML files found in %q", tempDir)
	}

	return out, nil
}

// renderToDir renders the pulled artifact in dir: it renders Helm charts into
// manifest.yaml and applies patches and edits to the manifest file(s).
func (rs *OrderService) renderToDir(ctx context.Context, dir string, render *deliveryv1alpha1.Render, mediaType string, patches, edits []deliveryv1alpha1.Patch, name, namespace string) error {
	manifestPath := filepath.Join(dir, "manifest.yaml")

	if render != nil && render.Helm != nil {
		if mediaType != oci.HelmChartLayerMediaType {
			return fmt.Errorf("source is not a Helm chart (got media type %q)", mediaType)
		}

		vals, err := jsonToMap(render.Helm.Values)
		if err != nil {
			return fmt.Errorf("failed convert values: %w", err)
		}

		releaseName := render.Helm.ReleaseName
		if releaseName == "" {
			releaseName = name
		}
		helmNamespace := render.Helm.Namespace
		if helmNamespace == "" {
			helmNamespace = namespace
		}

		chartPath := filepath.Join(dir, "chart.tgz")

		manifest, err := renderer.RenderChart(ctx, chartPath, releaseName, helmNamespace, render.Helm.IncludeCRDs, vals)
		if err != nil {
			return fmt.Errorf("failed to render Helm chart: %w", err)
		}

		if err := afero.WriteFile(rs.fs, manifestPath, []byte(manifest), 0600); err != nil {
			return fmt.Errorf("failed to write manifest: %w", err)
		}
	}

	return rs.processManifestFiles(ctx, dir, manifestPath, patches, edits)
}

// processManifestFiles applies patches and edits to the manifest content in dir.
// When manifestPath exists, only that file is processed. Otherwise every
// top-level YAML file is processed individually, preserving the artifact's
// original file layout.
func (rs *OrderService) processManifestFiles(ctx context.Context, dir, manifestPath string, patches, edits []deliveryv1alpha1.Patch) error {
	if _, err := rs.fs.Stat(manifestPath); err == nil {
		return rs.processManifestFile(ctx, manifestPath, patches, edits)
	}

	files, err := yamlFiles(rs.fs, dir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if err := rs.processManifestFile(ctx, file, patches, edits); err != nil {
			return err
		}
	}

	return nil
}

// processManifestFile applies patches and edits to a single YAML file in place.
func (rs *OrderService) processManifestFile(ctx context.Context, path string, patches, edits []deliveryv1alpha1.Patch) error {
	content, err := afero.ReadFile(rs.fs, path)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	processed, err := rs.processManifest(ctx, content, patches, edits)
	if err != nil {
		return err
	}

	if err := afero.WriteFile(rs.fs, path, processed, 0600); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}

// yamlFiles returns the sorted top-level YAML file paths in dir.
// ponytail: top-level only, recurse if real artifacts ship nested dirs.
func yamlFiles(fs afero.Fs, dir string) ([]string, error) {
	var files []string
	for _, pattern := range []string{"*.yaml", "*.yml"} {
		matches, err := afero.Glob(fs, filepath.Join(dir, pattern))
		if err != nil {
			return nil, fmt.Errorf("read directory %q: %w", dir, err)
		}
		files = append(files, matches...)
	}

	sort.Strings(files)

	return files, nil
}

// concatYAMLFiles reads all top-level YAML files in dir and returns them
// concatenated with "---" separators, each preceded by a Source comment.
func concatYAMLFiles(fs afero.Fs, dir string) ([]byte, error) {
	files, err := yamlFiles(fs, dir)
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no YAML files found in %q", dir)
	}

	var out strings.Builder
	for _, file := range files {
		content, err := afero.ReadFile(fs, file)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", file, err)
		}

		fmt.Fprintf(&out, "---\n# Source: %s\n%s\n", filepath.Base(file), strings.TrimSpace(string(content)))
	}

	return []byte(out.String()), nil
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
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// pullCacheKey returns a filesystem-safe directory name for the given OCI ref + version.
// The file layout is mixed in because merged and separate file layouts
// must not share a cache entry.
func pullCacheKey(ref oci.Reference, layout deliveryv1alpha1.FileLayout) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s#%s", ref.String(), layout))
	return fmt.Sprintf("%x", sum)
}

// layoutPolicy returns the effective file layout for a raw manifest
// source, defaulting to Single when unset.
func layoutPolicy(render *deliveryv1alpha1.Render) deliveryv1alpha1.FileLayout {
	if render != nil && render.Manifest != nil && render.Manifest.Layout != "" {
		return render.Manifest.Layout
	}

	return deliveryv1alpha1.FileLayoutSingle
}

// hasKustomization reports whether dir contains a top-level kustomization file.
func hasKustomization(fs afero.Fs, dir string) bool {
	for _, name := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization"} {
		if _, err := fs.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}

	return false
}

// pullWithCache returns a previously cached artifact when available, otherwise
// pulls from the OCI registry and caches the result for future reconciles.
// Version tags are treated as immutable — if a tag is re-pushed with different
// content, remove the cache directory to force a fresh pull.
// Non-Helm artifacts are merged into a single manifest.yaml unless the file
// layout is Multi or the artifact contains a kustomization file.
// It returns the media type, manifest digest, manifest annotations, and any error.
func (rs *OrderService) pullWithCache(ctx context.Context, client oci.Client, ref oci.Reference, workDir string, layout deliveryv1alpha1.FileLayout) (string, string, map[string]string, error) {
	logger := log.FromContext(ctx)

	if rs.cacheDir == "" {
		mediaType, digest, annotations, err := client.Pull(ctx, ref, workDir)
		if err != nil {
			return "", "", nil, err
		}

		if err := rs.consolidatePulled(workDir, mediaType, layout); err != nil {
			return "", "", nil, err
		}

		return mediaType, digest, annotations, nil
	}

	key := pullCacheKey(ref, layout)
	entryDir := filepath.Join(rs.cacheDir, key)
	metaPath := filepath.Join(entryDir, "meta.json")

	if metaBytes, err := afero.ReadFile(rs.fs, metaPath); err == nil {
		var entry cacheEntry
		if err := json.Unmarshal(metaBytes, &entry); err == nil {
			if err := rs.restoreCacheEntry(entryDir, workDir); err == nil {
				logger.Info("Pulled source artifact from cache", "ref", ref.RepositoryReference(), "version", ref.GetReference(), "digest", entry.Digest)
				return entry.MediaType, entry.Digest, entry.Annotations, nil
			}
		}

		logger.Info("Cache entry invalid, re-pulling source artifact", "ref", ref.RepositoryReference(), "version", ref.GetReference())
	}

	mediaType, digest, annotations, err := client.Pull(ctx, ref, workDir)
	if err != nil {
		return "", "", nil, err
	}

	if err := rs.consolidatePulled(workDir, mediaType, layout); err != nil {
		return "", "", nil, err
	}

	rs.populateCache(ctx, entryDir, metaPath, mediaType, digest, annotations, workDir)

	return mediaType, digest, annotations, nil
}

// consolidatePulled merges a pulled raw manifest artifact into a single
// manifest.yaml when the file layout requires it. Artifacts containing
// a kustomization file are always left as separate files.
func (rs *OrderService) consolidatePulled(workDir, mediaType string, layout deliveryv1alpha1.FileLayout) error {
	if mediaType == oci.HelmChartLayerMediaType {
		return nil
	}

	if layout == deliveryv1alpha1.FileLayoutMulti || hasKustomization(rs.fs, workDir) {
		return nil
	}

	if err := mergeYAMLFiles(rs.fs, workDir); err != nil {
		return fmt.Errorf("failed to merge manifest files: %w", err)
	}

	return nil
}

// restoreCacheEntry copies all files of a cache entry into workDir.
func (rs *OrderService) restoreCacheEntry(entryDir, workDir string) error {
	infos, err := afero.ReadDir(rs.fs, entryDir)
	if err != nil {
		return err
	}

	for _, info := range infos {
		if info.IsDir() || info.Name() == "meta.json" {
			continue
		}

		data, err := afero.ReadFile(rs.fs, filepath.Join(entryDir, info.Name()))
		if err != nil {
			return err
		}

		if err := afero.WriteFile(rs.fs, filepath.Join(workDir, info.Name()), data, 0600); err != nil {
			return err
		}
	}

	return nil
}

// populateCache writes the pulled artifact and its metadata to the cache entry
// directory. Errors are non-fatal and only logged as informational messages.
func (rs *OrderService) populateCache(ctx context.Context, entryDir, metaPath, mediaType, digest string, annotations map[string]string, workDir string) {
	logger := log.FromContext(ctx)

	if err := rs.fs.MkdirAll(entryDir, 0700); err != nil {
		logger.Info("Could not create cache entry directory, skipping cache", "error", err)
		return
	}

	infos, err := afero.ReadDir(rs.fs, workDir)
	if err != nil {
		logger.Info("Could not read artifact directory for caching, skipping cache", "error", err)
		return
	}

	for _, info := range infos {
		if info.IsDir() {
			continue
		}

		data, err := afero.ReadFile(rs.fs, filepath.Join(workDir, info.Name()))
		if err != nil {
			logger.Info("Could not read artifact for caching, skipping cache", "error", err)
			return
		}

		if err := afero.WriteFile(rs.fs, filepath.Join(entryDir, info.Name()), data, 0600); err != nil {
			logger.Info("Could not write artifact to cache, skipping cache", "error", err)
			return
		}
	}

	metaBytes, err := json.Marshal(cacheEntry{MediaType: mediaType, Digest: digest, Annotations: annotations})
	if err != nil {
		return
	}

	if err := afero.WriteFile(rs.fs, metaPath, metaBytes, 0600); err != nil {
		logger.Info("Could not write cache metadata, skipping cache", "error", err)
	}
}

// mergeYAMLFiles merges all top-level YAML files in dir into a single
// manifest.yaml. Kustomization files are excluded from the merge.
func mergeYAMLFiles(fs afero.Fs, dir string) error {
	all, err := yamlFiles(fs, dir)
	if err != nil {
		return err
	}

	var files []string
	for _, file := range all {
		switch filepath.Base(file) {
		case "kustomization.yaml", "kustomization.yml", "Kustomization":
			continue
		}
		files = append(files, file)
	}

	if len(files) == 0 {
		return nil
	}

	if len(files) == 1 && filepath.Base(files[0]) == "manifest.yaml" {
		return nil
	}

	var renderedManifest strings.Builder
	for _, file := range files {
		content, err := afero.ReadFile(fs, file)
		if err != nil {
			return fmt.Errorf("read %q: %w", file, err)
		}

		renderedManifest.WriteString("---\n")
		fmt.Fprintf(&renderedManifest, "# Source: %s\n", filepath.Base(file))
		renderedManifest.WriteString(strings.TrimSpace(string(content)))
		renderedManifest.WriteString("\n")
	}

	for _, file := range files {
		if err := fs.Remove(file); err != nil {
			return fmt.Errorf("remove %q: %w", file, err)
		}
	}

	if err := afero.WriteFile(fs, filepath.Join(dir, "manifest.yaml"), []byte(renderedManifest.String()), 0600); err != nil {
		return fmt.Errorf("write manifest.yaml: %w", err)
	}

	return nil
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
