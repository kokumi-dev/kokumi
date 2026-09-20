package artifact_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/oci"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	testSourceOCI      = "oci://registry.svc.cluster.local:5000/order/app"
	testDestOCI        = "oci://registry.svc.cluster.local:5000/preparation/app"
	testChartsOCI      = "oci://registry.example.com/charts/app"
	testChartsAppOCI   = "oci://registry.svc.cluster.local:5000/charts/app"
	testVendorOCI      = "oci://registry.svc.cluster.local:5000/vendor/app"
	fakeDigest         = "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f"
	testVersion        = "1.0.0"
	testDeploymentFile = "deployment.yaml"
	testServiceFile    = "service.yaml"
	testDeploymentYAML = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: my-app\nspec:\n  replicas: 1\n"
	testServiceYAML    = "apiVersion: v1\nkind: Service\nmetadata:\n  name: my-app\nspec:\n  port: 80\n"

	testNamespaceName = "default"
	testReleaseName   = "my-app"
	testAppName       = "app"
	kindDeployment    = "Deployment"
	replicasPatchPath = ".spec.replicas"
	multiFileOCI      = "oci://registry.svc.cluster.local:5000/order/multi-file-app"
	multiFileName     = "multi-file-app"
)

func baseRenderRequest() artifact.RenderRequest {
	return artifact.RenderRequest{
		Source: artifact.Source{
			OCI:     testSourceOCI,
			Version: testVersion,
		},
		Destination: artifact.Destination{
			OCI: testDestOCI,
		},
		Name:      testAppName,
		Namespace: testNamespaceName,
	}
}

func TestPipeline_Render(t *testing.T) {
	t.Run("no patches pushes normalized manifest", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		p := artifact.NewPipeline(artifact.NewStore(oci.NewFakeClient(fs), fs, ""))

		result, err := p.Render(context.Background(), baseRenderRequest())
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.Equal(t, "registry.svc.cluster.local:5000/order/app", result.SourceRef.RepositoryReference())
		assert.Equal(t, "registry.svc.cluster.local:5000/preparation/app", result.DestRef.RepositoryReference())
		assert.Equal(t, fakeDigest, result.SourceRef.Digest)
		assert.Regexp(t, `^sha256:[a-f0-9]{64}$`, result.DestRef.Digest)
	})

	t.Run("helm render rejected when source is not a helm chart", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		p := artifact.NewPipeline(artifact.NewStore(oci.NewFakeClient(fs), fs, ""))

		req := baseRenderRequest()
		req.Render = &artifact.RenderSpec{
			Helm: &artifact.HelmSpec{ReleaseName: testReleaseName, Namespace: testNamespaceName},
		}

		_, err := p.Render(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "source is not a Helm chart")
	})

	t.Run("multiple yaml files merged into single manifest", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &capturingFakeClient{
			fs: fs,
			// Base artifact carries the source files the merge consumes.
			annotations: nil,
		}
		p := artifact.NewPipeline(artifact.NewStore(&multiFileCapture{capturingFakeClient: client}, fs, ""))

		result, err := p.Render(context.Background(), baseRenderRequest())
		require.NoError(t, err)
		assert.Equal(t, fakeDigest, result.SourceRef.Digest)

		// The push must see exactly one merged file containing both docs.
		require.Len(t, client.lastPushedFiles, 1, "multi-file source must merge into a single manifest.yaml")
		manifest, ok := client.lastPushedFiles["manifest.yaml"]
		require.True(t, ok)
		assert.Contains(t, manifest, "kind: Deployment")
		assert.Contains(t, manifest, "kind: Service")
	})
}

// multiFileCapture pulls a multi-file artifact (via multiFileFakeClient) and
// captures pushes (via capturingFakeClient), combining both fakes' behaviors.
type multiFileCapture struct {
	*capturingFakeClient
}

var _ oci.Client = (*multiFileCapture)(nil)

func (c *multiFileCapture) Pull(ctx context.Context, ref oci.Reference, targetDir string) (string, string, map[string]string, error) {
	files := map[string]string{
		testDeploymentFile: testDeploymentYAML,
		testServiceFile:    testServiceYAML,
	}
	for name, content := range files {
		if err := c.fs.MkdirAll(targetDir, 0700); err != nil {
			return "", "", nil, err
		}
		if err := afero.WriteFile(c.fs, filepath.Join(targetDir, name), []byte(content), 0600); err != nil {
			return "", "", nil, err
		}
	}
	return "", fakeDigest, nil, nil
}

func TestPipeline_RenderProvenance(t *testing.T) {
	baseAnnotations := map[string]string{
		ocispec.AnnotationSource:   "https://github.com/kokumi-dev/example",
		ocispec.AnnotationVersion:  "1.2.3",
		ocispec.AnnotationRevision: "abcdef1234567890abcdef1234567890abcdef12",
	}

	t.Run("extracts and copies provenance forward", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &capturingFakeClient{fs: fs, annotations: baseAnnotations}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		result, err := p.Render(context.Background(), baseRenderRequest())
		require.NoError(t, err)

		assert.Equal(t, "https://github.com/kokumi-dev/example", result.SCM.Repo)
		assert.Equal(t, "1.2.3", result.SCM.Tag)
		assert.Equal(t, "abcdef1234567890abcdef1234567890abcdef12", result.SCM.CommitHash)

		require.NotNil(t, client.lastPushAnnotations)
		assert.Equal(t, "https://github.com/kokumi-dev/example", client.lastPushAnnotations[ocispec.AnnotationSource])
		assert.Equal(t, "1.2.3", client.lastPushAnnotations[ocispec.AnnotationVersion])
		assert.Equal(t, "abcdef1234567890abcdef1234567890abcdef12", client.lastPushAnnotations[ocispec.AnnotationRevision])
		assert.Equal(t, "registry.svc.cluster.local:5000/order/app", client.lastPushAnnotations[ocispec.AnnotationBaseImageName])
		assert.Equal(t, fakeDigest, client.lastPushAnnotations[ocispec.AnnotationBaseImageDigest])
	})

	t.Run("omits provenance when base has none", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &capturingFakeClient{fs: fs, annotations: nil}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		result, err := p.Render(context.Background(), baseRenderRequest())
		require.NoError(t, err)

		assert.Empty(t, result.SCM.Repo)
		assert.Empty(t, result.SCM.Tag)
		assert.Empty(t, result.SCM.CommitHash)
		require.NotNil(t, client.lastPushAnnotations)
		_, hasSource := client.lastPushAnnotations[ocispec.AnnotationSource]
		assert.False(t, hasSource, "source annotation should not be set when base has none")
	})

	t.Run("parent digest and description stamped", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &capturingFakeClient{fs: fs, annotations: nil}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		req := baseRenderRequest()
		req.Description = "Initial commit"
		req.ParentDigest = "sha256:previous"

		_, err := p.Render(context.Background(), req)
		require.NoError(t, err)

		assert.Equal(t, "Initial commit", client.lastPushAnnotations[ocispec.AnnotationDescription])
		assert.Equal(t, "sha256:previous", client.lastPushAnnotations[oci.AnnotationParentDigest])
	})
}

// TestPipeline_Render_PatchesThenEdits pins the application order: patches
// first, edits second — edits must win, so consumer edits override Menu
// patches on the same field.
func TestPipeline_Render_PatchesThenEdits(t *testing.T) {
	fs := afero.NewMemMapFs()
	client := &capturingFakeClient{
		fs: fs,
	}
	p := artifact.NewPipeline(artifact.NewStore(&multiFileCapture{capturingFakeClient: client}, fs, ""))

	req := baseRenderRequest()
	req.Patches = []artifact.PatchSpec{
		{Target: artifact.PatchTargetSpec{Kind: kindDeployment, Name: testReleaseName}, Set: map[string]string{replicasPatchPath: "3"}},
	}
	req.Edits = []artifact.PatchSpec{
		{Target: artifact.PatchTargetSpec{Kind: kindDeployment, Name: testReleaseName}, Set: map[string]string{replicasPatchPath: "9"}},
	}

	_, err := p.Render(context.Background(), req)
	require.NoError(t, err)

	require.Contains(t, client.lastPushedFiles, "manifest.yaml")
	assert.Contains(t, client.lastPushedFiles["manifest.yaml"], "replicas: 9",
		"edits are applied after patches and must win on the same field")
}

func TestPipeline_PullCache(t *testing.T) {
	const cacheDir = "/cache"

	t.Run("cache miss populates cache", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		pullCount := 0
		client := &countingFakeClient{fs: fs, onPull: func() { pullCount++ }}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, cacheDir))

		_, err := p.Render(context.Background(), baseRenderRequest())
		require.NoError(t, err)
		assert.Equal(t, 1, pullCount, "expected one pull on cache miss")

		exists, err := afero.Exists(fs, filepath.Join(cacheDir, entryKey(t, fs, cacheDir), "meta.json"))
		require.NoError(t, err)
		assert.True(t, exists, "meta.json should be written to cache")
	})

	t.Run("cache hit skips pull", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		pullCount := 0
		client := &countingFakeClient{fs: fs, onPull: func() { pullCount++ }}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, cacheDir))

		_, err := p.Render(context.Background(), baseRenderRequest())
		require.NoError(t, err)
		require.Equal(t, 1, pullCount)

		_, err = p.Render(context.Background(), baseRenderRequest())
		require.NoError(t, err)
		assert.Equal(t, 1, pullCount, "second call should be served from cache without pulling")
	})
}

func TestPipeline_CacheSplitByLayout(t *testing.T) {
	const cacheDir = "/cache"

	fs := afero.NewMemMapFs()
	pullCount := 0
	client := &countingFakeClient{fs: fs, onPull: func() { pullCount++ }}
	p := artifact.NewPipeline(artifact.NewStore(client, fs, cacheDir))

	single := baseRenderRequest()

	separate := baseRenderRequest()
	separate.Render = &artifact.RenderSpec{
		Manifest: &artifact.ManifestSpec{Layout: artifact.LayoutMulti},
	}

	_, err := p.Render(context.Background(), single)
	require.NoError(t, err)
	assert.Equal(t, 1, pullCount)

	// Different file layout must not reuse the cached merged layout.
	_, err = p.Render(context.Background(), separate)
	require.NoError(t, err)
	assert.Equal(t, 2, pullCount, "separate file layout must not share cache entry")

	// Same policy again hits the cache.
	_, err = p.Render(context.Background(), single)
	require.NoError(t, err)
	assert.Equal(t, 2, pullCount)
}

// entryKey finds the single cache entry directory below cacheDir.
func entryKey(t *testing.T, fs afero.Fs, cacheDir string) string {
	t.Helper()

	entries, err := afero.ReadDir(fs, cacheDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "expected exactly one cache entry")

	return entries[0].Name()
}

func TestPipeline_FileLayoutMulti(t *testing.T) {
	t.Run("files kept separate and patched individually", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &multiFileFakeClient{fs: fs}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		req := artifact.PreviewRequest{
			Source: artifact.Source{
				OCI:     multiFileOCI,
				Version: testVersion,
			},
			Name:      multiFileName,
			Namespace: testNamespaceName,
			Render: &artifact.RenderSpec{
				Manifest: &artifact.ManifestSpec{Layout: artifact.LayoutMulti},
			},
			Patches: []artifact.PatchSpec{
				{
					Target: artifact.PatchTargetSpec{Kind: "Deployment", Name: "my-app"},
					Set:    map[string]string{replicasPatchPath: "3"},
				},
			},
		}

		preview, err := p.PreviewManifest(context.Background(), req)
		require.NoError(t, err)
		assert.Contains(t, string(preview), "# Source: deployment.yaml")
		assert.Contains(t, string(preview), "# Source: service.yaml")
		assert.Contains(t, string(preview), "replicas: 3")
	})

	t.Run("kustomization.yaml prevents merge even with Single layout", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &kustomizeFakeClient{fs: fs}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		req := artifact.PreviewRequest{
			Source: artifact.Source{
				OCI:     multiFileOCI,
				Version: testVersion,
			},
			Name:      multiFileName,
			Namespace: testNamespaceName,
		}

		preview, err := p.PreviewManifest(context.Background(), req)
		require.NoError(t, err)
		assert.Contains(t, string(preview), "# Source: deployment.yaml")
		assert.Contains(t, string(preview), "# Source: kustomization.yaml")
	})

	t.Run("default still merges into manifest.yaml", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &multiFileFakeClient{fs: fs}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		req := artifact.PreviewRequest{
			Source: artifact.Source{
				OCI:     multiFileOCI,
				Version: testVersion,
			},
			Name:      multiFileName,
			Namespace: testNamespaceName,
		}

		preview, err := p.PreviewManifest(context.Background(), req)
		require.NoError(t, err)
		assert.Contains(t, string(preview), "# Source: deployment.yaml")
		assert.Contains(t, string(preview), "# Source: service.yaml")
	})
}

func TestPipeline_FluxKustomizeArtifact(t *testing.T) {
	fs := afero.NewMemMapFs()
	client := &fluxFakeClient{fs: fs}
	p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

	req := artifact.PreviewRequest{
		Source: artifact.Source{
			OCI:     multiFileOCI,
			Version: testVersion,
		},
		Name:      "podinfo-kustomize",
		Namespace: testNamespaceName,
		Patches: []artifact.PatchSpec{
			{
				Target: artifact.PatchTargetSpec{Kind: "Deployment", Name: "podinfo"},
				Set:    map[string]string{replicasPatchPath: "2"},
			},
		},
	}

	preview, err := p.PreviewManifest(context.Background(), req)
	require.NoError(t, err)

	assert.Contains(t, string(preview), "# Source: deployment.yaml")
	assert.Contains(t, string(preview), "# Source: service.yaml")
	assert.Contains(t, string(preview), "# Source: kustomization.yaml")
	assert.Contains(t, string(preview), "replicas: 2")
}

func TestStore_ResolveDigest(t *testing.T) {
	fs := afero.NewMemMapFs()
	store := artifact.NewStore(oci.NewFakeClient(fs), fs, "")

	digest, err := store.ResolveDigest(context.Background(), artifact.Source{
		OCI:     testSourceOCI,
		Version: testVersion,
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, fakeDigest, digest)
}

func TestStore_Copy(t *testing.T) {
	fs := afero.NewMemMapFs()
	client := &copyFakeClient{fs: fs}
	store := artifact.NewStore(client, fs, "")

	digest, err := store.Copy(context.Background(),
		artifact.Source{OCI: testChartsOCI, Version: testVersion}, nil,
		artifact.Destination{OCI: testVendorOCI}, nil,
	)
	require.NoError(t, err)
	assert.Equal(t, fakeDigest, digest)
	assert.Equal(t, "registry.example.com/charts/app", client.copiedSrc.RepositoryReference())
	assert.Equal(t, "registry.svc.cluster.local:5000/vendor/app", client.copiedDst.RepositoryReference())
	assert.Equal(t, testVersion, client.copiedDst.Tag)
}

// Fake OCI clients used by the pipeline tests. They embed oci.FakeClient for
// defaults and override Pull/Push/Copy to simulate specific artifact shapes.

// listPushedFiles reads all regular files under sourceDir (the directory a
// real client would push) into a name→content map.
func listPushedFiles(fs afero.Fs, sourceDir string) (map[string]string, error) {
	infos, err := afero.ReadDir(fs, sourceDir)
	if err != nil {
		return nil, err
	}

	files := make(map[string]string, len(infos))
	for _, info := range infos {
		if info.IsDir() {
			continue
		}
		data, err := afero.ReadFile(fs, filepath.Join(sourceDir, info.Name()))
		if err != nil {
			return nil, err
		}
		files[info.Name()] = string(data)
	}

	return files, nil
}

type capturingFakeClient struct {
	fs                  afero.Fs
	annotations         map[string]string
	lastPushAnnotations map[string]string
	lastPushedFiles     map[string]string
}

var _ oci.Client = (*capturingFakeClient)(nil)

func (c *capturingFakeClient) Pull(ctx context.Context, ref oci.Reference, targetDir string) (string, string, map[string]string, error) {
	_, _, _, err := oci.NewFakeClient(c.fs).Pull(ctx, ref, targetDir)
	if err != nil {
		return "", "", nil, err
	}
	return "", fakeDigest, c.annotations, nil
}

func (c *capturingFakeClient) Push(ctx context.Context, ref oci.Reference, sourceDir string, annotations map[string]string) (string, error) {
	c.lastPushAnnotations = annotations

	files, err := listPushedFiles(c.fs, sourceDir)
	if err != nil {
		return "", err
	}
	c.lastPushedFiles = files

	return oci.NewFakeClient(c.fs).Push(ctx, ref, sourceDir, annotations)
}

func (c *capturingFakeClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func (c *capturingFakeClient) Resolve(_ context.Context, _ oci.Reference) (string, error) {
	return fakeDigest, nil
}

func (c *capturingFakeClient) Copy(_ context.Context, _ oci.Client, _, _ oci.Reference) error {
	return nil
}

type multiFileFakeClient struct {
	fs afero.Fs
}

var _ oci.Client = (*multiFileFakeClient)(nil)

func (c *multiFileFakeClient) Pull(_ context.Context, _ oci.Reference, targetDir string) (string, string, map[string]string, error) {
	files := map[string]string{
		testDeploymentFile: testDeploymentYAML,
		testServiceFile:    testServiceYAML,
	}
	for name, content := range files {
		if err := afero.WriteFile(c.fs, filepath.Join(targetDir, name), []byte(content), 0600); err != nil {
			return "", "", nil, err
		}
	}
	return "", fakeDigest, nil, nil
}

func (c *multiFileFakeClient) Push(_ context.Context, _ oci.Reference, _ string, _ map[string]string) (string, error) {
	return fakeDigest, nil
}

func (c *multiFileFakeClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func (c *multiFileFakeClient) Resolve(_ context.Context, _ oci.Reference) (string, error) {
	return fakeDigest, nil
}

func (c *multiFileFakeClient) Copy(_ context.Context, _ oci.Client, _, _ oci.Reference) error {
	return nil
}

type kustomizeFakeClient struct {
	fs afero.Fs
}

var _ oci.Client = (*kustomizeFakeClient)(nil)

func (c *kustomizeFakeClient) Pull(_ context.Context, _ oci.Reference, targetDir string) (string, string, map[string]string, error) {
	files := map[string]string{
		"kustomization.yaml": "resources:\n- deployment.yaml\n",
		"deployment.yaml":    "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: my-app\n",
	}
	for name, content := range files {
		if err := afero.WriteFile(c.fs, filepath.Join(targetDir, name), []byte(content), 0600); err != nil {
			return "", "", nil, err
		}
	}
	return "", fakeDigest, nil, nil
}

func (c *kustomizeFakeClient) Push(_ context.Context, _ oci.Reference, _ string, _ map[string]string) (string, error) {
	return fakeDigest, nil
}

func (c *kustomizeFakeClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func (c *kustomizeFakeClient) Resolve(_ context.Context, _ oci.Reference) (string, error) {
	return fakeDigest, nil
}

func (c *kustomizeFakeClient) Copy(_ context.Context, _ oci.Client, _, _ oci.Reference) error {
	return nil
}

type fluxFakeClient struct {
	fs afero.Fs
}

var _ oci.Client = (*fluxFakeClient)(nil)

func (c *fluxFakeClient) Pull(_ context.Context, _ oci.Reference, targetDir string) (string, string, map[string]string, error) {
	files := map[string]string{
		"kustomization.yaml": "resources:\n- deployment.yaml\n- service.yaml\n",
		"deployment.yaml":    "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: podinfo\n",
		"service.yaml":       "apiVersion: v1\nkind: Service\nmetadata:\n  name: podinfo\n",
	}
	for name, content := range files {
		if err := afero.WriteFile(c.fs, filepath.Join(targetDir, name), []byte(content), 0600); err != nil {
			return "", "", nil, err
		}
	}
	return "", fakeDigest, nil, nil
}

func (c *fluxFakeClient) Push(_ context.Context, _ oci.Reference, _ string, _ map[string]string) (string, error) {
	return fakeDigest, nil
}

func (c *fluxFakeClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func (c *fluxFakeClient) Resolve(_ context.Context, _ oci.Reference) (string, error) {
	return fakeDigest, nil
}

func (c *fluxFakeClient) Copy(_ context.Context, _ oci.Client, _, _ oci.Reference) error {
	return nil
}

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

type copyFakeClient struct {
	fs        afero.Fs
	copiedSrc oci.Reference
	copiedDst oci.Reference
}

var _ oci.Client = (*copyFakeClient)(nil)

func (c *copyFakeClient) Pull(_ context.Context, _ oci.Reference, _ string) (string, string, map[string]string, error) {
	return "", fakeDigest, nil, nil
}

func (c *copyFakeClient) Push(_ context.Context, _ oci.Reference, _ string, _ map[string]string) (string, error) {
	return fakeDigest, nil
}

func (c *copyFakeClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func (c *copyFakeClient) Resolve(_ context.Context, _ oci.Reference) (string, error) {
	return fakeDigest, nil
}

func (c *copyFakeClient) Copy(_ context.Context, _ oci.Client, src, dst oci.Reference) error {
	c.copiedSrc = src
	c.copiedDst = dst
	return nil
}
