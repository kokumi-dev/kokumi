package server

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/resolve"
)

func TestPreviewRequest(t *testing.T) {
	helm := func(values string) *deliveryv1alpha1.Render {
		return &deliveryv1alpha1.Render{Helm: &deliveryv1alpha1.HelmRender{Values: &apiextensionsv1.JSON{Raw: []byte(values)}}}
	}
	source := deliveryv1alpha1.OCISource{OCI: "oci://registry/charts/app", Version: "1.0.0"}

	t.Run("rejects Helm values that are not an object", func(t *testing.T) {
		p := &preview{spec: &resolve.EffectiveSpec{Source: source, Render: helm(`[1,2]`)}, source: source}

		_, err := p.request()
		require.ErrorContains(t, err, "invalid render config")
	})

	t.Run("pulls the digest the Product resolved", func(t *testing.T) {
		const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		p := &preview{spec: &resolve.EffectiveSpec{Source: source, SourceDigest: digest, Render: helm(`{"replicaCount":2}`)}, source: source}

		req, err := p.request()
		require.NoError(t, err)
		require.Equal(t, digest, req.Source.Digest)
		require.Equal(t, map[string]any{"replicaCount": float64(2)}, req.Render.Helm.Values)
	})

	t.Run("passes the image policy, mirrors and destination", func(t *testing.T) {
		p := &preview{
			spec: &resolve.EffectiveSpec{Source: source, Images: deliveryv1alpha1.ProductImages{
				Policy:     deliveryv1alpha1.ImagePolicyVendor,
				FieldSpecs: []deliveryv1alpha1.ImageFieldSpec{{Kind: prometheusKind, Path: imagePath}},
			}},
			source:      source,
			mirrors:     []images.Mirror{{Prefix: dockerHub, Location: "mirror.example.com/hub"}},
			destination: "oci://registry/shop/app",
			locked:      []images.Image{{Ref: nginxRef, Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		}

		req, err := p.request()
		require.NoError(t, err)
		require.Equal(t, images.PolicyVendor, req.Images.Policy)
		require.Equal(t, []images.FieldSpec{{Kind: prometheusKind, Path: imagePath}}, req.Images.FieldSpecs)
		require.Equal(t, p.mirrors, req.Images.Mirrors)
		require.Equal(t, p.locked, req.Images.Locked)
		require.Equal(t, "oci://registry/shop/app", req.Destination)
	})
}

func TestLockedImagesAs(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	order := newOrder("web", nsA)
	order.Status.LatestPreparationName = "web-aaaaaaaaaaaa"
	prep := &deliveryv1alpha1.Preparation{
		Name:      "web-aaaaaaaaaaaa",
		Namespace: nsA,
		Spec: deliveryv1alpha1.PreparationSpec{Images: []deliveryv1alpha1.PreparationImage{
			{Ref: nginxRef, Name: nginxName, Digest: digest, Vendored: "registry/shop/web@" + digest},
		}},
	}
	c := fake.NewClientBuilder().WithScheme(unionScheme(t)).WithObjects(order, prep).Build()
	uc := &userClient{imp: &impersonator{clients: map[string]client.Client{saEditor: c}}, sas: []corev1.ServiceAccount{newSA(saEditor)}}
	deps := &apiDeps{logger: logr.Discard()}
	pin := deliveryv1alpha1.ProductImages{Policy: deliveryv1alpha1.ImagePolicyPin}
	ctx := context.Background()

	require.Equal(t, []images.Image{
		{Ref: nginxRef, Name: nginxName, Digest: digest, Vendored: "registry/shop/web@" + digest},
	}, lockedImagesAs(ctx, deps, uc, pin, nsA, "web"))
	require.Nil(t, lockedImagesAs(ctx, deps, uc, deliveryv1alpha1.ProductImages{}, nsA, "web"), "Record locks nothing")
	require.Nil(t, lockedImagesAs(ctx, deps, uc, pin, nsA, "new"), "a new Order locks nothing")
}
