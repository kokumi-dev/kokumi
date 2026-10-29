package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

func TestHandleGetPreparationManifest(t *testing.T) {
	prep := &deliveryv1alpha1.Preparation{
		Name:      webPreparation,
		Namespace: nsA,
		Spec: deliveryv1alpha1.PreparationSpec{
			OrderName: "web",
			Artifact: deliveryv1alpha1.Artifact{
				OCIRef: "oci://registry.example.com/shop/web@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				// The recorded Registry connects without the Order, so only
				// the Order check can refuse.
				RegistryRef: &corev1.LocalObjectReference{Name: "shop"},
			},
		},
	}
	shop := &deliveryv1alpha1.Registry{Name: "shop", Namespace: nsA, Spec: deliveryv1alpha1.RegistrySpec{URL: "oci://registry.example.com/shop"}}
	order := &deliveryv1alpha1.Order{Name: "web", Namespace: nsA}
	path := "/api/v1/preparations/" + nsA + "/" + prep.Name + "/manifest"

	serve := func(t *testing.T, cluster client.Client) *httptest.ResponseRecorder {
		t.Helper()
		fs := afero.NewMemMapFs()
		return serveAsAdminWith(t, httptest.NewRequest(http.MethodGet, path, nil), testBackend{fs: fs, registry: oci.NewFakeClient(fs), cluster: cluster})
	}

	t.Run("serves the files to a user who may read the Order", func(t *testing.T) {
		requireFiles(t, serve(t, adminCluster(t, prep, shop, order)), fakeManifestFile)
	})

	t.Run("refuses a user who may read the Preparation but not its Order", func(t *testing.T) {
		cluster := fake.NewClientBuilder().WithScheme(unionScheme(t)).WithObjects(prep, shop, order).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if review, ok := obj.(*authorizationv1.SelfSubjectAccessReview); ok {
					review.Status.Allowed = true
					return nil
				}
				return c.Create(ctx, obj, opts...)
			},
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*deliveryv1alpha1.Order); ok {
					return apierrors.NewForbidden(schema.GroupResource{Group: deliveryv1alpha1.GroupVersion.Group, Resource: "orders"}, key.Name,
						errors.New("RBAC refuses it"))
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()

		rec := serve(t, cluster)
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})
}
