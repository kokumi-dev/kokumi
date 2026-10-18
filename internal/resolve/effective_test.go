package resolve

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	testPath     = ".spec.replicas"
	pausedPath   = ".spec.paused"
	appName      = "app"
	depKind      = "Deployment"
	sourceDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
)

// testProduct returns a resolved Product whose Orders may change anything.
func testProduct(render *deliveryv1alpha1.Render) *deliveryv1alpha1.Product {
	return &deliveryv1alpha1.Product{
		Name: appName,
		Spec: deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{URL: "oci://registry/charts/app", Version: "1.0.0"},
			Render: render,
			Patches: []deliveryv1alpha1.Patch{
				{Target: deliveryv1alpha1.PatchTarget{Kind: depKind, Name: appName}, Set: map[string]string{testPath: "1", pausedPath: "false"}},
			},
			Overrides: deliveryv1alpha1.Overrides{
				Values:  deliveryv1alpha1.ValueOverrides{Policy: deliveryv1alpha1.OverridePolicyAll},
				Patches: deliveryv1alpha1.PatchOverrides{Policy: deliveryv1alpha1.OverridePolicyAll},
			},
		},
		Status: deliveryv1alpha1.ProductStatus{
			ConfigHash: "sha256:product",
			Source: &deliveryv1alpha1.ProductSourceStatus{
				URL:     "oci://mirror/charts/app",
				Version: "1.0.0",
				Digest:  sourceDigest,
			},
		},
	}
}

func helm(values string) *deliveryv1alpha1.Render {
	return &deliveryv1alpha1.Render{Helm: &deliveryv1alpha1.HelmRender{Values: &apiextensionsv1.JSON{Raw: []byte(values)}}}
}

func values(raw string) *apiextensionsv1.JSON {
	return &apiextensionsv1.JSON{Raw: []byte(raw)}
}

func replicas(n string) []deliveryv1alpha1.Patch {
	return []deliveryv1alpha1.Patch{{Target: deliveryv1alpha1.PatchTarget{Kind: depKind, Name: appName}, Set: map[string]string{testPath: n}}}
}

func TestForProduct(t *testing.T) {
	t.Run("renders from the published source with the Product's config", func(t *testing.T) {
		product := testProduct(helm(`{"replicaCount":1}`))

		eff, err := ForProduct(product, &deliveryv1alpha1.Order{})
		require.NoError(t, err)

		require.Equal(t, "oci://mirror/charts/app", eff.Source.URL)
		require.Equal(t, product.Spec.RegistryRefs, eff.RegistryRefs, "the Product's Registries connect to its source")
		require.Empty(t, eff.ImagesVendorURL, "images are vendored only under the Vendor policy")
		require.Equal(t, sourceDigest, eff.SourceDigest, "every variant renders the digest the Product resolved")
		require.JSONEq(t, `{"replicaCount":1}`, string(eff.Render.Helm.Values.Raw))
		require.Equal(t, product.Spec.Patches, eff.Patches)
		require.Equal(t, ProductIdentity{Name: appName, ConfigHash: "sha256:product"}, eff.Product)
	})

	t.Run("deep-merges Order values over the Product's", func(t *testing.T) {
		product := testProduct(helm(`{"replicaCount":1,"ui":{"color":"red","message":"base"}}`))
		order := &deliveryv1alpha1.Order{Spec: deliveryv1alpha1.OrderSpec{Values: values(`{"ui":{"message":"prod"}}`)}}

		eff, err := ForProduct(product, order)
		require.NoError(t, err)
		require.JSONEq(t, `{"replicaCount":1,"ui":{"color":"red","message":"prod"}}`, string(eff.Render.Helm.Values.Raw))

		var unchanged map[string]any
		require.NoError(t, json.Unmarshal(product.Spec.Render.Helm.Values.Raw, &unchanged))
		require.Equal(t, "base", unchanged["ui"].(map[string]any)["message"], "the Product is not modified")
	})

	t.Run("keeps large numbers in merged values as written", func(t *testing.T) {
		product := testProduct(helm(`{"base":9007199254740993}`))
		order := &deliveryv1alpha1.Order{Spec: deliveryv1alpha1.OrderSpec{Values: values(`{"id":9007199254740995}`)}}

		eff, err := ForProduct(product, order)
		require.NoError(t, err)
		require.Contains(t, string(eff.Render.Helm.Values.Raw), `"base":9007199254740993`)
		require.Contains(t, string(eff.Render.Helm.Values.Raw), `"id":9007199254740995`)
	})

	t.Run("rejects values when the Product does not render with Helm", func(t *testing.T) {
		order := &deliveryv1alpha1.Order{Spec: deliveryv1alpha1.OrderSpec{Values: values(`{"replicaCount":2}`)}}

		_, err := ForProduct(testProduct(nil), order)
		require.ErrorContains(t, err, "only allowed when the Product renders with Helm")
	})

	t.Run("merges Order patches over the Product's per target", func(t *testing.T) {
		order := &deliveryv1alpha1.Order{Spec: deliveryv1alpha1.OrderSpec{Patches: replicas("3")}}

		eff, err := ForProduct(testProduct(nil), order)
		require.NoError(t, err)
		require.Equal(t, []deliveryv1alpha1.Patch{
			{Target: deliveryv1alpha1.PatchTarget{Kind: depKind, Name: appName}, Set: map[string]string{testPath: "3", pausedPath: "false"}},
		}, eff.Patches)
	})

	t.Run("passes edits through unmerged", func(t *testing.T) {
		order := &deliveryv1alpha1.Order{Spec: deliveryv1alpha1.OrderSpec{Edits: replicas("4")}}

		eff, err := ForProduct(testProduct(nil), order)
		require.NoError(t, err)
		require.Equal(t, replicas("4"), eff.Edits)
	})

	t.Run("fails while the Product source is not resolved", func(t *testing.T) {
		product := testProduct(nil)
		product.Status.Source = nil

		_, err := ForProduct(product, &deliveryv1alpha1.Order{})
		require.Error(t, err)
	})
}

func TestForProduct_overrides(t *testing.T) {
	withOverrides := func(overrides deliveryv1alpha1.Overrides) *deliveryv1alpha1.Product {
		product := testProduct(helm(`{"replicaCount":1}`))
		product.Spec.Overrides = overrides
		return product
	}
	listed := withOverrides(deliveryv1alpha1.Overrides{
		Values: deliveryv1alpha1.ValueOverrides{
			Policy: deliveryv1alpha1.OverridePolicyListed,
			Paths:  []string{"replicaCount", "ui.message"},
		},
		Patches: deliveryv1alpha1.PatchOverrides{
			Policy: deliveryv1alpha1.OverridePolicyListed,
			Targets: []deliveryv1alpha1.AllowedPatchTarget{
				{Target: deliveryv1alpha1.PatchTarget{Kind: depKind, Name: appName}, Paths: []string{testPath}},
				{Target: deliveryv1alpha1.PatchTarget{Kind: depKind, Name: appName, Namespace: "prod"}, Paths: []string{pausedPath}},
			},
		},
	})
	none := withOverrides(deliveryv1alpha1.Overrides{
		Values:  deliveryv1alpha1.ValueOverrides{Policy: deliveryv1alpha1.OverridePolicyNone},
		Patches: deliveryv1alpha1.PatchOverrides{Policy: deliveryv1alpha1.OverridePolicyNone},
	})
	const paused = "true"
	patch := func(namespace string, set map[string]string) []deliveryv1alpha1.Patch {
		return []deliveryv1alpha1.Patch{{Target: deliveryv1alpha1.PatchTarget{Kind: depKind, Name: appName, Namespace: namespace}, Set: set}}
	}

	tests := []struct {
		name    string
		product *deliveryv1alpha1.Product
		order   deliveryv1alpha1.OrderSpec
		wantErr string
	}{
		{
			name:    "policy All allows any value",
			product: testProduct(helm(`{}`)),
			order:   deliveryv1alpha1.OrderSpec{Values: values(`{"anything":{"goes":true}}`)},
		},
		{
			name:    "policy All allows any patch and edit",
			product: testProduct(nil),
			order: deliveryv1alpha1.OrderSpec{
				Patches: []deliveryv1alpha1.Patch{{Target: deliveryv1alpha1.PatchTarget{Kind: "Service", Name: "svc"}, Set: map[string]string{".spec.type": "NodePort"}}},
				Edits:   []deliveryv1alpha1.Patch{{Target: deliveryv1alpha1.PatchTarget{Kind: "ConfigMap", Name: "cm"}, Set: map[string]string{".data.a": "b"}}},
			},
		},
		{
			name:    "policy None rejects values",
			product: none,
			order:   deliveryv1alpha1.OrderSpec{Values: values(`{"replicaCount":3}`)},
			wantErr: `value path "replicaCount" is not allowed`,
		},
		{
			name:    "policy None rejects patches",
			product: none,
			order:   deliveryv1alpha1.OrderSpec{Patches: replicas("3")},
			wantErr: "patch target Deployment/app is not allowed",
		},
		{
			name:    "policy None rejects edits",
			product: none,
			order:   deliveryv1alpha1.OrderSpec{Edits: replicas("3")},
			wantErr: "edits not allowed",
		},
		{
			name:    "an unset policy allows nothing",
			product: withOverrides(deliveryv1alpha1.Overrides{}),
			order:   deliveryv1alpha1.OrderSpec{Patches: replicas("3")},
			wantErr: "patch target Deployment/app is not allowed",
		},
		{
			name:    "policy None allows an Order that changes nothing",
			product: none,
			order:   deliveryv1alpha1.OrderSpec{},
		},
		{
			name:    "listed value paths are allowed",
			product: listed,
			order:   deliveryv1alpha1.OrderSpec{Values: values(`{"replicaCount":3,"ui":{"message":"hi"}}`)},
		},
		{
			name:    "unlisted value paths are rejected",
			product: listed,
			order:   deliveryv1alpha1.OrderSpec{Values: values(`{"ui":{"color":"blue"}}`)},
			wantErr: `value path "ui.color" is not allowed`,
		},
		{
			name:    "listed patch paths are allowed",
			product: listed,
			order:   deliveryv1alpha1.OrderSpec{Patches: replicas("3"), Edits: replicas("4")},
		},
		{
			name:    "a target without a namespace allows the resource in any namespace",
			product: listed,
			order:   deliveryv1alpha1.OrderSpec{Edits: patch("staging", map[string]string{testPath: "2"})},
		},
		{
			name:    "the paths of all matching targets are allowed",
			product: listed,
			order:   deliveryv1alpha1.OrderSpec{Edits: patch("prod", map[string]string{testPath: "2", pausedPath: paused})},
		},
		{
			name:    "a namespaced target does not allow other namespaces",
			product: listed,
			order:   deliveryv1alpha1.OrderSpec{Edits: patch("staging", map[string]string{pausedPath: paused})},
			wantErr: `patch path ".spec.paused" on staging/Deployment/app is not allowed`,
		},
		{
			name:    "a namespaced target does not allow a patch on every namespace",
			product: listed,
			order:   deliveryv1alpha1.OrderSpec{Patches: patch("", map[string]string{pausedPath: paused})},
			wantErr: `patch path ".spec.paused" on Deployment/app is not allowed`,
		},
		{
			name:    "unlisted edit targets are rejected",
			product: listed,
			order: deliveryv1alpha1.OrderSpec{Edits: []deliveryv1alpha1.Patch{
				{Target: deliveryv1alpha1.PatchTarget{Kind: "Service", Name: appName}, Set: map[string]string{".spec.type": "NodePort"}},
			}},
			wantErr: "edits not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ForProduct(tt.product, &deliveryv1alpha1.Order{Spec: tt.order})
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestProductReady(t *testing.T) {
	ready := func(generation, observed int64, status metav1.ConditionStatus) *deliveryv1alpha1.Product {
		product := testProduct(nil)
		product.Generation = generation
		product.Status.Conditions = []metav1.Condition{{
			Type: deliveryv1alpha1.ConditionTypeReady, Status: status, ObservedGeneration: observed,
		}}
		return product
	}

	require.True(t, ProductReady(ready(2, 2, metav1.ConditionTrue)))
	require.False(t, ProductReady(ready(3, 2, metav1.ConditionTrue)), "a spec change is not resolved yet")
	require.False(t, ProductReady(ready(2, 2, metav1.ConditionFalse)))

	unresolved := ready(2, 2, metav1.ConditionTrue)
	unresolved.Status.Source = nil
	require.False(t, ProductReady(unresolved))
}
