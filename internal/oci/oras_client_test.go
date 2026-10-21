package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/errcode"
	"github.com/stretchr/testify/require"
)

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

func TestORASClient_Pull_helmChart(t *testing.T) {
	const provenanceMediaType = "application/vnd.cncf.helm.chart.provenance.v1.prov"

	for name, provenanceFirst := range map[string]bool{
		"chart layer first":      false,
		"provenance layer first": true,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			host := newTestRegistry(t, false)
			repo, err := remote.NewRepository(host + "/charts/podinfo")
			require.NoError(t, err)
			repo.Registry.PlainHTTP = true

			describe := func(mediaType string, data []byte) ocispec.Descriptor {
				return ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(data), Size: int64(len(data))}
			}
			push := func(mediaType string, data []byte) ocispec.Descriptor {
				desc := describe(mediaType, data)
				require.NoError(t, repo.Push(ctx, desc, bytes.NewReader(data)))
				return desc
			}
			config := push("application/vnd.cncf.helm.config.v1+json", []byte(`{"name":"podinfo","version":"6.14.1"}`))
			layers := []ocispec.Descriptor{push(HelmChartLayerMediaType, []byte("chart")), push(provenanceMediaType, []byte("provenance"))}
			if provenanceFirst {
				slices.Reverse(layers)
			}
			manifest, err := json.Marshal(ocispec.Manifest{
				SchemaVersion: 2,
				MediaType:     ocispec.MediaTypeImageManifest,
				Config:        config,
				Layers:        layers,
			})
			require.NoError(t, err)
			manifestDesc := describe(ocispec.MediaTypeImageManifest, manifest)
			require.NoError(t, repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifest), "6.14.1"))

			ref, err := Parse("oci://" + host + "/charts/podinfo:6.14.1")
			require.NoError(t, err)
			dir := t.TempDir()
			mediaType, pulled, _, err := NewClient(Options{PlainHTTP: true}).Pull(ctx, ref, dir)
			require.NoError(t, err)

			require.Equal(t, HelmChartLayerMediaType, mediaType)
			require.Equal(t, manifestDesc.Digest.String(), pulled)
			chart, err := os.ReadFile(filepath.Join(dir, "chart.tgz"))
			require.NoError(t, err)
			require.Equal(t, "chart", string(chart))
		})
	}
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
	ref, err := Parse(host + "/app:1")
	require.NoError(t, err)

	client := NewClient(Options{Host: host, Credential: credentials.Credential{Username: "u", Password: "p"}, PlainHTTP: true})
	for range 2 {
		desc, err := client.Resolve(context.Background(), ref)
		require.NoError(t, err)
		require.Equal(t, digest, desc.Digest)
	}

	require.Equal(t, int32(1), tokens.Load(), "the token is fetched once")
	require.Equal(t, int32(1), challenges.Load(), "the second request carries the cached token")
}

const (
	testUsername = "user"
	testPassword = "pass"
)

// basicAuthServer serves the registry API base endpoint only to requests
// that carry user:pass.
func basicAuthServer(t *testing.T, newServer func(http.Handler) *httptest.Server) *httptest.Server {
	t.Helper()
	srv := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != testUsername || pass != testPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewClient_sendsCredentialOnlyToItsHost(t *testing.T) {
	srv := basicAuthServer(t, httptest.NewServer)
	host := strings.TrimPrefix(srv.URL, "http://")
	cred := credentials.Credential{Username: testUsername, Password: testPassword}

	require.NoError(t, NewClient(Options{Host: host, Credential: cred, PlainHTTP: true}).Ping(context.Background(), host))
	require.Error(t, NewClient(Options{Host: "registry.example.com", Credential: cred, PlainHTTP: true}).Ping(context.Background(), host),
		"a credential for another host is not sent")
	require.Error(t, NewClient(Options{PlainHTTP: true}).Ping(context.Background(), host), "anonymous")
}

func TestPing_anonymous(t *testing.T) {
	// A token registry such as ghcr.io asks for a bearer token at its base
	// endpoint and denies an anonymous one there, but grants anonymous
	// tokens for its public repositories.
	bearer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="http://`+r.Host+`/token",service="test",scope="repository:user/image:pull"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(bearer.Close)
	bearerHost := strings.TrimPrefix(bearer.URL, "http://")
	require.NoError(t, NewClient(Options{PlainHTTP: true}).Ping(context.Background(), bearerHost))

	basicHost := strings.TrimPrefix(basicAuthServer(t, httptest.NewServer).URL, "http://")
	err := NewClient(Options{PlainHTTP: true}).Ping(context.Background(), basicHost)
	require.ErrorIs(t, err, auth.ErrBasicCredentialNotFound, "a basic auth registry refuses anonymous access")

	missing := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(missing.Close)
	err = NewClient(Options{PlainHTTP: true}).Ping(context.Background(), strings.TrimPrefix(missing.URL, "http://"))
	var resp *errcode.ErrorResponse
	require.ErrorAs(t, err, &resp, "a server that does not serve the registry API")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestNewClient_trustsTheGivenCA(t *testing.T) {
	srv := basicAuthServer(t, httptest.NewTLSServer)
	host := strings.TrimPrefix(srv.URL, "https://")
	cred := credentials.Credential{Username: testUsername, Password: testPassword}

	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	trusted := NewClient(Options{Host: host, Credential: cred, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}})
	require.NoError(t, trusted.Ping(context.Background(), host))

	err := NewClient(Options{Host: host, Credential: cred}).Ping(context.Background(), host)
	require.ErrorContains(t, err, "certificate", "the system trust store does not know the test CA")
}

func TestCanonicalHost(t *testing.T) {
	for host, want := range map[string]string{
		dockerHubHost:          dockerHubHost,
		"index.docker.io":      dockerHubHost,
		"registry-1.docker.io": dockerHubHost,
		"GHCR.io":              "ghcr.io",
		"localhost:5000":       "localhost:5000",
	} {
		require.Equal(t, want, CanonicalHost(host), host)
	}
}

// mountRegistry is a registry that serves one image manifest in the
// repository "src" and accepts blob mounts and manifest pushes for any other
// repository. It counts the mounts it was asked for.
type mountRegistry struct {
	manifest, config, layer []byte
	mounts                  atomic.Int32
}

func newMountRegistry(t *testing.T) (*mountRegistry, string) {
	t.Helper()
	reg := &mountRegistry{config: []byte("{}"), layer: []byte("layer")}
	manifest := ocispec.Manifest{
		SchemaVersion: 2,
		MediaType:     ocispec.MediaTypeImageManifest,
		Config:        ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromBytes(reg.config), Size: int64(len(reg.config))},
		Layers:        []ocispec.Descriptor{{MediaType: ocispec.MediaTypeImageLayer, Digest: digest.FromBytes(reg.layer), Size: int64(len(reg.layer))}},
	}
	var err error
	reg.manifest, err = json.Marshal(manifest)
	require.NoError(t, err)

	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	return reg, strings.TrimPrefix(srv.URL, "http://")
}

func (reg *mountRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v2/src/manifests/1":
		w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
		w.Header().Set("Docker-Content-Digest", digest.FromBytes(reg.manifest).String())
		w.Header().Set("Content-Length", fmt.Sprint(len(reg.manifest)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(reg.manifest)
		}
	case r.Method == http.MethodPost && r.URL.Query().Get("mount") != "":
		reg.mounts.Add(1)
		w.Header().Set("Location", r.URL.Path+r.URL.Query().Get("mount"))
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/manifests/"):
		w.Header().Set("Docker-Content-Digest", digest.FromBytes(reg.manifest).String())
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestORASClient_Copy_mountsBetweenClientsOfOneRegistry(t *testing.T) {
	reg, host := newMountRegistry(t)
	src, err := Parse(host + "/src:1")
	require.NoError(t, err)
	dst, err := Parse(host + "/dst:1")
	require.NoError(t, err)

	// Two clients, as for two Registry objects on the same host.
	reader := NewClient(Options{PlainHTTP: true})
	writer := NewClient(Options{PlainHTTP: true})
	require.NoError(t, reader.Copy(context.Background(), writer, src, dst, CopyOptions{}))

	require.Equal(t, int32(2), reg.mounts.Load(), "the config and the layer are mounted, not transferred")
}
