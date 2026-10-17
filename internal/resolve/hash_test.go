package resolve_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/resolve"
)

var update = flag.Bool("update", false, "update golden files")

const (
	hashSource  = "oci://ghcr.io/org/charts/app"
	hashDest    = "oci://registry.kokumi.svc.cluster.local:5000/ns/app"
	hashVersion = "1.0.0"
	nextVersion = "1.0.1"
	hashDigest  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	namespace   = "shop"
)

// resolvedProduct returns a Product whose source is resolved to hashSource
// at hashVersion and hashDigest, rendered with render. Its Orders may change
// anything.
func resolvedProduct(render *deliveryv1alpha1.Render) *deliveryv1alpha1.Product {
	return &deliveryv1alpha1.Product{
		Name: "app",
		Spec: deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{OCI: hashSource, Version: hashVersion},
			Render: render,
			Overrides: deliveryv1alpha1.Overrides{
				Values:  deliveryv1alpha1.ValueOverrides{Policy: deliveryv1alpha1.OverridePolicyAll},
				Patches: deliveryv1alpha1.PatchOverrides{Policy: deliveryv1alpha1.OverridePolicyAll},
			},
		},
		Status: deliveryv1alpha1.ProductStatus{
			ConfigHash: "sha256:product",
			Source:     &deliveryv1alpha1.ProductSourceStatus{OCI: hashSource, Version: hashVersion, Digest: hashDigest},
		},
	}
}

// orderOf returns an Order of the Product "app".
func orderOf(spec deliveryv1alpha1.OrderSpec) *deliveryv1alpha1.Order {
	spec.ProductRef = deliveryv1alpha1.ProductRef{Name: "app"}
	return &deliveryv1alpha1.Order{Spec: spec}
}

// hashOf returns the config hash of order rendered from product.
func hashOf(t *testing.T, product *deliveryv1alpha1.Product, order *deliveryv1alpha1.Order, source, dest string) string {
	t.Helper()

	return resolve.CalculateSpecHash(canonicalOf(t, product, order, source, dest))
}

// canonicalOf returns the canonical intent of order rendered from product.
func canonicalOf(t *testing.T, product *deliveryv1alpha1.Product, order *deliveryv1alpha1.Order, source, dest string) []byte {
	t.Helper()

	effective, err := resolve.ForProduct(product, order)
	require.NoError(t, err)

	canonical, err := resolve.NewIntent(order, effective, source, dest).Canonical()
	require.NoError(t, err)

	return canonical
}

func helmRender(values string) *deliveryv1alpha1.Render {
	return &deliveryv1alpha1.Render{
		Helm: &deliveryv1alpha1.HelmRender{Values: &apiextensionsv1.JSON{Raw: []byte(values)}},
	}
}

func TestCalculateSpecHash(t *testing.T) {
	product := func() *deliveryv1alpha1.Product {
		return resolvedProduct(helmRender(`{"replicaCount":2,"image":{"tag":"1.0.0"}}`))
	}
	order := func() *deliveryv1alpha1.Order { return orderOf(deliveryv1alpha1.OrderSpec{}) }

	base := hashOf(t, product(), order(), hashSource, hashDest)
	require.Regexp(t, `^sha256:[a-f0-9]{64}$`, base)

	t.Run("same inputs produce same hash", func(t *testing.T) {
		require.Equal(t, base, hashOf(t, product(), order(), hashSource, hashDest))
	})

	t.Run("source URL change produces different hash", func(t *testing.T) {
		require.NotEqual(t, base, hashOf(t, product(), order(), hashSource+"-v2", hashDest))
	})

	t.Run("dest URL change produces different hash", func(t *testing.T) {
		require.NotEqual(t, base, hashOf(t, product(), order(), hashSource, hashDest+"-v2"))
	})

	t.Run("version change produces different hash", func(t *testing.T) {
		p := product()
		p.Status.Source.Version = nextVersion
		require.NotEqual(t, base, hashOf(t, p, order(), hashSource, hashDest))
	})

	t.Run("pantryRef name is not hashed", func(t *testing.T) {
		p := product()
		p.Status.Source.PantryRef = &deliveryv1alpha1.PantryRef{Name: "other-charts"}
		require.Equal(t, base, hashOf(t, p, order(), hashSource, hashDest))
	})

	t.Run("key order of inline values is not hashed", func(t *testing.T) {
		p := product()
		p.Spec.Render = helmRender(`{"image": {"tag": "1.0.0"}, "replicaCount": 2}`)
		require.Equal(t, base, hashOf(t, p, order(), hashSource, hashDest))
	})

	t.Run("large numbers in inline values are kept as written", func(t *testing.T) {
		p := product()
		p.Spec.Render = helmRender(`{"id":9007199254740993,"note":"<a&b>"}`)
		neighbour := product()
		neighbour.Spec.Render = helmRender(`{"id":9007199254740992,"note":"<a&b>"}`)
		require.NotEqual(t, hashOf(t, p, order(), hashSource, hashDest), hashOf(t, neighbour, order(), hashSource, hashDest))

		canonical := canonicalOf(t, p, order(), hashSource, hashDest)
		require.Contains(t, string(canonical), `"id":9007199254740993`)
		require.Contains(t, string(canonical), `"note":"<a&b>"`)
	})

	t.Run("promotion mode is not hashed", func(t *testing.T) {
		o := order()
		o.Spec.Promotion = &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeAutomatic}
		require.Equal(t, base, hashOf(t, product(), o, hashSource, hashDest))
	})

	t.Run("the default image policy is not hashed", func(t *testing.T) {
		p := product()
		p.Spec.Images = deliveryv1alpha1.ProductImages{
			Policy:        deliveryv1alpha1.ImagePolicyRecord,
			PullSecretRef: &corev1.LocalObjectReference{Name: "registry-credentials"},
		}
		require.Equal(t, base, hashOf(t, p, order(), hashSource, hashDest))
	})

	t.Run("image policy and field specs produce different hashes", func(t *testing.T) {
		pin := product()
		pin.Spec.Images.Policy = deliveryv1alpha1.ImagePolicyPin
		vendor := product()
		vendor.Spec.Images.Policy = deliveryv1alpha1.ImagePolicyVendor
		specs := product()
		specs.Spec.Images.FieldSpecs = []deliveryv1alpha1.ImageFieldSpec{{Kind: "Prometheus", Path: "spec/image"}}

		hashes := []string{base, hashOf(t, pin, order(), hashSource, hashDest), hashOf(t, vendor, order(), hashSource, hashDest), hashOf(t, specs, order(), hashSource, hashDest)}
		require.Len(t, slices.Compact(slices.Sorted(slices.Values(hashes))), 4)
	})

	t.Run("approval policy change produces different hash", func(t *testing.T) {
		o := order()
		o.Spec.Promotion = &deliveryv1alpha1.PromotionSpec{
			Approvals: &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 1},
		}
		one := hashOf(t, product(), o, hashSource, hashDest)
		require.NotEqual(t, base, one)

		o.Spec.Promotion.Approvals.RequiredApprovals = 2
		require.NotEqual(t, one, hashOf(t, product(), o, hashSource, hashDest))
	})
}

func TestCalculateSpecHash_product(t *testing.T) {
	product := func() *deliveryv1alpha1.Product {
		return resolvedProduct(helmRender(`{"replicaCount":1}`))
	}
	order := orderOf(deliveryv1alpha1.OrderSpec{
		Values: &apiextensionsv1.JSON{Raw: []byte(`{"image":{"tag":"1.0.0"}}`)},
	})

	base := hashOf(t, product(), order, hashSource, hashDest)

	t.Run("product version change produces different hash", func(t *testing.T) {
		p := product()
		p.Status.Source.Version = nextVersion
		require.NotEqual(t, base, hashOf(t, p, order, hashSource, hashDest))
	})

	t.Run("product digest change produces different hash", func(t *testing.T) {
		p := product()
		p.Status.Source.Digest = "sha256:" + strings.Repeat("2", 64)
		require.NotEqual(t, base, hashOf(t, p, order, hashSource, hashDest))
	})

	t.Run("product values change produces different hash", func(t *testing.T) {
		p := product()
		p.Spec.Render = helmRender(`{"replicaCount":3}`)
		require.NotEqual(t, base, hashOf(t, p, order, hashSource, hashDest))
	})

	t.Run("order values change produces different hash", func(t *testing.T) {
		o := order.DeepCopy()
		o.Spec.Values = &apiextensionsv1.JSON{Raw: []byte(`{"image":{"tag":"1.0.1"}}`)}
		require.NotEqual(t, base, hashOf(t, product(), o, hashSource, hashDest))
	})

	t.Run("product change that leaves the merge untouched is not hashed", func(t *testing.T) {
		p := product()
		p.Status.ConfigHash = "sha256:other"
		p.Spec.Overrides.Values = deliveryv1alpha1.ValueOverrides{Policy: deliveryv1alpha1.OverridePolicyListed, Paths: []string{"image.tag"}}
		require.Equal(t, base, hashOf(t, p, order, hashSource, hashDest))
	})

	t.Run("where a value is set is not hashed, only the merged result", func(t *testing.T) {
		merged := resolvedProduct(helmRender(`{"image":{"tag":"1.0.0"},"replicaCount":1}`))
		require.Equal(t, base, hashOf(t, merged, orderOf(deliveryv1alpha1.OrderSpec{}), hashSource, hashDest))
	})
}

func TestCalculateProductHash(t *testing.T) {
	spec := func() deliveryv1alpha1.ProductSpec {
		return deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{PantryRef: &deliveryv1alpha1.PantryRef{Name: "charts"}, Version: hashVersion},
			Render: helmRender(`{"replicaCount":1}`),
		}
	}
	hash := func(t *testing.T, spec deliveryv1alpha1.ProductSpec, sourceOCI, vendorOCI string) string {
		t.Helper()
		h, err := resolve.CalculateProductHash(spec.Source, spec.Vendor, sourceOCI, vendorOCI)
		require.NoError(t, err)
		return h
	}

	base := hash(t, spec(), hashSource, "")
	require.Regexp(t, `^sha256:[a-f0-9]{64}$`, base)
	require.Equal(t, base, hash(t, spec(), hashSource, ""))

	t.Run("source change produces different hash", func(t *testing.T) {
		s := spec()
		s.Source.Version = nextVersion
		require.NotEqual(t, base, hash(t, s, hashSource, ""))
	})

	t.Run("vendor change produces different hash", func(t *testing.T) {
		s := spec()
		s.Vendor = &deliveryv1alpha1.OCIDestination{}
		require.NotEqual(t, base, hash(t, s, hashSource, ""))
	})

	t.Run("resolved source URL change produces different hash", func(t *testing.T) {
		require.NotEqual(t, base, hash(t, spec(), hashSource+"-v2", ""))
	})

	t.Run("resolved vendor URL change produces different hash", func(t *testing.T) {
		s := spec()
		s.Vendor = &deliveryv1alpha1.OCIDestination{PantryRef: &deliveryv1alpha1.PantryRef{Name: "mirror"}}
		require.NotEqual(t, hash(t, s, hashSource, hashDest), hash(t, s, hashSource, hashDest+"-v2"))
	})

	t.Run("render, patches, and overrides do not change the hash", func(t *testing.T) {
		s := spec()
		s.Render = helmRender(`{"replicaCount":2}`)
		s.Patches = []deliveryv1alpha1.Patch{{Target: deliveryv1alpha1.PatchTarget{Kind: "Service", Name: "svc"}, Set: map[string]string{".spec.type": "NodePort"}}}
		s.Overrides.Values.Policy = deliveryv1alpha1.OverridePolicyAll
		require.Equal(t, base, hash(t, s, hashSource, ""))
	})
}

// indent returns data with its whitespace normalized, so that a golden file
// an editor has reformatted still compares equal. Key order, numbers and
// string escapes are compared as written.
func indent(t *testing.T, data []byte) string {
	t.Helper()

	var out bytes.Buffer
	require.NoError(t, json.Indent(&out, bytes.TrimSpace(data), "", "  "))

	return out.String()
}

// TestIntent_golden pins the canonical form of a fully populated intent,
// merged from a Product and an Order. The config hash is the SHA-256 of these
// bytes, so a change here renders a new Preparation for every Order and voids
// its approvals. Update the golden file (go test -update) only when that is
// intended. The file is indented for reading; the canonical form is the same
// JSON without whitespace.
func TestIntent_golden(t *testing.T) {
	const name = "podinfo"

	product := resolvedProduct(&deliveryv1alpha1.Render{
		Helm: &deliveryv1alpha1.HelmRender{
			ReleaseName: name,
			Namespace:   namespace,
			IncludeCRDs: true,
			Values:      &apiextensionsv1.JSON{Raw: []byte(`{"replicaCount": 1, "image": {"tag": "1.0.0"}, "args": "a && <b>"}`)},
		},
	})
	product.Spec.Patches = []deliveryv1alpha1.Patch{{
		Target: deliveryv1alpha1.PatchTarget{Kind: "Deployment", Name: name, Namespace: namespace},
		Set:    map[string]string{".spec.replicas": "1", ".metadata.labels.tier": "web"},
	}}
	product.Spec.Images = deliveryv1alpha1.ProductImages{
		Policy:        deliveryv1alpha1.ImagePolicyVendor,
		FieldSpecs:    []deliveryv1alpha1.ImageFieldSpec{{Group: "monitoring.coreos.com", Kind: "Prometheus", Path: "spec/image"}},
		PullSecretRef: &corev1.LocalObjectReference{Name: "registry-credentials"},
	}

	order := orderOf(deliveryv1alpha1.OrderSpec{
		Values: &apiextensionsv1.JSON{Raw: []byte(`{"replicaCount": 2, "id": 9007199254740993}`)},
		Patches: []deliveryv1alpha1.Patch{{
			Target: deliveryv1alpha1.PatchTarget{Kind: "Deployment", Name: name, Namespace: namespace},
			Set:    map[string]string{".spec.replicas": "3"},
		}},
		Edits: []deliveryv1alpha1.Patch{{
			Target: deliveryv1alpha1.PatchTarget{Kind: "Service", Name: name},
			Set:    map[string]string{".spec.type": "ClusterIP"},
		}},
		Promotion: &deliveryv1alpha1.PromotionSpec{
			Mode:      deliveryv1alpha1.PromotionModeManual,
			Approvals: &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 2, AllowedGroups: []string{"release", "security"}},
		},
	})

	canonical := canonicalOf(t, product, order, hashSource, hashDest)

	golden := filepath.Join("testdata", "intent.golden.json")
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o750))
		require.NoError(t, os.WriteFile(golden, []byte(indent(t, canonical)+"\n"), 0o600))
	}

	want, err := os.ReadFile(golden)
	require.NoError(t, err)

	// A readable diff first, then the exact bytes that are hashed: the golden
	// file without its whitespace.
	require.Equal(t, indent(t, want), indent(t, canonical))

	var compact bytes.Buffer
	require.NoError(t, json.Compact(&compact, want))
	require.Equal(t, compact.String(), string(canonical), "the canonical form holds no insignificant whitespace")
}
