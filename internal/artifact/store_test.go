package artifact_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// failingResolveClient resolves nothing: every Resolve call errors.
type failingResolveClient struct {
	fs afero.Fs
}

var _ oci.Client = (*failingResolveClient)(nil)

func (c *failingResolveClient) Pull(ctx context.Context, ref oci.Reference, targetDir string) (string, string, map[string]string, error) {
	return oci.NewFakeClient(c.fs).Pull(ctx, ref, targetDir)
}

func (c *failingResolveClient) Push(ctx context.Context, ref oci.Reference, sourceDir string, annotations map[string]string) (string, error) {
	return oci.NewFakeClient(c.fs).Push(ctx, ref, sourceDir, annotations)
}

func (c *failingResolveClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func (c *failingResolveClient) Resolve(_ context.Context, _ oci.Reference) (string, error) {
	return "", assert.AnError
}

func (c *failingResolveClient) Copy(_ context.Context, _ oci.Client, _, _ oci.Reference) error {
	return nil
}

func TestStore_Copy_ResolveFallback(t *testing.T) {
	t.Run("target resolve fails falls back to source", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		failing := &failingResolveClient{fs: fs}
		working := oci.NewFakeClient(fs)
		store := artifact.NewStore(failing, fs, "")

		digest, err := store.Copy(context.Background(),
			artifact.Source{OCI: testChartsOCI, Version: testVersion},
			working,
			artifact.Destination{OCI: testVendorOCI},
			failing,
		)
		require.NoError(t, err)
		assert.Equal(t, fakeDigest, digest)
	})

	t.Run("both resolves failing returns error", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		failing := &failingResolveClient{fs: fs}
		store := artifact.NewStore(failing, fs, "")

		_, err := store.Copy(context.Background(),
			artifact.Source{OCI: testChartsOCI, Version: testVersion},
			failing,
			artifact.Destination{OCI: testVendorOCI},
			failing,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to resolve copied digest")
	})
}

func TestStore_ResolveDigest_Errors(t *testing.T) {
	t.Run("invalid ref", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		store := artifact.NewStore(oci.NewFakeClient(fs), fs, "")

		_, err := store.ResolveDigest(context.Background(), artifact.Source{
			OCI:     "not-a-ref",
			Version: testVersion,
		}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse source ref")
	})

	t.Run("registry resolve error", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		store := artifact.NewStore(&failingResolveClient{fs: fs}, fs, "")

		_, err := store.ResolveDigest(context.Background(), artifact.Source{
			OCI:     testSourceOCI,
			Version: testVersion,
		}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to resolve digest")
	})

	t.Run("nil client and nil default returns empty digest", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		store := artifact.NewStore(nil, fs, "")

		digest, err := store.ResolveDigest(context.Background(), artifact.Source{
			OCI:     testSourceOCI,
			Version: testVersion,
		}, nil)
		require.NoError(t, err)
		assert.Empty(t, digest)
	})
}

func TestPipeline_Render_HelmChart(t *testing.T) {
	t.Run("successful full render pushes rendered manifest", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &helmFakeClient{fs: fs}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		req := baseRenderRequest()
		req.Source.OCI = testChartsAppOCI
		req.Destination.OCI = testDestOCI
		req.Render = &artifact.RenderSpec{
			Helm: &artifact.HelmSpec{ReleaseName: testAppName, Namespace: testNamespaceName},
		}

		result, err := p.Render(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Regexp(t, `^sha256:[a-f0-9]{64}$`, result.DestRef.Digest)
	})

	t.Run("helm render failure surfaces chart error", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &helmFakeClient{fs: fs}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		req := baseRenderRequest()
		req.Source.OCI = testChartsAppOCI
		req.Destination.OCI = testDestOCI
		// A release name that Helm rejects triggers a render error.
		req.Render = &artifact.RenderSpec{
			Helm: &artifact.HelmSpec{ReleaseName: "INVALID..Name", Namespace: testNamespaceName},
		}

		_, err := p.Render(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to render Helm chart")
	})
}

func TestPipeline_Preview_Files(t *testing.T) {
	t.Run("returns sorted files individually", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &multiFileFakeClient{fs: fs}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		files, err := p.Preview(context.Background(), artifact.PreviewRequest{
			Source: artifact.Source{
				OCI:     "oci://registry.svc.cluster.local:5000/order/multi-file-app",
				Version: testVersion,
			},
			Render: &artifact.RenderSpec{
				Manifest: &artifact.ManifestSpec{Layout: artifact.LayoutMulti},
			},
			Name:      "multi-file-app",
			Namespace: testNamespaceName,
		})
		require.NoError(t, err)
		require.Len(t, files, 2)
		assert.Equal(t, testDeploymentFile, files[0].Path)
		assert.Equal(t, testServiceFile, files[1].Path)
		assert.Contains(t, files[0].Content, "kind: Deployment")
	})

	t.Run("empty artifact errors", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		// plain oci.FakeClient writes only a bare manifest.yaml which the
		// listing picks up; use a client that writes nothing at all.
		p := artifact.NewPipeline(artifact.NewStore(&emptyFakeClient{fs: fs}, fs, ""))

		_, err := p.Preview(context.Background(), artifact.PreviewRequest{
			Source: artifact.Source{
				OCI:     "oci://registry.svc.cluster.local:5000/order/empty",
				Version: testVersion,
			},
			Name:      "empty",
			Namespace: testNamespaceName,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no YAML files found")
	})

	t.Run("PreviewManifest concatenates with Source headers", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &multiFileFakeClient{fs: fs}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		manifest, err := p.PreviewManifest(context.Background(), artifact.PreviewRequest{
			Source: artifact.Source{
				OCI:     "oci://registry.svc.cluster.local:5000/order/multi-file-app",
				Version: testVersion,
			},
			Render: &artifact.RenderSpec{
				Manifest: &artifact.ManifestSpec{Layout: artifact.LayoutMulti},
			},
			Name:      "multi-file-app",
			Namespace: testNamespaceName,
		})
		require.NoError(t, err)
		assert.Contains(t, string(manifest), "---\n# Source: deployment.yaml\n")
		assert.Contains(t, string(manifest), "---\n# Source: service.yaml\n")
	})

	t.Run("helm chart renders without pushing", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &helmFakeClient{fs: fs}
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		files, err := p.Preview(context.Background(), artifact.PreviewRequest{
			Source: artifact.Source{
				OCI:     testChartsAppOCI,
				Version: testVersion,
			},
			Render: &artifact.RenderSpec{
				Helm: &artifact.HelmSpec{ReleaseName: testAppName, Namespace: testNamespaceName},
			},
			Name:      "app",
			Namespace: testNamespaceName,
		})
		require.NoError(t, err)
		require.NotEmpty(t, files)
		assert.Equal(t, "manifest.yaml", files[0].Path)
		assert.Contains(t, files[0].Content, "kind:")
	})
}

// emptyFakeClient pulls nothing: the target directory stays empty.
type emptyFakeClient struct {
	fs afero.Fs
}

var _ oci.Client = (*emptyFakeClient)(nil)

func (c *emptyFakeClient) Pull(_ context.Context, _ oci.Reference, _ string) (string, string, map[string]string, error) {
	return "", fakeDigest, nil, nil
}

func (c *emptyFakeClient) Push(_ context.Context, _ oci.Reference, _ string, _ map[string]string) (string, error) {
	return fakeDigest, nil
}

func (c *emptyFakeClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func (c *emptyFakeClient) Resolve(_ context.Context, _ oci.Reference) (string, error) {
	return fakeDigest, nil
}

func (c *emptyFakeClient) Copy(_ context.Context, _ oci.Client, _, _ oci.Reference) error {
	return nil
}
