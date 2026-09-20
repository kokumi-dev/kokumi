package artifact

import (
	"context"
	"encoding/json"
	"path/filepath"
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
	fs     afero.Fs
	onPull func()
}

var _ oci.Client = (*countingFakeClient)(nil)

func (c *countingFakeClient) Pull(ctx context.Context, ref oci.Reference, targetDir string) (string, string, map[string]string, error) {
	c.onPull()
	return oci.NewFakeClient(c.fs).Pull(ctx, ref, targetDir)
}

func (c *countingFakeClient) Push(ctx context.Context, ref oci.Reference, sourceDir string, annotations map[string]string) (string, error) {
	return oci.NewFakeClient(c.fs).Push(ctx, ref, sourceDir, annotations)
}

func (c *countingFakeClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func (c *countingFakeClient) Resolve(_ context.Context, _ oci.Reference) (string, error) {
	return fakeDigest, nil
}

func (c *countingFakeClient) Copy(_ context.Context, _ oci.Client, _, _ oci.Reference) error {
	return nil
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

func TestPullCache_CorruptMetaTriggersRepull(t *testing.T) {
	fs := afero.NewMemMapFs()
	ref := parseRef(t)

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
	ref := parseRef(t)

	require.Equal(t, 1, pullInto(t, fs, "/cache", LayoutSingle))

	// Delete the cached artifact file, leaving only meta.json: the restore
	// then fails and the store must re-pull.
	key := pullCacheKey(ref, LayoutSingle)
	entryDir := filepath.Join("/cache", key)
	entries, err := afero.ReadDir(fs, entryDir)
	require.NoError(t, err)
	for _, info := range entries {
		if !info.IsDir() && info.Name() != "meta.json" {
			require.NoError(t, fs.Remove(filepath.Join(entryDir, info.Name())))
		}
	}

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
	ref := parseRef(t)

	// First pull populates the cache normally.
	require.Equal(t, 1, pullInto(t, fs, "/cache", LayoutSingle))

	// A file where the cache entry directory must go forces the cache write
	// to fail on the next pull.
	key := pullCacheKey(ref, LayoutSingle)
	require.NoError(t, fs.Remove(filepath.Join("/cache", key)))
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
	ref := parseRef(t)

	require.Equal(t, 1, pullInto(t, fs, "/cache", LayoutSingle))

	metaBytes, err := afero.ReadFile(fs, filepath.Join("/cache", pullCacheKey(ref, LayoutSingle), "meta.json"))
	require.NoError(t, err)

	var entry cacheEntry
	require.NoError(t, json.Unmarshal(metaBytes, &entry))
	assert.Equal(t, fakeDigest, entry.Digest)
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
