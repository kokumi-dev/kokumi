package artifact

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

const (
	yamlKindA          = "kind: A\n"
	fileA              = "a.yaml"
	cacheTestRef       = "oci://registry.svc.cluster.local:5000/order/app"
	testVersion        = "1.0.0"
	fakeDigest         = "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f"
	testDeploymentFile = "deployment.yaml"
	testServiceFile    = "service.yaml"
)

// countingFakeClient wraps oci.FakeClient and invokes onPull on every Pull
// call, letting tests count registry round-trips.
type countingFakeClient struct {
	oci.FakeClient
	fs     afero.Fs
	onPull func()
}

var _ oci.Client = (*countingFakeClient)(nil)

func (c *countingFakeClient) Pull(ctx context.Context, ref oci.Reference, targetDir string) (string, string, map[string]string, error) {
	c.onPull()
	return oci.NewFakeClient(c.fs).Pull(ctx, ref, targetDir)
}

func (c *countingFakeClient) Push(ctx context.Context, ref oci.Reference, sourceDir string, opts oci.PushOptions) (string, error) {
	return oci.NewFakeClient(c.fs).Push(ctx, ref, sourceDir, opts)
}

func (c *countingFakeClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func (c *countingFakeClient) Resolve(_ context.Context, _ oci.Reference) (oci.Descriptor, error) {
	return oci.Descriptor{Digest: fakeDigest}, nil
}

func (c *countingFakeClient) Copy(_ context.Context, _ oci.Client, _, _ oci.Reference, _ oci.CopyOptions) error {
	return nil
}

func (c *countingFakeClient) PushReferrer(_ context.Context, _ oci.Reference, _ oci.ReferrerArtifact) (string, error) {
	return fakeDigest, nil
}

// pullInto runs a single pull via a fresh store and returns the number of
// registry Pull calls that single operation made (0 = served from cache).
func pullInto(t *testing.T, fs afero.Fs, cacheDir string, layout Layout) int {
	t.Helper()

	pullCount := 0
	client := &countingFakeClient{fs: fs, onPull: func() { pullCount++ }}
	store := NewStore(client, fs, cacheDir)

	ref := parseRef(t)
	pulled, err := store.pull(context.Background(), client, ref, layout, "cache-test-*")
	require.NoError(t, err)
	require.NotNil(t, pulled)

	return pullCount
}

func parseRef(t *testing.T) oci.Reference {
	t.Helper()

	ref, err := oci.Parse(cacheTestRef)
	require.NoError(t, err)
	ref.Tag = testVersion
	return ref
}

// pinnedRef is parseRef resolved to its digest, as the cache keys it.
func pinnedRef(t *testing.T) oci.Reference {
	t.Helper()

	ref := parseRef(t)
	ref.Tag, ref.Digest = "", fakeDigest
	return ref
}

func TestPullCache_CorruptMetaTriggersRepull(t *testing.T) {
	fs := afero.NewMemMapFs()
	ref := pinnedRef(t)

	// First pull populates the cache.
	require.Equal(t, 1, pullInto(t, fs, "/cache", LayoutSingle))

	// Corrupt the cache metadata.
	key := pullCacheKey(ref, LayoutSingle)
	metaPath := filepath.Join("/cache", key, "meta.json")
	require.NoError(t, afero.WriteFile(fs, metaPath, []byte("{not json"), 0600))

	// Second pull must not trust the corrupt entry and re-pull from the registry.
	require.Equal(t, 1, pullInto(t, fs, "/cache", LayoutSingle), "corrupt meta.json must trigger a re-pull")
}

func TestPullCache_PartialRestoreTriggersRepull(t *testing.T) {
	fs := afero.NewMemMapFs()
	ref := pinnedRef(t)

	require.Equal(t, 1, pullInto(t, fs, "/cache", LayoutSingle))

	// Delete the cached artifact files, leaving only meta.json: the restore
	// then fails and the store must re-pull.
	key := pullCacheKey(ref, LayoutSingle)
	require.NoError(t, fs.RemoveAll(filepath.Join("/cache", key, filesDir)))

	// With no artifact files left, restore must fail and force a re-pull.
	require.Equal(t, 1, pullInto(t, fs, "/cache", LayoutSingle), "partial cache entry must trigger a re-pull")
}

func TestPullCache_Disabled(t *testing.T) {
	fs := afero.NewMemMapFs()

	// With caching disabled every pull goes to the registry (count 1 per
	// call, never 0) and no cache directory is ever used.
	require.Equal(t, 1, pullInto(t, fs, "", LayoutSingle))
	require.Equal(t, 1, pullInto(t, fs, "", LayoutMulti))

	exists, err := afero.Exists(fs, "/cache")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestPullCache_NewStoreCreatesCacheDir(t *testing.T) {
	fs := afero.NewMemMapFs()
	NewStore(oci.NewFakeClient(fs), fs, "/tmpcache")

	exists, err := afero.Exists(fs, "/tmpcache")
	require.NoError(t, err)
	assert.True(t, exists, "NewStore should create the cache directory")
}

func TestPullCache_WriteFailureIsNonFatal(t *testing.T) {
	fs := afero.NewMemMapFs()
	ref := pinnedRef(t)

	// First pull populates the cache normally.
	require.Equal(t, 1, pullInto(t, fs, "/cache", LayoutSingle))

	// A file where the cache entry directory must go forces the cache write
	// to fail on the next pull.
	key := pullCacheKey(ref, LayoutSingle)
	require.NoError(t, fs.RemoveAll(filepath.Join("/cache", key)))
	require.NoError(t, afero.WriteFile(fs, filepath.Join("/cache", key), []byte("i am a file"), 0600))

	// The pull must succeed regardless: cache write failures are logged,
	// never propagated.
	pulledDir, err := pullDir(t, fs, "/cache", LayoutSingle)
	require.NoError(t, err)
	assert.NotEmpty(t, pulledDir)
}

// pullDir is pullInto's twin returning the pulled directory instead of a pull
// count, for tests asserting success rather than call counts.
func pullDir(t *testing.T, fs afero.Fs, cacheDir string, layout Layout) (string, error) {
	t.Helper()

	client := &countingFakeClient{fs: fs, onPull: func() {}}
	store := NewStore(client, fs, cacheDir)

	pulled, err := store.pull(context.Background(), client, parseRef(t), layout, "cache-test-*")
	if err != nil {
		return "", err
	}

	return pulled.dir, nil
}

func TestPullCache_EntryContent(t *testing.T) {
	fs := afero.NewMemMapFs()
	ref := pinnedRef(t)

	require.Equal(t, 1, pullInto(t, fs, "/cache", LayoutSingle))

	metaBytes, err := afero.ReadFile(fs, filepath.Join("/cache", pullCacheKey(ref, LayoutSingle), "meta.json"))
	require.NoError(t, err)

	var entry cacheEntry
	require.NoError(t, json.Unmarshal(metaBytes, &entry))
	assert.Equal(t, fakeDigest, entry.Digest)
}

// nestedClient pulls an artifact with a file in a subdirectory, as a
// kustomize base is.
type nestedClient struct {
	countingFakeClient
}

func (c *nestedClient) Pull(_ context.Context, _ oci.Reference, targetDir string) (string, string, map[string]string, error) {
	c.onPull()
	if err := afero.WriteFile(c.fs, filepath.Join(targetDir, "kustomization.yaml"), []byte("resources: [base]\n"), 0600); err != nil {
		return "", "", nil, err
	}
	if err := afero.WriteFile(c.fs, filepath.Join(targetDir, "base", fileA), []byte(yamlKindA), 0600); err != nil {
		return "", "", nil, err
	}
	return "", fakeDigest, nil, nil
}

func TestPullCache_KeepsNestedFiles(t *testing.T) {
	fs := afero.NewMemMapFs()
	pulls := 0
	client := &nestedClient{countingFakeClient{fs: fs, onPull: func() { pulls++ }}}
	store := NewStore(client, fs, "/cache")

	_, err := store.pull(context.Background(), client, parseRef(t), LayoutSingle, "cache-test-*")
	require.NoError(t, err)
	cached, err := store.pull(context.Background(), client, parseRef(t), LayoutSingle, "cache-test-*")
	require.NoError(t, err)

	require.Equal(t, 1, pulls, "the second pull is served from the cache")
	data, err := afero.ReadFile(fs, filepath.Join(cached.dir, "base", fileA))
	require.NoError(t, err, "a cache hit restores files in subdirectories")
	assert.Equal(t, yamlKindA, string(data))
}

// deniedClient resolves nothing, as a client without the credentials an
// artifact needs.
type deniedClient struct {
	countingFakeClient
}

func (c *deniedClient) Resolve(_ context.Context, ref oci.Reference) (oci.Descriptor, error) {
	return oci.Descriptor{}, fmt.Errorf("resolve %s: unauthorized", ref)
}

func TestPullCache_NeedsTheCallersCredentials(t *testing.T) {
	fs := afero.NewMemMapFs()
	pulls := 0
	authenticated := &countingFakeClient{fs: fs, onPull: func() { pulls++ }}
	anonymous := &deniedClient{countingFakeClient{fs: fs, onPull: func() { pulls++ }}}
	store := NewStore(authenticated, fs, "/cache")

	_, err := store.pull(context.Background(), authenticated, parseRef(t), LayoutSingle, "cache-test-*")
	require.NoError(t, err)

	_, err = store.pull(context.Background(), anonymous, parseRef(t), LayoutSingle, "cache-test-*")
	require.ErrorContains(t, err, "unauthorized", "a cached artifact is not served to a caller that cannot resolve it")
	require.Equal(t, 1, pulls)
}

func TestConsolidatePulled(t *testing.T) {
	write := func(fs afero.Fs, dir string, files map[string]string) {
		t.Helper()
		for name, content := range files {
			require.NoError(t, afero.WriteFile(fs, filepath.Join(dir, name), []byte(content), 0600))
		}
	}

	t.Run("helm media type skips merge", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		write(fs, "/dir", map[string]string{fileA: yamlKindA, "chart.tgz": "blob"})

		require.NoError(t, consolidatePulled(fs, "/dir", oci.HelmChartLayerMediaType, LayoutSingle))

		exists, _ := afero.Exists(fs, "/dir/manifest.yaml")
		assert.False(t, exists, "helm artifacts are never merged")
	})

	t.Run("Multi layout keeps files", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		write(fs, "/dir", map[string]string{fileA: yamlKindA, "b.yaml": "kind: B\n"})

		require.NoError(t, consolidatePulled(fs, "/dir", "", LayoutMulti))

		exists, _ := afero.Exists(fs, "/dir/manifest.yaml")
		assert.False(t, exists)
	})

	t.Run("kustomization prevents merge", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		write(fs, "/dir", map[string]string{
			"kustomization.yaml": "resources:\n- a.yaml\n",
			fileA:                yamlKindA,
		})

		require.NoError(t, consolidatePulled(fs, "/dir", "", LayoutSingle))

		data, err := afero.ReadFile(fs, "/dir/kustomization.yaml")
		require.NoError(t, err)
		assert.Equal(t, "resources:\n- a.yaml\n", string(data), "kustomization must survive")
	})

	t.Run("Single layout merges into manifest.yaml", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		write(fs, "/dir", map[string]string{"b.yaml": "kind: B\n", fileA: yamlKindA})

		require.NoError(t, consolidatePulled(fs, "/dir", "", LayoutSingle))

		data, err := afero.ReadFile(fs, "/dir/manifest.yaml")
		require.NoError(t, err)
		assert.Contains(t, string(data), "# Source: a.yaml")
		assert.Contains(t, string(data), "# Source: b.yaml")

		gone, _ := afero.Exists(fs, "/dir/a.yaml")
		assert.False(t, gone, "source files are removed after merge")
	})
}

func TestPullCacheKey_SplitsByLayout(t *testing.T) {
	ref := oci.Reference{}

	assert.NotEqual(t,
		pullCacheKey(ref, LayoutSingle),
		pullCacheKey(ref, LayoutMulti),
		"different layouts must not share a cache entry")
	assert.NotEqual(t,
		pullCacheKey(ref, LayoutSingle),
		pullCacheKey(ref, layoutRaw),
		"raw pulls must not share cache entries with consolidated layouts")
}

func TestPullCache_ConcurrentPullsRestoreCompleteEntries(t *testing.T) {
	fs := afero.NewOsFs()
	cacheDir := t.TempDir()
	content := strings.Repeat("kind: ConfigMap\n", 4096)

	client := &contentFakeClient{fs: fs, content: []byte(content)}
	store := NewStore(client, fs, cacheDir)

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			pulled, err := store.pull(context.Background(), client, parseRef(t), LayoutMulti, "cache-test-*")
			if err != nil {
				errs <- err
				return
			}
			defer store.cleanup(pulled.dir)
			data, err := afero.ReadFile(fs, filepath.Join(pulled.dir, "manifest.yaml"))
			if err != nil {
				errs <- err
				return
			}
			if string(data) != content {
				errs <- fmt.Errorf("restored %d of %d bytes", len(data), len(content))
			}
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(cacheDir, ".tmp-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers, "temporary entries are removed")
}

// contentFakeClient pulls a manifest.yaml with the given content.
type contentFakeClient struct {
	countingFakeClient
	fs      afero.Fs
	content []byte
}

func (c *contentFakeClient) Pull(_ context.Context, _ oci.Reference, targetDir string) (string, string, map[string]string, error) {
	if err := afero.WriteFile(c.fs, filepath.Join(targetDir, "manifest.yaml"), c.content, 0600); err != nil {
		return "", "", nil, err
	}
	return "", fakeDigest, nil, nil
}
