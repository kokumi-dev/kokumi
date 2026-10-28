package server

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/resolve"
)

// Names the conversion tests use.
const (
	testProduct       = "app"
	testSourceVersion = "1.0.0"
	testStage         = "staging"
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
			Name: testProduct, DeletionTimestamp: &now,
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

	t.Run("a resolved Product has the revision its Orders render", func(t *testing.T) {
		product := deliveryv1alpha1.Product{Name: testProduct, Status: deliveryv1alpha1.ProductStatus{
			Source: &deliveryv1alpha1.ProductSourceStatus{URL: "oci://ghcr.io/org/app", Version: testSourceVersion, Digest: "sha256:" + strings.Repeat("1", 64)},
		}}
		revision, err := resolve.ProductRevision(&product)
		require.NoError(t, err)
		require.Equal(t, revision, productToDTO(product).Revision)
		require.Empty(t, productToDTO(deliveryv1alpha1.Product{}).Revision, "an unresolved Product has none")
	})

	t.Run("an unset image policy reads as Record", func(t *testing.T) {
		dto := productToDTO(deliveryv1alpha1.Product{})
		require.Equal(t, string(deliveryv1alpha1.ImagePolicyRecord), dto.Images.Policy)
	})

	t.Run("image settings round-trip", func(t *testing.T) {
		images := deliveryv1alpha1.ProductImages{
			Policy:     deliveryv1alpha1.ImagePolicyVendor,
			FieldSpecs: []deliveryv1alpha1.ImageFieldSpec{{Group: "monitoring.coreos.com", Kind: prometheusKind, Path: imagePath}},
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

	dto := preparationToDTO(deliveryv1alpha1.Preparation{
		Labels: map[string]string{deliveryv1alpha1.LabelVersion: "6.14.1"},
		Spec:   deliveryv1alpha1.PreparationSpec{Images: images},
	}, false)
	assert.Equal(t, "6.14.1", dto.Version, "the version of the source, from its label")

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
		registries := make([]deliveryv1alpha1.Registry, 0, len(sortedKeys))
		repositories := make([]deliveryv1alpha1.Repository, 0, len(sortedKeys))
		for _, m := range shuffledMeta() {
			orders = append(orders, deliveryv1alpha1.Order{ObjectMeta: m})
			servings = append(servings, deliveryv1alpha1.Serving{ObjectMeta: m})
			products = append(products, deliveryv1alpha1.Product{ObjectMeta: m})
			registries = append(registries, deliveryv1alpha1.Registry{ObjectMeta: m})
			repositories = append(repositories, deliveryv1alpha1.Repository{ObjectMeta: m})
		}

		assert.Equal(t, sortedKeys, keysOf(enrichOrders(orders, nil, nil), func(d OrderDTO) [2]string { return [2]string{d.Namespace, d.Name} }))
		assert.Equal(t, sortedKeys, keysOf(servingsToDTO(servings), func(d ServingDTO) [2]string { return [2]string{d.Namespace, d.Name} }))
		assert.Equal(t, sortedKeys, keysOf(productsToDTO(products), func(d ProductDTO) [2]string { return [2]string{d.Namespace, d.Name} }))
		assert.Equal(t, sortedKeys, keysOf(registriesToDTO(registries), func(d RegistryDTO) [2]string { return [2]string{d.Namespace, d.Name} }))
		assert.Equal(t, sortedKeys, keysOf(repositoriesToDTO(repositories), func(d RepositoryDTO) [2]string { return [2]string{d.Namespace, d.Name} }))
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

func TestServingToDTO_history(t *testing.T) {
	digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	started := metav1.NewTime(time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC))
	healthy := metav1.NewTime(started.Add(time.Minute))
	serving := deliveryv1alpha1.Serving{
		Name: prod, Namespace: "shop",
		Spec: deliveryv1alpha1.ServingSpec{OrderName: prod},
		Status: deliveryv1alpha1.ServingStatus{History: []deliveryv1alpha1.DeploymentEntry{
			{PreparationName: "prod-2", Digest: digest("2"), StartedTime: started},
			{PreparationName: "prod-1", Digest: digest("1"), ProductRevision: digest("7"), StartedTime: started, HealthyTime: &healthy,
				PromotionRecord: digest("a"), DeploymentRecord: digest("b")},
		}},
	}

	dto := servingToDTO(serving)
	assert.Equal(t, "prod-1", dto.DeployedPreparation, "a promotion that is being recorded deploys nothing yet")
	assert.Equal(t, digest("1"), dto.DeployedDigest)
	require.Len(t, dto.History, 2)
	assert.Equal(t, DeploymentEntryDTO{
		Preparation: "prod-1", Digest: digest("1"), ProductRevision: digest("7"), StartedAt: started.Time,
		HealthyAt: &healthy.Time, PromotionRecord: digest("a"), DeploymentRecord: digest("b"),
	}, dto.History[1])

	assert.Equal(t, "prod-1", activePreparationFor("shop", "prod", []deliveryv1alpha1.Serving{serving}))
	assert.Empty(t, servingToDTO(deliveryv1alpha1.Serving{}).DeployedPreparation)
}

func TestOrderToDTO_upstreamStages(t *testing.T) {
	stages := []deliveryv1alpha1.UpstreamStage{{Order: testStage, SoakSeconds: 3600}}
	order := deliveryv1alpha1.Order{Spec: deliveryv1alpha1.OrderSpec{Promotion: &deliveryv1alpha1.PromotionSpec{After: stages}}}

	dto := orderToDTO(order, "", nil)
	require.Equal(t, []UpstreamStageDTO{{Order: testStage, SoakSeconds: 3600}}, dto.After)
	require.Equal(t, stages, upstreamStagesFromDTO(dto.After))
	require.Nil(t, upstreamStagesFromDTO(nil))
}

func TestPreparationToDTO_gates(t *testing.T) {
	revision := "sha256:" + strings.Repeat("7", 64)
	prep := deliveryv1alpha1.Preparation{Spec: deliveryv1alpha1.PreparationSpec{
		ProductRevision: revision,
		Promotion: &deliveryv1alpha1.PromotionGates{
			After:  []deliveryv1alpha1.UpstreamStage{{Order: testStage}},
			Checks: &deliveryv1alpha1.PromotionChecks{Vulnerabilities: &deliveryv1alpha1.VulnerabilityCheck{MaxSeverity: deliveryv1alpha1.SeverityHigh}},
		},
	}}

	dto := preparationToDTO(prep, false)
	assert.Equal(t, revision, dto.ProductRevision)
	assert.Equal(t, []UpstreamStageDTO{{Order: testStage}}, dto.After)
	assert.Equal(t, "High", dto.Checks.Vulnerabilities.MaxSeverity)
	assert.Nil(t, dto.Approval, "no approval policy applies")
}

func TestEffectiveTargetNamespace(t *testing.T) {
	const (
		orderNamespace   = "store"
		productNamespace = "storefront"
		orderOverride    = "checkout"
	)
	order := &deliveryv1alpha1.Order{Name: orderOverride, Namespace: orderNamespace}
	product := &deliveryv1alpha1.Product{Spec: deliveryv1alpha1.ProductSpec{TargetNamespace: productNamespace}}

	namespace, source := effectiveTargetNamespace(order, nil)
	assert.Equal(t, [2]string{orderNamespace, targetNamespaceFromNamespace}, [2]string{namespace, source}, "without a Product, the Order's own")
	namespace, source = effectiveTargetNamespace(order, product)
	assert.Equal(t, [2]string{productNamespace, targetNamespaceFromProduct}, [2]string{namespace, source})
	order.Spec.TargetNamespace = orderOverride
	namespace, source = effectiveTargetNamespace(order, product)
	assert.Equal(t, [2]string{orderOverride, targetNamespaceFromOrder}, [2]string{namespace, source})
}
