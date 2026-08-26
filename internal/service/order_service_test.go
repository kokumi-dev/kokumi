package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	fakeDigest         = "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f"
	testVersion        = "1.0.0"
	testDeploymentFile = "deployment.yaml"
	testServiceFile    = "service.yaml"
	testDeploymentYAML = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: my-app\nspec:\n  replicas: 1\n"
	testServiceYAML    = "apiVersion: v1\nkind: Service\nmetadata:\n  name: my-app\nspec:\n  port: 80\n"
)

func TestOrderService_ProcessOrder(t *testing.T) {
	tests := []struct {
		name          string
		makeClient    func(fs afero.Fs) oci.Client
		order         *deliveryv1alpha1.Order
		wantSourceRef string
		wantDestRef   string
		wantSourceDig string
		wantErr       bool
		wantErrMsg    string
	}{
		{
			name: "no patches",
			order: &deliveryv1alpha1.Order{
				Spec: deliveryv1alpha1.OrderSpec{
					Source: &deliveryv1alpha1.OCISource{
						OCI:     "oci://kokumi-registry.kokumi.svc.cluster.local:5000/order/external-secrets",
						Version: testVersion,
					},
					Destination: &deliveryv1alpha1.OCIDestination{
						OCI: "oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/external-secrets",
					},
				},
			},
			wantSourceRef: "kokumi-registry.kokumi.svc.cluster.local:5000/order/external-secrets",
			wantDestRef:   "kokumi-registry.kokumi.svc.cluster.local:5000/preparation/external-secrets",
			wantSourceDig: fakeDigest,
		},
		{
			name: "helm render rejected when source is not a helm chart",
			order: &deliveryv1alpha1.Order{
				Spec: deliveryv1alpha1.OrderSpec{
					Source: &deliveryv1alpha1.OCISource{
						OCI:     "oci://kokumi-registry.kokumi.svc.cluster.local:5000/order/my-app",
						Version: testVersion,
					},
					Destination: &deliveryv1alpha1.OCIDestination{
						OCI: "oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/my-app",
					},
					Render: &deliveryv1alpha1.Render{
						Helm: &deliveryv1alpha1.HelmRender{
							ReleaseName: "my-app",
							Namespace:   "default",
						},
					},
				},
			},
			wantErr:    true,
			wantErrMsg: "source is not a Helm chart",
		},
		{
			name: "multiple yaml files merged into single manifest",
			makeClient: func(fs afero.Fs) oci.Client {
				return &multiFileFakeClient{fs: fs}
			},
			order: &deliveryv1alpha1.Order{
				Spec: deliveryv1alpha1.OrderSpec{
					Source: &deliveryv1alpha1.OCISource{
						OCI:     "oci://registry.svc.cluster.local:5000/order/multi-file-app",
						Version: testVersion,
					},
					Destination: &deliveryv1alpha1.OCIDestination{
						OCI: "oci://registry.svc.cluster.local:5000/preparation/multi-file-app",
					},
				},
			},
			wantSourceRef: "registry.svc.cluster.local:5000/order/multi-file-app",
			wantDestRef:   "registry.svc.cluster.local:5000/preparation/multi-file-app",
			wantSourceDig: fakeDigest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()

			var client oci.Client = oci.NewFakeClient(fs)
			if tc.makeClient != nil {
				client = tc.makeClient(fs)
			}

			svc := NewOrderService(client, fs, "")

			var dest string
			if tc.order.Spec.Destination != nil {
				dest = tc.order.Spec.Destination.OCI
			}
			result, err := svc.ProcessOrder(context.Background(), tc.order, *tc.order.Spec.Source, tc.order.Spec.Render, tc.order.Spec.Patches, tc.order.Spec.Edits, dest, "", "", nil, nil)

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tc.wantErrMsg)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tc.wantSourceRef, result.SourceRef.RepositoryReference())
			assert.Equal(t, tc.wantDestRef, result.DestRef.RepositoryReference())
			assert.Equal(t, tc.wantSourceDig, result.SourceRef.Digest)
			assert.Regexp(t, `^sha256:[a-f0-9]{64}$`, result.DestRef.Digest)
		})
	}
}

func TestOrderService_Provenance(t *testing.T) {
	// Base artifact carries standard OCI git provenance annotations.
	baseAnnotations := map[string]string{
		ocispec.AnnotationSource:   "https://github.com/kokumi-dev/example",
		ocispec.AnnotationVersion:  "1.2.3",
		ocispec.AnnotationRevision: "abcdef1234567890abcdef1234567890abcdef12",
	}

	order := &deliveryv1alpha1.Order{
		Spec: deliveryv1alpha1.OrderSpec{
			Source: &deliveryv1alpha1.OCISource{
				OCI:     "oci://registry.svc.cluster.local:5000/order/app",
				Version: testVersion,
			},
			Destination: &deliveryv1alpha1.OCIDestination{
				OCI: "oci://registry.svc.cluster.local:5000/preparation/app",
			},
		},
	}

	t.Run("extracts and copies provenance forward", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &capturingFakeClient{fs: fs, annotations: baseAnnotations}

		svc := NewOrderService(client, fs, "")
		result, err := svc.ProcessOrder(context.Background(), order, *order.Spec.Source, order.Spec.Render, order.Spec.Patches, order.Spec.Edits, order.Spec.Destination.OCI, "", "", nil, nil)
		require.NoError(t, err)

		// Returned on the result.
		assert.Equal(t, "https://github.com/kokumi-dev/example", result.GitRepo)
		assert.Equal(t, "1.2.3", result.GitTag)
		assert.Equal(t, "abcdef1234567890abcdef1234567890abcdef12", result.GitCommitHash)

		// Copied forward onto the rendered artifact's annotations.
		require.NotNil(t, client.lastPushAnnotations)
		assert.Equal(t, "https://github.com/kokumi-dev/example", client.lastPushAnnotations[ocispec.AnnotationSource])
		assert.Equal(t, "1.2.3", client.lastPushAnnotations[ocispec.AnnotationVersion])
		assert.Equal(t, "abcdef1234567890abcdef1234567890abcdef12", client.lastPushAnnotations[ocispec.AnnotationRevision])
		// Base identity stamped for chain verification.
		assert.Equal(t, "registry.svc.cluster.local:5000/order/app", client.lastPushAnnotations[ocispec.AnnotationBaseImageName])
		assert.Equal(t, fakeDigest, client.lastPushAnnotations[ocispec.AnnotationBaseImageDigest])
	})

	t.Run("omits provenance when base has none", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &capturingFakeClient{fs: fs, annotations: nil}

		svc := NewOrderService(client, fs, "")
		result, err := svc.ProcessOrder(context.Background(), order, *order.Spec.Source, order.Spec.Render, order.Spec.Patches, order.Spec.Edits, order.Spec.Destination.OCI, "", "", nil, nil)
		require.NoError(t, err)

		assert.Empty(t, result.GitRepo)
		assert.Empty(t, result.GitTag)
		assert.Empty(t, result.GitCommitHash)
		require.NotNil(t, client.lastPushAnnotations)
		_, hasSource := client.lastPushAnnotations[ocispec.AnnotationSource]
		assert.False(t, hasSource, "source annotation should not be set when base has none")
	})
}

// capturingFakeClient records the annotations passed to Push so tests can assert
// that provenance is copied forward onto the rendered artifact.
type capturingFakeClient struct {
	fs                  afero.Fs
	annotations         map[string]string
	lastPushAnnotations map[string]string
}

var _ oci.Client = (*capturingFakeClient)(nil)

func (c *capturingFakeClient) Pull(_ context.Context, ref oci.Reference, targetDir string) (string, string, map[string]string, error) {
	_, _, _, err := oci.NewFakeClient(c.fs).Pull(context.Background(), ref, targetDir)
	if err != nil {
		return "", "", nil, err
	}
	return "", fakeDigest, c.annotations, nil
}

func (c *capturingFakeClient) Push(_ context.Context, ref oci.Reference, sourceDir string, annotations map[string]string) (string, error) {
	c.lastPushAnnotations = annotations
	return oci.NewFakeClient(c.fs).Push(context.Background(), ref, sourceDir, annotations)
}

func (c *capturingFakeClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func TestOrderService_PullCache(t *testing.T) {
	const cacheDir = "/cache"

	order := &deliveryv1alpha1.Order{
		Spec: deliveryv1alpha1.OrderSpec{
			Source: &deliveryv1alpha1.OCISource{
				OCI:     "oci://registry.svc.cluster.local:5000/order/app",
				Version: testVersion,
			},
			Destination: &deliveryv1alpha1.OCIDestination{
				OCI: "oci://registry.svc.cluster.local:5000/preparation/app",
			},
		},
	}

	t.Run("cache miss populates cache", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		pullCount := 0
		client := &countingFakeClient{fs: fs, onPull: func() { pullCount++ }}

		svc := NewOrderService(client, fs, cacheDir)
		_, err := svc.ProcessOrder(context.Background(), order, *order.Spec.Source, order.Spec.Render, order.Spec.Patches, order.Spec.Edits, order.Spec.Destination.OCI, "", "", nil, nil)
		require.NoError(t, err)

		assert.Equal(t, 1, pullCount, "expected one pull on cache miss")

		ociRef, err := oci.Parse("registry.svc.cluster.local:5000/order/app")
		assert.NoError(t, err)
		ociRef.Tag = testVersion

		// Verify cache entry was written.
		key := pullCacheKey(
			ociRef,
			deliveryv1alpha1.FileLayoutSingle,
		)
		exists, err := afero.Exists(fs, filepath.Join(cacheDir, key, "meta.json"))
		require.NoError(t, err)
		assert.True(t, exists, "meta.json should be written to cache")
	})

	t.Run("cache hit skips pull", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		pullCount := 0
		client := &countingFakeClient{fs: fs, onPull: func() { pullCount++ }}

		svc := NewOrderService(client, fs, cacheDir)

		// First call populates the cache.
		_, err := svc.ProcessOrder(context.Background(), order, *order.Spec.Source, order.Spec.Render, order.Spec.Patches, order.Spec.Edits, order.Spec.Destination.OCI, "", "", nil, nil)
		require.NoError(t, err)
		require.Equal(t, 1, pullCount)

		// Second call with identical spec should hit the cache.
		_, err = svc.ProcessOrder(context.Background(), order, *order.Spec.Source, order.Spec.Render, order.Spec.Patches, order.Spec.Edits, order.Spec.Destination.OCI, "", "", nil, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, pullCount, "second call should be served from cache without pulling")
	})
}

// multiFileFakeClient simulates an OCI artifact that contains multiple individual
// YAML files instead of a single manifest.yaml.
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

// countingFakeClient wraps FakeClient and invokes onPull on every Pull call.
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

func TestMergeYAMLFiles(t *testing.T) {
	tests := []struct {
		name         string
		setup        map[string]string
		wantManifest string
		wantGone     []string
	}{
		{
			name:         "no-op when only manifest.yaml exists",
			setup:        map[string]string{"manifest.yaml": "---\nkind: Pod\n"},
			wantManifest: "---\nkind: Pod\n",
		},
		{
			name:  "no-op when directory has no yaml files",
			setup: map[string]string{"chart.tgz": "binary"},
		},
		{
			name: "multiple yaml files are merged in sorted order",
			setup: map[string]string{
				testServiceFile:    "kind: Service\n",
				testDeploymentFile: "kind: Deployment\n",
			},
			wantManifest: "---\n# Source: deployment.yaml\nkind: Deployment\n---\n# Source: service.yaml\nkind: Service\n",
			wantGone:     []string{testDeploymentFile, testServiceFile},
		},
		{
			name: "existing manifest.yaml included and removed before rewrite",
			setup: map[string]string{
				"manifest.yaml": "kind: ConfigMap\n",
				testServiceFile: "kind: Service\n",
			},
			wantManifest: "---\n# Source: manifest.yaml\nkind: ConfigMap\n---\n# Source: service.yaml\nkind: Service\n",
			wantGone:     []string{testServiceFile},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			for name, content := range tc.setup {
				_ = afero.WriteFile(fs, filepath.Join("/dir", name), []byte(content), 0600)
			}

			require.NoError(t, mergeYAMLFiles(fs, "/dir"))

			if tc.wantManifest == "" {
				exists, _ := afero.Exists(fs, "/dir/manifest.yaml")
				assert.False(t, exists)
			} else {
				data, err := afero.ReadFile(fs, "/dir/manifest.yaml")
				require.NoError(t, err)
				assert.Equal(t, tc.wantManifest, string(data))
			}

			for _, name := range tc.wantGone {
				exists, _ := afero.Exists(fs, filepath.Join("/dir", name))
				assert.False(t, exists, "%s should be removed after merge", name)
			}
		})
	}
}

// kustomizeFakeClient simulates an OCI artifact containing a kustomization.yaml.
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

// fluxFakeClient simulates a Flux OCI artifact (cncf.flux.content layer): a
// kustomization plus individual manifest files, no io.deis.oras.content.unpack
// annotation. The real ORASClient extracts this via extractLayer; the fake
// writes the same files directly.
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

func multiFileOrder(render *deliveryv1alpha1.Render) *deliveryv1alpha1.Order {
	return &deliveryv1alpha1.Order{
		Spec: deliveryv1alpha1.OrderSpec{
			Source: &deliveryv1alpha1.OCISource{
				OCI:     "oci://registry.svc.cluster.local:5000/order/multi-file-app",
				Version: testVersion,
			},
			Destination: &deliveryv1alpha1.OCIDestination{
				OCI: "oci://registry.svc.cluster.local:5000/preparation/multi-file-app",
			},
			Render: render,
		},
	}
}

func TestOrderService_FileLayoutMulti(t *testing.T) {
	t.Run("files kept separate and patched individually", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &multiFileFakeClient{fs: fs}
		svc := NewOrderService(client, fs, "")

		order := multiFileOrder(&deliveryv1alpha1.Render{
			Manifest: &deliveryv1alpha1.ManifestRender{
				Layout: deliveryv1alpha1.FileLayoutMulti,
			},
		})
		order.Spec.Patches = []deliveryv1alpha1.Patch{
			{
				Target: deliveryv1alpha1.PatchTarget{Kind: "Deployment", Name: "my-app"},
				Set:    map[string]string{".spec.replicas": "3"},
			},
		}

		_, err := svc.ProcessOrder(context.Background(), order, *order.Spec.Source, order.Spec.Render, order.Spec.Patches, order.Spec.Edits, order.Spec.Destination.OCI, "", "", nil, nil)
		require.NoError(t, err)

		// The pushed artifact directory was removed after processing; verify via
		// PreviewOrder instead, which returns the concatenated content.
		preview, err := svc.PreviewOrder(context.Background(), *order.Spec.Source, order.Spec.Render, order.Spec.Patches, order.Spec.Edits, "multi-file-app", "default", nil)
		require.NoError(t, err)
		assert.Contains(t, string(preview), "# Source: deployment.yaml")
		assert.Contains(t, string(preview), "# Source: service.yaml")
		assert.Contains(t, string(preview), "replicas: 3")
	})

	t.Run("kustomization.yaml prevents merge even with Single layout", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &kustomizeFakeClient{fs: fs}
		svc := NewOrderService(client, fs, "")

		order := multiFileOrder(nil)

		preview, err := svc.PreviewOrder(context.Background(), *order.Spec.Source, order.Spec.Render, nil, nil, "multi-file-app", "default", nil)
		require.NoError(t, err)
		assert.Contains(t, string(preview), "# Source: deployment.yaml")
		assert.Contains(t, string(preview), "# Source: kustomization.yaml")
	})

	t.Run("default still merges into manifest.yaml", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &multiFileFakeClient{fs: fs}
		svc := NewOrderService(client, fs, "")

		order := multiFileOrder(nil)

		preview, err := svc.PreviewOrder(context.Background(), *order.Spec.Source, order.Spec.Render, nil, nil, "multi-file-app", "default", nil)
		require.NoError(t, err)
		assert.Contains(t, string(preview), "# Source: deployment.yaml")
		assert.Contains(t, string(preview), "# Source: service.yaml")
	})
}

func TestOrderService_FluxKustomizeArtifact(t *testing.T) {
	fs := afero.NewMemMapFs()
	client := &fluxFakeClient{fs: fs}
	svc := NewOrderService(client, fs, "")

	order := multiFileOrder(nil)
	order.Spec.Patches = []deliveryv1alpha1.Patch{
		{
			Target: deliveryv1alpha1.PatchTarget{Kind: "Deployment", Name: "podinfo"},
			Set:    map[string]string{".spec.replicas": "2"},
		},
	}

	preview, err := svc.PreviewOrder(context.Background(), *order.Spec.Source, order.Spec.Render, order.Spec.Patches, order.Spec.Edits, "podinfo-kustomize", "default", nil)
	require.NoError(t, err)

	// Files are kept separate (kustomization present) and each is patched.
	assert.Contains(t, string(preview), "# Source: deployment.yaml")
	assert.Contains(t, string(preview), "# Source: service.yaml")
	assert.Contains(t, string(preview), "# Source: kustomization.yaml")
	assert.Contains(t, string(preview), "replicas: 2")
}

func TestOrderService_CacheSplitByLayout(t *testing.T) {
	const cacheDir = "/cache"

	fs := afero.NewMemMapFs()
	pullCount := 0
	client := &countingFakeClient{fs: fs, onPull: func() { pullCount++ }}
	svc := NewOrderService(client, fs, cacheDir)

	single := multiFileOrder(nil)
	separate := multiFileOrder(&deliveryv1alpha1.Render{
		Manifest: &deliveryv1alpha1.ManifestRender{
			Layout: deliveryv1alpha1.FileLayoutMulti,
		},
	})

	_, err := svc.ProcessOrder(context.Background(), single, *single.Spec.Source, single.Spec.Render, nil, nil, single.Spec.Destination.OCI, "", "", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, pullCount)

	// Different file layout must not reuse the cached merged layout.
	_, err = svc.ProcessOrder(context.Background(), separate, *separate.Spec.Source, separate.Spec.Render, nil, nil, separate.Spec.Destination.OCI, "", "", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, pullCount, "separate file layout must not share cache entry")

	// Same policy again hits the cache.
	_, err = svc.ProcessOrder(context.Background(), single, *single.Spec.Source, single.Spec.Render, nil, nil, single.Spec.Destination.OCI, "", "", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, pullCount)
}

func TestMergeYAMLFiles_KustomizationSkipped(t *testing.T) {
	fs := afero.NewMemMapFs()
	_ = afero.WriteFile(fs, "/dir/kustomization.yaml", []byte("resources:\n- deployment.yaml\n"), 0600)
	_ = afero.WriteFile(fs, "/dir/deployment.yaml", []byte("kind: Deployment\n"), 0600)

	require.NoError(t, mergeYAMLFiles(fs, "/dir"))

	data, err := afero.ReadFile(fs, "/dir/manifest.yaml")
	require.NoError(t, err)
	assert.NotContains(t, string(data), "resources:")
	assert.Contains(t, string(data), "kind: Deployment")

	exists, _ := afero.Exists(fs, "/dir/kustomization.yaml")
	assert.True(t, exists, "kustomization.yaml must survive the merge")
}
