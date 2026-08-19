package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_isPlainHTTP(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		// In-cluster Kubernetes service DNS
		{"kokumi-registry.kokumi.svc.cluster.local:5000/repo/image", true},
		{"registry.default.svc.cluster.local/repo/image", true},
		{"registry.svc/image", true},
		// Loopback and bare IPs
		{"localhost:5000/image", true},
		{"127.0.0.1:5000/image", true},
		{"10.96.0.1/image", true},
		// Public / external registries
		{"ghcr.io/myorg/myimage", false},
		{"registry-1.docker.io/library/nginx", false},
		{"public.ecr.aws/myimage", false},
		{"gcr.io/google-containers/pause", false},
	}

	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			require.Equal(t, tc.want, isPlainHTTP(tc.ref))
		})
	}
}

// writeTar builds an uncompressed tar archive from the given file entries.
func writeTar(t *testing.T, entries map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range entries {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0600,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return &buf
}

func TestExtractTar(t *testing.T) {
	t.Run("extracts regular files preserving names", func(t *testing.T) {
		dir := t.TempDir()
		buf := writeTar(t, map[string]string{
			"deployment.yaml": "kind: Deployment\n",
			"service.yaml":    "kind: Service\n",
		})

		require.NoError(t, extractTar(buf, dir))

		for name, want := range map[string]string{
			"deployment.yaml": "kind: Deployment\n",
			"service.yaml":    "kind: Service\n",
		} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err)
			require.Equal(t, want, string(data))
		}
	})

	t.Run("rejects path traversal entries", func(t *testing.T) {
		dir := t.TempDir()
		buf := writeTar(t, map[string]string{"../escape.yaml": "nope\n"})

		require.Error(t, extractTar(buf, dir))
	})
}

// gzipTar wraps a tar archive in gzip, matching the Flux content layer format.
func gzipTar(t *testing.T, entries map[string]string) *bytes.Buffer {
	t.Helper()
	raw := writeTar(t, entries)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, err := gz.Write(raw.Bytes())
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	return &buf
}

func TestExtractGzipTar(t *testing.T) {
	t.Run("extracts gzip-compressed Flux layer preserving names", func(t *testing.T) {
		dir := t.TempDir()
		buf := gzipTar(t, map[string]string{
			"deployment.yaml":    "kind: Deployment\n",
			"kustomization.yaml": "resources:\n- deployment.yaml\n",
			"service.yaml":       "kind: Service\n",
		})

		require.NoError(t, extractGzipTar(buf, FluxContentMediaType, dir))

		for name, want := range map[string]string{
			"deployment.yaml":    "kind: Deployment\n",
			"kustomization.yaml": "resources:\n- deployment.yaml\n",
			"service.yaml":       "kind: Service\n",
		} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err)
			require.Equal(t, want, string(data))
		}
	})

	t.Run("rejects path traversal in gzip layer", func(t *testing.T) {
		dir := t.TempDir()
		buf := gzipTar(t, map[string]string{"../escape.yaml": "nope\n"})

		require.Error(t, extractGzipTar(buf, FluxContentMediaType, dir))
	})
}
