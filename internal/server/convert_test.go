package server

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

func TestStateFromConditions(t *testing.T) {
	ready := func(status metav1.ConditionStatus, reason string) []metav1.Condition {
		return []metav1.Condition{{Type: deliveryv1alpha1.ConditionTypeReady, Status: status, Reason: reason}}
	}

	tests := []struct {
		name       string
		conditions []metav1.Condition
		want       string
	}{
		{name: "no Ready condition", want: "Unknown"},
		{name: "ready", conditions: ready(metav1.ConditionTrue, "Ready"), want: "Ready"},
		{name: "processing", conditions: ready(metav1.ConditionUnknown, "Processing"), want: "Processing"},
		{name: "failed", conditions: ready(metav1.ConditionFalse, "ProcessingFailed"), want: stateFailed},
		{name: "Product not found", conditions: ready(metav1.ConditionFalse, deliveryv1alpha1.ReasonProductNotFound), want: stateWaiting},
		{name: "Product not ready", conditions: ready(metav1.ConditionFalse, deliveryv1alpha1.ReasonProductNotReady), want: stateWaiting},
		{name: "Product deleting", conditions: ready(metav1.ConditionFalse, deliveryv1alpha1.ReasonProductDeleting), want: stateWaiting},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, stateFromConditions(tt.conditions))
		})
	}
}

func TestProductToDTO(t *testing.T) {
	t.Run("a deleted Product waiting for its Orders is deleting", func(t *testing.T) {
		now := metav1.Now()
		product := deliveryv1alpha1.Product{
			Name: "app", DeletionTimestamp: &now,
			Status: deliveryv1alpha1.ProductStatus{Conditions: []metav1.Condition{{
				Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionFalse, Reason: deliveryv1alpha1.ReasonInUse,
			}}},
		}

		dto := productToDTO(product)
		require.Equal(t, stateDeleting, dto.State)
		require.NotNil(t, dto.DeletionTimestamp)
	})

	t.Run("an unset override policy reads as None", func(t *testing.T) {
		dto := productToDTO(deliveryv1alpha1.Product{})
		require.Equal(t, string(deliveryv1alpha1.OverridePolicyNone), dto.Overrides.Values.Policy)
		require.Equal(t, string(deliveryv1alpha1.OverridePolicyNone), dto.Overrides.Patches.Policy)
	})

	t.Run("listed overrides round-trip", func(t *testing.T) {
		overrides := deliveryv1alpha1.Overrides{
			Values: deliveryv1alpha1.ValueOverrides{Policy: deliveryv1alpha1.OverridePolicyListed, Paths: []string{"ui.message"}},
			Patches: deliveryv1alpha1.PatchOverrides{Policy: deliveryv1alpha1.OverridePolicyListed, Targets: []deliveryv1alpha1.AllowedPatchTarget{{
				Target: deliveryv1alpha1.PatchTarget{Kind: "Deployment", Name: "app"}, Paths: []string{".spec.replicas"},
			}}},
		}

		require.Equal(t, overrides, overridesFromDTO(overridesToDTO(overrides)))
	})

	t.Run("an unset image policy reads as Record", func(t *testing.T) {
		dto := productToDTO(deliveryv1alpha1.Product{})
		require.Equal(t, string(deliveryv1alpha1.ImagePolicyRecord), dto.Images.Policy)
	})

	t.Run("image settings round-trip", func(t *testing.T) {
		images := deliveryv1alpha1.ProductImages{
			Policy:        deliveryv1alpha1.ImagePolicyVendor,
			FieldSpecs:    []deliveryv1alpha1.ImageFieldSpec{{Group: "monitoring.coreos.com", Kind: prometheusKind, Path: imagePath}},
			PullSecretRef: &corev1.LocalObjectReference{Name: "registry-credentials"},
		}

		require.Equal(t, images, productImagesFromDTO(productImagesToDTO(images)))
	})
}

func TestPreparationToDTO_images(t *testing.T) {
	images := []deliveryv1alpha1.PreparationImage{{
		Ref:       nginxRef,
		Name:      nginxName,
		Digest:    "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		MediaType: "application/vnd.oci.image.index.v1+json",
		Vendored:  "registry.example.com/shop/app/images/docker.io/library/nginx:1.27@sha256:1111111111111111111111111111111111111111111111111111111111111111",
	}}

	dto := preparationToDTO(deliveryv1alpha1.Preparation{Spec: deliveryv1alpha1.PreparationSpec{Images: images}}, false)

	require.Equal(t, []PreparationImageDTO{{
		Ref:       images[0].Ref,
		Name:      images[0].Name,
		Digest:    images[0].Digest,
		MediaType: images[0].MediaType,
		Vendored:  images[0].Vendored,
	}}, dto.Images)
}

// sortedKeys are the namespace/name pairs the list converters must return,
// in this order, regardless of the order the cache hands them out.
var sortedKeys = [][2]string{
	{"a", "external-dns"},
	{"a", "podinfo"},
	{"a", "podinfo-kustomize"},
	{"b", "external-dns"},
	{"b", "external-secrets"},
}

// shuffledMeta returns ObjectMeta for sortedKeys in a random order, the way
// the controller-runtime cache lists objects.
func shuffledMeta() []metav1.ObjectMeta {
	out := make([]metav1.ObjectMeta, len(sortedKeys))
	for i, k := range sortedKeys {
		out[i] = metav1.ObjectMeta{Namespace: k[0], Name: k[1]}
	}
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func keysOf[T any](items []T, key func(T) [2]string) [][2]string {
	out := make([][2]string, len(items))
	for i, it := range items {
		out[i] = key(it)
	}
	return out
}

func TestListConverters_sortByNamespaceAndName(t *testing.T) {
	for range 20 {
		orders := make([]deliveryv1alpha1.Order, 0, len(sortedKeys))
		servings := make([]deliveryv1alpha1.Serving, 0, len(sortedKeys))
		products := make([]deliveryv1alpha1.Product, 0, len(sortedKeys))
		var pantries deliveryv1alpha1.PantryList
		for _, m := range shuffledMeta() {
			orders = append(orders, deliveryv1alpha1.Order{ObjectMeta: m})
			servings = append(servings, deliveryv1alpha1.Serving{ObjectMeta: m})
			products = append(products, deliveryv1alpha1.Product{ObjectMeta: m})
			pantries.Items = append(pantries.Items, deliveryv1alpha1.Pantry{ObjectMeta: m})
		}

		assert.Equal(t, sortedKeys, keysOf(enrichOrders(orders, nil), func(d OrderDTO) [2]string { return [2]string{d.Namespace, d.Name} }))
		assert.Equal(t, sortedKeys, keysOf(servingsToDTO(servings), func(d ServingDTO) [2]string { return [2]string{d.Namespace, d.Name} }))
		assert.Equal(t, sortedKeys, keysOf(productsToDTO(products), func(d ProductDTO) [2]string { return [2]string{d.Namespace, d.Name} }))
		assert.Equal(t, sortedKeys, keysOf(pantriesFromList(pantries), func(d PantryDTO) [2]string { return [2]string{d.Namespace, d.Name} }))
	}
}

func TestEnrichPreparations_newestFirstThenByKey(t *testing.T) {
	older := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := metav1.NewTime(older.Add(time.Hour))
	newest := sortedKeys[2]
	unset := [2]string{"a", "unset"}

	// Creation timestamps have second precision, so Preparations created in
	// the same second must still come in a fixed order.
	want := [][2]string{newest}
	for _, k := range sortedKeys {
		if k != newest {
			want = append(want, k)
		}
	}
	want = append(want, unset)

	for range 20 {
		preps := make([]deliveryv1alpha1.Preparation, 0, len(sortedKeys)+1)
		for _, m := range shuffledMeta() {
			m.CreationTimestamp = older
			if m.Namespace == newest[0] && m.Name == newest[1] {
				m.CreationTimestamp = newer
			}
			preps = append(preps, deliveryv1alpha1.Preparation{ObjectMeta: m})
		}
		preps = append(preps, deliveryv1alpha1.Preparation{Namespace: unset[0], Name: unset[1]})
		rand.Shuffle(len(preps), func(i, j int) { preps[i], preps[j] = preps[j], preps[i] })

		assert.Equal(t, want, keysOf(enrichPreparations(preps, nil), func(d PreparationDTO) [2]string { return [2]string{d.Namespace, d.Name} }))
	}
}
