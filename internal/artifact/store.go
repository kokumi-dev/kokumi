package artifact

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/afero"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

// Store provides registry-facing artifact operations: copying artifacts
// between registries (vendor flow), inspecting artifact contents, and
// resolving digests. It owns the shared pull cache and the pull machinery
// used by the Pipeline for its render flows.
type Store struct {
	client oci.Client
	fs     afero.Fs
	cache  *pullCache
}

// pulledArtifact is the outcome of a cached or fresh pull.
type pulledArtifact struct {
	dir         string
	mediaType   string
	digest      string
	annotations map[string]string
}

// layoutRaw is a synthetic cache-key layout for raw (non-consolidating)
// pulls. It must not share cache entries with consolidated layouts.
const layoutRaw = Layout("Raw")

// NewStore returns a Store using the given default OCI client and filesystem.
// cacheDir is the directory used to cache pulled OCI artifacts between runs;
// pass an empty string to disable caching.
func NewStore(client oci.Client, fs afero.Fs, cacheDir string) *Store {
	s := &Store{client: client, fs: fs}
	if cacheDir != "" {
		_ = fs.MkdirAll(cacheDir, 0700)
		s.cache = &pullCache{fs: fs, dir: cacheDir}
	}
	return s
}

// Copy copies the source artifact to the destination registry unchanged
// (vendor flow). The pushed tag is the source version.
func (s *Store) Copy(ctx context.Context, src Source, srcClient oci.Client, dst Destination, dstClient oci.Client) (string, error) {
	source := cmp.Or(srcClient, s.client)
	target := cmp.Or(dstClient, source)

	srcRef, err := parseTagged(src.OCI, src.Version)
	if err != nil {
		return "", fmt.Errorf("failed to parse source ref: %w", err)
	}

	dstRef, err := parseTagged(dst.OCI, src.Version)
	if err != nil {
		return "", fmt.Errorf("failed to parse destination ref: %w", err)
	}

	if err := source.Copy(ctx, target, srcRef, dstRef); err != nil {
		return "", fmt.Errorf("failed to copy artifact: %w", err)
	}

	digest, err := target.Resolve(ctx, dstRef)
	if err != nil {
		digest, err = source.Resolve(ctx, dstRef)
		if err != nil {
			return "", fmt.Errorf("failed to resolve copied digest: %w", err)
		}
	}

	return digest, nil
}

// ResolveDigest resolves the tag of the given source to its current digest.
// The version tag is mandatory. When neither the given client nor the
// store's default client is available, an empty digest is returned. Digest
// resolution is best-effort for advertised sources.
func (s *Store) ResolveDigest(ctx context.Context, src Source, client oci.Client) (string, error) {
	if client == nil && s.client == nil {
		return "", nil //nolint:nilnil
	}

	c := cmp.Or(client, s.client)

	ref, err := parseTagged(src.OCI, src.Version)
	if err != nil {
		return "", fmt.Errorf("failed to parse source ref: %w", err)
	}

	digest, err := c.Resolve(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("failed to resolve digest: %w", err)
	}

	return digest, nil
}

// pull fetches the artifact. Raw layout pulls preserve the artifact's original
// original files; everything else consolidates per the layout.
func (s *Store) pull(ctx context.Context, client oci.Client, ref oci.Reference, layout Layout, tempPrefix string) (*pulledArtifact, error) {
	dir, err := afero.TempDir(s.fs, "", tempPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	if entry := s.cache.get(ctx, ref, layout, dir); entry != nil {
		return &pulledArtifact{dir: dir, mediaType: entry.MediaType, digest: entry.Digest, annotations: entry.Annotations}, nil
	}

	mediaType, digest, annotations, err := client.Pull(ctx, ref, dir)
	if err != nil {
		s.cleanup(dir)
		return nil, err
	}

	if layout != layoutRaw {
		if err := consolidatePulled(s.fs, dir, mediaType, layout); err != nil {
			s.cleanup(dir)
			return nil, err
		}
	}

	s.cache.put(ctx, mediaType, digest, annotations, ref, layout, dir)

	return &pulledArtifact{dir: dir, mediaType: mediaType, digest: digest, annotations: annotations}, nil
}

// cleanup removes a temp directory, ignoring errors.
func (s *Store) cleanup(dir string) {
	_ = s.fs.RemoveAll(dir) //nolint:errcheck
}

// consolidatePulled merges a pulled raw manifest artifact into a single
// manifest.yaml when the layout requires it. Helm charts and artifacts
// containing a kustomization file are always left as separate files.
func consolidatePulled(fs afero.Fs, workDir, mediaType string, layout Layout) error {
	if mediaType == oci.HelmChartLayerMediaType {
		return nil
	}

	if layout == LayoutMulti || hasKustomization(fs, workDir) {
		return nil
	}

	if err := mergeYAMLFiles(fs, workDir); err != nil {
		return fmt.Errorf("failed to merge manifest files: %w", err)
	}

	return nil
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
