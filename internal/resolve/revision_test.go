package resolve_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/resolve"
)

// prometheusKind is the kind of a resource with an image field besides the
// default ones, at prometheusImagePath.
const (
	prometheusKind      = "Prometheus"
	prometheusImagePath = "spec/image"
)

// revisionOf returns the Product revision of product.
func revisionOf(t *testing.T, product *deliveryv1alpha1.Product) string {
	t.Helper()

	revision, err := resolve.ProductRevision(product)
	require.NoError(t, err)

	return revision
}

func TestProductRevision(t *testing.T) {
	product := func() *deliveryv1alpha1.Product {
		return resolvedProduct(helmRender(`{"replicaCount":1}`))
	}
	vendoring := func() *deliveryv1alpha1.Product {
		p := product()
		p.Spec.Images.Policy = deliveryv1alpha1.ImagePolicyVendor
		return p
	}

	base := revisionOf(t, product())
	require.Regexp(t, `^sha256:[a-f0-9]{64}$`, base)
	require.Equal(t, base, revisionOf(t, product()))

	changes := map[string]func(*deliveryv1alpha1.Product){
		"source digest": func(p *deliveryv1alpha1.Product) { p.Status.Source.Digest = "sha256:" + strings.Repeat("2", 64) },
		"render values": func(p *deliveryv1alpha1.Product) { p.Spec.Render = helmRender(`{"replicaCount":2}`) },
		"render type":   func(p *deliveryv1alpha1.Product) { p.Spec.Render = nil },
		"patches": func(p *deliveryv1alpha1.Product) {
			p.Spec.Patches = []deliveryv1alpha1.Patch{{Target: deliveryv1alpha1.PatchTarget{Kind: depKind, Name: appName}, Set: map[string]string{testPath: "2"}}}
		},
		"image policy": func(p *deliveryv1alpha1.Product) { p.Spec.Images.Policy = deliveryv1alpha1.ImagePolicyPin },
	}
	for name, change := range changes {
		t.Run(name+" changes the revision", func(t *testing.T) {
			p := product()
			change(p)
			require.NotEqual(t, base, revisionOf(t, p))
		})
	}

	unchanged := map[string]func(*deliveryv1alpha1.Product){
		"source version": func(p *deliveryv1alpha1.Product) { p.Status.Source.Version = nextVersion },
		"source URL":     func(p *deliveryv1alpha1.Product) { p.Status.Source.URL = hashDest },
		"overrides": func(p *deliveryv1alpha1.Product) {
			p.Spec.Overrides.Values = deliveryv1alpha1.ValueOverrides{Policy: deliveryv1alpha1.OverridePolicyNone}
		},
		"Registries": func(p *deliveryv1alpha1.Product) {
			p.Spec.RegistryRefs = []corev1.LocalObjectReference{{Name: "charts"}}
		},
		"config hash": func(p *deliveryv1alpha1.Product) { p.Status.ConfigHash = "sha256:other" },
	}
	for name, change := range unchanged {
		t.Run(name+" leaves the revision", func(t *testing.T) {
			p := product()
			change(p)
			require.Equal(t, base, revisionOf(t, p))
		})
	}

	t.Run("the vendor location changes the revision under the Vendor policy", func(t *testing.T) {
		moved := vendoring()
		moved.Status.Source.URL = hashDest
		require.NotEqual(t, revisionOf(t, vendoring()), revisionOf(t, moved))
	})

	t.Run("Orders of the Product share it", func(t *testing.T) {
		dev := orderOf(deliveryv1alpha1.OrderSpec{Values: &apiextensionsv1.JSON{Raw: []byte(`{"replicaCount":3}`)}})
		effective, err := resolve.ForProduct(product(), dev)
		require.NoError(t, err)
		require.Equal(t, base, effective.ProductRevision)
	})
}

// TestProductRevision_golden pins the canonical form a Product revision is
// the SHA-256 of. A change here changes the revision, and through the intent
// the config hash, of every Order: each renders a new Preparation. Update
// the golden file (go test -update) only when that is intended.
func TestProductRevision_golden(t *testing.T) {
	product := resolvedProduct(&deliveryv1alpha1.Render{
		Helm: &deliveryv1alpha1.HelmRender{
			ReleaseName: "podinfo",
			Values:      &apiextensionsv1.JSON{Raw: []byte(`{"replicaCount": 1, "ui": {"color": "#34577c"}}`)},
		},
	})
	product.Spec.Patches = []deliveryv1alpha1.Patch{{
		Target: deliveryv1alpha1.PatchTarget{Kind: depKind, Name: "podinfo", Namespace: namespace},
		Set:    map[string]string{".metadata.labels.tier": "web"},
	}}
	product.Spec.Images = deliveryv1alpha1.ProductImages{
		Policy:     deliveryv1alpha1.ImagePolicyVendor,
		FieldSpecs: []deliveryv1alpha1.ImageFieldSpec{{Kind: prometheusKind, Path: prometheusImagePath}},
	}
	product.Spec.Overrides.Values = deliveryv1alpha1.ValueOverrides{Policy: deliveryv1alpha1.OverridePolicyListed, Paths: []string{"replicaCount"}}
	product.Spec.RegistryRefs = []corev1.LocalObjectReference{{Name: "registry-credentials"}}

	canonical, err := resolve.ProductRevisionCanonical(product)
	require.NoError(t, err)

	golden := filepath.Join("testdata", "product_revision.golden.json")
	if *update {
		require.NoError(t, os.WriteFile(golden, []byte(indent(t, canonical)+"\n"), 0o600))
	}

	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	require.Equal(t, indent(t, want), indent(t, canonical))
	require.Equal(t, resolve.CalculateSpecHash(canonical), revisionOf(t, product))
}
