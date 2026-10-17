package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func Test_IsPlainHTTP(t *testing.T) {
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
			require.Equal(t, tc.want, IsPlainHTTP(tc.ref))
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

const (
	testDeploymentFile = "deployment.yaml"
	testServiceFile    = "service.yaml"
	testDeploymentYAML = "kind: Deployment\n"
	testServiceYAML    = "kind: Service\n"
)

func TestExtractTar(t *testing.T) {
	t.Run("extracts regular files preserving names", func(t *testing.T) {
		dir := t.TempDir()
		buf := writeTar(t, map[string]string{
			testDeploymentFile: testDeploymentYAML,
			testServiceFile:    testServiceYAML,
		})

		require.NoError(t, extractTar(buf, dir))

		for name, want := range map[string]string{
			testDeploymentFile: testDeploymentYAML,
			testServiceFile:    testServiceYAML,
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

func TestORASClient_authenticatedClientCachesTokens(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var tokens, challenges atomic.Int32

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			tokens.Add(1)
			_, _ = w.Write([]byte(`{"token":"tok"}`))
		case r.Header.Get("Authorization") != "Bearer tok":
			challenges.Add(1)
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="test",scope="repository:app:pull"`, srv.URL))
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", digest)
			w.Header().Set("Content-Length", "2")
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	creds, err := CredentialsFromDockerConfigJSON(fmt.Appendf(nil, `{"auths":{%q:{"username":"u","password":"p"}}}`, host))
	require.NoError(t, err)
	ref, err := Parse(host + "/app:1")
	require.NoError(t, err)

	client := NewAuthenticatedORASClient(creds)
	for range 2 {
		desc, err := client.Resolve(context.Background(), ref)
		require.NoError(t, err)
		require.Equal(t, digest, desc.Digest)
	}

	require.Equal(t, int32(1), tokens.Load(), "the token is fetched once")
	require.Equal(t, int32(1), challenges.Load(), "the second request carries the cached token")
}
