package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/resolve"
)

const (
	webPreparation = "web-aaaaaaaaaaaa"
	// fakeManifestFile is the one file every pull of oci.FakeClient writes.
	fakeManifestFile = "manifest.yaml"
)

// serveAsAdmin routes req through the API as the admin login, which may read
// objs. Every artifact pull returns fakeManifestFile.
func serveAsAdmin(t *testing.T, req *http.Request, objs ...client.Object) *httptest.ResponseRecorder {
	t.Helper()
	fs := afero.NewMemMapFs()
	return serveAsAdminWith(t, req, testBackend{fs: fs, registry: oci.NewFakeClient(fs)}, objs...)
}

// testBackend is what the handlers under test read besides the cluster.
type testBackend struct {
	fs afero.Fs
	// registry serves every Registry.
	registry oci.Client
	// kitchen is the Kitchen, nil for none.
	kitchen *deliveryv1alpha1.Kitchen
	// cluster is what the admin reads and writes, built from the objects
	// passed along when nil.
	cluster client.Client
}

// serveAsAdminWith serves req as the admin, with objs in the cluster and
// backend behind it.
func serveAsAdminWith(t *testing.T, req *http.Request, backend testBackend, objs ...client.Object) *httptest.ResponseRecorder {
	t.Helper()
	fs, registryClient := backend.fs, backend.registry
	if fs == nil {
		fs = afero.NewMemMapFs()
	}
	store := artifact.NewStore(registryClient, fs, "")
	imp := newImpersonator(nil, nil, capsInstallNamespace)
	cluster := backend.cluster
	if cluster == nil {
		cluster = fake.NewClientBuilder().WithScheme(unionScheme(t)).WithObjects(objs...).Build()
	}
	imp.clients[adminServiceAccountName] = cluster
	deps := &apiDeps{
		registries:   registry.NewClients(artifact.DefaultRegistryHost).WithClientFactory(func(oci.Options) oci.Client { return registryClient }),
		store:        store,
		pipeline:     artifact.NewPipeline(store),
		logger:       logr.Discard(),
		impersonator: imp,
		saList: func() []*corev1.ServiceAccount {
			return []*corev1.ServiceAccount{{Name: adminServiceAccountName}}
		},
		kitchen: func() (*deliveryv1alpha1.Kitchen, error) {
			if backend.kitchen == nil {
				return nil, errors.New("no Kitchen")
			}
			return backend.kitchen, nil
		},
	}

	mux := http.NewServeMux()
	addRoutes(mux, nil, deps, nil, capsInstallNamespace)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(withIdentity(req.Context(), adminLogin)))
	return rec
}

// requireFiles asserts that rec is a JSON list of files with the given paths.
func requireFiles(t *testing.T, rec *httptest.ResponseRecorder, paths ...string) {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var files []ArtifactFileDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &files))
	got := make([]string, len(files))
	for i, f := range files {
		got[i] = f.Path
	}
	require.Equal(t, paths, got)
}

func TestHandlePreview(t *testing.T) {
	const source = "oci://registry.example.com/charts/web"
	product := &deliveryv1alpha1.Product{
		Name:      "web",
		Namespace: nsA,
		Status: deliveryv1alpha1.ProductStatus{
			Source: &deliveryv1alpha1.ProductSourceStatus{URL: source, Version: testSourceVersion},
		},
	}

	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "Order",
			path: "/api/v1/orders/preview",
			body: `{"namespace":"` + nsA + `","name":"web-dev","productRef":{"name":"web"}}`,
		},
		{
			name: "Product",
			path: "/api/v1/products/preview",
			body: `{"namespace":"` + nsA + `","name":"web","source":{"url":"` + source + `","version":"1.0.0"},"images":{}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			requireFiles(t, serveAsAdmin(t, req, product), fakeManifestFile)
		})
	}
}

func TestPreviewRequest(t *testing.T) {
	helm := func(values string) *deliveryv1alpha1.Render {
		return &deliveryv1alpha1.Render{Helm: &deliveryv1alpha1.HelmRender{Values: &apiextensionsv1.JSON{Raw: []byte(values)}}}
	}
	source := deliveryv1alpha1.OCISource{URL: "oci://registry/charts/app", Version: testSourceVersion}

	t.Run("rejects Helm values that are not an object", func(t *testing.T) {
		p := &preview{spec: &resolve.EffectiveSpec{Source: source, Render: helm(`[1,2]`)}}

		_, err := p.request()
		require.ErrorContains(t, err, "invalid render config")
	})

	t.Run("pulls the digest the Product resolved", func(t *testing.T) {
		const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		p := &preview{spec: &resolve.EffectiveSpec{Source: source, SourceDigest: digest, Render: helm(`{"replicaCount":2}`)}}

		req, err := p.request()
		require.NoError(t, err)
		require.Equal(t, digest, req.Source.Digest)
		require.Equal(t, map[string]any{"replicaCount": float64(2)}, req.Render.Helm.Values)
	})

	t.Run("passes the image policy, mirrors and vendor location", func(t *testing.T) {
		p := &preview{
			spec: &resolve.EffectiveSpec{Source: source, ImagesVendorURL: "oci://registry/shop/vendor/app", Images: deliveryv1alpha1.ProductImages{
				Policy:     deliveryv1alpha1.ImagePolicyVendor,
				FieldSpecs: []deliveryv1alpha1.ImageFieldSpec{{Kind: prometheusKind, Path: imagePath}},
			}},
			mirrors: []images.Mirror{{Prefix: dockerHub, Location: "mirror.example.com/hub"}},
			locked:  []images.Image{{Ref: nginxRef, Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		}

		req, err := p.request()
		require.NoError(t, err)
		require.Equal(t, images.PolicyVendor, req.Images.Policy)
		require.Equal(t, []images.FieldSpec{{Kind: prometheusKind, Path: imagePath}}, req.Images.FieldSpecs)
		require.Equal(t, p.mirrors, req.Images.Mirrors)
		require.Equal(t, p.locked, req.Images.Locked)
		require.Equal(t, "oci://registry/shop/vendor/app", req.Images.VendorURL)
	})
}

func TestLockedImagesAs(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	order := newOrder("web", nsA)
	order.Status.LatestPreparationName = webPreparation
	prep := &deliveryv1alpha1.Preparation{
		Name:      webPreparation,
		Namespace: nsA,
		Spec: deliveryv1alpha1.PreparationSpec{Images: []deliveryv1alpha1.PreparationImage{
			{Ref: nginxRef, Name: nginxName, Digest: digest, Vendored: "registry/shop/web/images/docker.io/library/nginx:1.27@" + digest},
		}},
	}
	c := fake.NewClientBuilder().WithScheme(unionScheme(t)).WithObjects(order, prep).Build()
	uc := &userClient{imp: &impersonator{clients: map[string]client.Client{saEditor: c}}, sas: []corev1.ServiceAccount{newSA(saEditor)}}
	deps := &apiDeps{logger: logr.Discard()}
	pin := deliveryv1alpha1.ProductImages{Policy: deliveryv1alpha1.ImagePolicyPin}
	ctx := context.Background()

	require.Equal(t, []images.Image{
		{Ref: nginxRef, Name: nginxName, Digest: digest, Vendored: "registry/shop/web/images/docker.io/library/nginx:1.27@" + digest},
	}, lockedImagesAs(ctx, deps, uc, pin, nsA, "web"))
	require.Nil(t, lockedImagesAs(ctx, deps, uc, deliveryv1alpha1.ProductImages{}, nsA, "web"), "Record locks nothing")
	require.Nil(t, lockedImagesAs(ctx, deps, uc, pin, nsA, "new"), "a new Order locks nothing")
}
