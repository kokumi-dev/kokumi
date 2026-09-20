package artifact_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// packChartDir tars+gzips the sample chart directory into a chart.tgz file on
// the given filesystem, mimicking how a real Helm chart OCI layer is pulled.
func packChartDir(fs afero.Fs, targetDir string) error {
	srcRoot := filepath.Join("..", "renderer", "testdata", "sample-chart")

	tmp, err := os.MkdirTemp("", "chart-pack-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	tarPath := filepath.Join(tmp, "chart.tgz")
	f, err := os.Create(tarPath)
	if err != nil {
		return err
	}

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	walkErr := filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()

		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = "sample-chart/" + filepath.ToSlash(rel)

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		_, err = io.Copy(tw, file)
		return err
	})
	if walkErr != nil {
		_ = f.Close()
		return walkErr
	}
	if err := tw.Close(); err != nil {
		_ = f.Close()
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	data, err := os.ReadFile(tarPath)
	if err != nil {
		return err
	}

	return afero.WriteFile(fs, filepath.Join(targetDir, "chart.tgz"), data, 0600)
}

// helmFakeClient simulates pulling a Helm chart OCI artifact: it writes a
// chart.tgz into the target directory and returns the Helm layer media type.
type helmFakeClient struct {
	fs afero.Fs
}

var _ oci.Client = (*helmFakeClient)(nil)

func (c *helmFakeClient) Pull(ctx context.Context, ref oci.Reference, targetDir string) (string, string, map[string]string, error) {
	if err := packChartDir(c.fs, targetDir); err != nil {
		return "", "", nil, err
	}
	return oci.HelmChartLayerMediaType, fakeDigest, nil, nil
}

func (c *helmFakeClient) Push(ctx context.Context, ref oci.Reference, sourceDir string, annotations map[string]string) (string, error) {
	return oci.NewFakeClient(c.fs).Push(ctx, ref, sourceDir, annotations)
}

func (c *helmFakeClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func (c *helmFakeClient) Resolve(_ context.Context, _ oci.Reference) (string, error) {
	return fakeDigest, nil
}

func (c *helmFakeClient) Copy(_ context.Context, _ oci.Client, _, _ oci.Reference) error {
	return nil
}

// nestedFilesFakeClient simulates an artifact with nested directories and a
// JSON file, exercising the recursive rich listing.
type nestedFilesFakeClient struct {
	fs afero.Fs
}

var _ oci.Client = (*nestedFilesFakeClient)(nil)

func (c *nestedFilesFakeClient) Pull(_ context.Context, _ oci.Reference, targetDir string) (string, string, map[string]string, error) {
	files := map[string]string{
		"base/deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: my-app\n",
		"service.yaml":         "apiVersion: v1\nkind: Service\nmetadata:\n  name: my-app\n",
		"config.json":          `{"port": 80}`,
	}
	for name, content := range files {
		if err := afero.WriteFile(c.fs, filepath.Join(targetDir, name), []byte(content), 0600); err != nil {
			return "", "", nil, err
		}
	}
	return "", fakeDigest, nil, nil
}

func (c *nestedFilesFakeClient) Push(_ context.Context, _ oci.Reference, _ string, _ map[string]string) (string, error) {
	return fakeDigest, nil
}

func (c *nestedFilesFakeClient) ListTags(_ context.Context, _ oci.Reference) ([]string, error) {
	return nil, nil
}

func (c *nestedFilesFakeClient) Resolve(_ context.Context, _ oci.Reference) (string, error) {
	return fakeDigest, nil
}

func (c *nestedFilesFakeClient) Copy(_ context.Context, _ oci.Client, _, _ oci.Reference) error {
	return nil
}

func TestStore_Inspect(t *testing.T) {
	t.Run("manifest bundle with recursive files", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		client := &nestedFilesFakeClient{fs: fs}
		store := artifact.NewStore(client, fs, "")

		result, err := store.Inspect(context.Background(), "oci://registry.svc.cluster.local:5000/order/nested", nil)
		require.NoError(t, err)

		assert.False(t, result.IsHelm)
		require.Len(t, result.Files, 3)
		assert.Equal(t, "base/deployment.yaml", result.Files[0].Path)
		assert.Equal(t, "config.json", result.Files[1].Path)
		assert.Equal(t, "service.yaml", result.Files[2].Path)
		assert.Contains(t, result.Manifest, "kind: Deployment")
		assert.Contains(t, result.Manifest, `"port": 80`)
	})

	t.Run("invalid ref rejected", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		store := artifact.NewStore(oci.NewFakeClient(fs), fs, "")

		_, err := store.Inspect(context.Background(), "not-a-ref", nil)
		require.Error(t, err)
	})
}

func TestStore_Inspect_HelmChart(t *testing.T) {
	fs := afero.NewMemMapFs()
	store := artifact.NewStore(&helmFakeClient{fs: fs}, fs, "")

	result, err := store.Inspect(context.Background(), "oci://registry.svc.cluster.local:5000/charts/podinfo:6.14.1", nil)
	require.NoError(t, err)

	assert.True(t, result.IsHelm)
	require.NotNil(t, result.ChartInfo)
	assert.NotEmpty(t, result.ChartInfo.Name)
	assert.NotEmpty(t, result.ChartInfo.ChartVersion)
}
