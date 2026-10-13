package resolve_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/resolve"
)

var update = flag.Bool("update", false, "update golden files")

const (
	hashSource  = "oci://ghcr.io/org/charts/app"
	hashDest    = "oci://registry.kokumi.svc.cluster.local:5000/ns/app"
	hashVersion = "1.0.0"
)

// hashOf returns the config hash of a standalone order.
func hashOf(t *testing.T, order *deliveryv1alpha1.Order, source, dest string) string {
	t.Helper()

	effective, err := resolve.FromOrder(order)
	require.NoError(t, err)

	canonical, err := resolve.NewIntent(order, effective, source, dest).Canonical()
	require.NoError(t, err)

	return resolve.CalculateSpecHash(canonical)
}

// menuHashOf returns the config hash of an order rendered from menu.
func menuHashOf(t *testing.T, menu *deliveryv1alpha1.Menu, order *deliveryv1alpha1.Order) string {
	t.Helper()

	effective, err := resolve.ForMenu(menu, order)
	require.NoError(t, err)

	canonical, err := resolve.NewIntent(order, effective, effective.Source.OCI, hashDest).Canonical()
	require.NoError(t, err)

	return resolve.CalculateSpecHash(canonical)
}

func helmRender(values string) *deliveryv1alpha1.Render {
	return &deliveryv1alpha1.Render{
		Helm: &deliveryv1alpha1.HelmRender{Values: &apiextensionsv1.JSON{Raw: []byte(values)}},
	}
}

func TestCalculateSpecHash(t *testing.T) {
	order := func() *deliveryv1alpha1.Order {
		return &deliveryv1alpha1.Order{
			Spec: deliveryv1alpha1.OrderSpec{
				Source: &deliveryv1alpha1.OCISource{
					PantryRef: &deliveryv1alpha1.PantryRef{Name: "charts"},
					Version:   hashVersion,
				},
				Render: helmRender(`{"replicaCount":2,"image":{"tag":"1.0.0"}}`),
			},
		}
	}

	base := hashOf(t, order(), hashSource, hashDest)
	require.Regexp(t, `^sha256:[a-f0-9]{64}$`, base)

	t.Run("same inputs produce same hash", func(t *testing.T) {
		require.Equal(t, base, hashOf(t, order(), hashSource, hashDest))
	})

	t.Run("source URL change produces different hash", func(t *testing.T) {
		require.NotEqual(t, base, hashOf(t, order(), hashSource+"-v2", hashDest))
	})

	t.Run("dest URL change produces different hash", func(t *testing.T) {
		require.NotEqual(t, base, hashOf(t, order(), hashSource, hashDest+"-v2"))
	})

	t.Run("version change produces different hash", func(t *testing.T) {
		o := order()
		o.Spec.Source.Version = "1.0.1"
		require.NotEqual(t, base, hashOf(t, o, hashSource, hashDest))
	})

	t.Run("pantryRef name is not hashed", func(t *testing.T) {
		o := order()
		o.Spec.Source.PantryRef.Name = "other-charts"
		require.Equal(t, base, hashOf(t, o, hashSource, hashDest))
	})

	t.Run("key order of inline values is not hashed", func(t *testing.T) {
		o := order()
		o.Spec.Render = helmRender(`{"image": {"tag": "1.0.0"}, "replicaCount": 2}`)
		require.Equal(t, base, hashOf(t, o, hashSource, hashDest))
	})

	t.Run("large numbers in inline values are kept as written", func(t *testing.T) {
		o := order()
		o.Spec.Render = helmRender(`{"id":9007199254740993,"note":"<a&b>"}`)
		neighbour := order()
		neighbour.Spec.Render = helmRender(`{"id":9007199254740992,"note":"<a&b>"}`)
		require.NotEqual(t, hashOf(t, o, hashSource, hashDest), hashOf(t, neighbour, hashSource, hashDest))

		effective, err := resolve.FromOrder(o)
		require.NoError(t, err)
		canonical, err := resolve.NewIntent(o, effective, hashSource, hashDest).Canonical()
		require.NoError(t, err)
		require.Contains(t, string(canonical), `"id":9007199254740993`)
		require.Contains(t, string(canonical), `"note":"<a&b>"`)
	})

	t.Run("promotion mode is not hashed", func(t *testing.T) {
		o := order()
		o.Spec.Promotion = &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeAutomatic}
		require.Equal(t, base, hashOf(t, o, hashSource, hashDest))
	})

	t.Run("approval policy change produces different hash", func(t *testing.T) {
		o := order()
		o.Spec.Promotion = &deliveryv1alpha1.PromotionSpec{
			Approvals: &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 1},
		}
		one := hashOf(t, o, hashSource, hashDest)
		require.NotEqual(t, base, one)

		o.Spec.Promotion.Approvals.RequiredApprovals = 2
		require.NotEqual(t, one, hashOf(t, o, hashSource, hashDest))
	})
}

func TestCalculateSpecHash_menu(t *testing.T) {
	menu := func() *deliveryv1alpha1.Menu {
		return &deliveryv1alpha1.Menu{
			Spec: deliveryv1alpha1.MenuSpec{
				Render: helmRender(`{"replicaCount":1}`),
				Overrides: deliveryv1alpha1.OverridePolicy{
					Values:  deliveryv1alpha1.ValueOverridePolicy{Policy: deliveryv1alpha1.OverridePolicyAll},
					Patches: deliveryv1alpha1.PatchOverridePolicy{Policy: deliveryv1alpha1.OverridePolicyAll},
				},
			},
			Status: deliveryv1alpha1.MenuStatus{
				ConfigHash: "sha256:menu",
				Source:     &deliveryv1alpha1.MenuSourceStatus{OCI: hashSource, Version: hashVersion},
			},
		}
	}
	order := &deliveryv1alpha1.Order{
		Spec: deliveryv1alpha1.OrderSpec{
			MenuRef: &deliveryv1alpha1.MenuRef{Name: "app"},
			Render:  helmRender(`{"image":{"tag":"1.0.0"}}`),
		},
	}

	base := menuHashOf(t, menu(), order)

	t.Run("menu version change produces different hash", func(t *testing.T) {
		m := menu()
		m.Status.Source.Version = "1.0.1"
		require.NotEqual(t, base, menuHashOf(t, m, order))
	})

	t.Run("menu values change produces different hash", func(t *testing.T) {
		m := menu()
		m.Spec.Render = helmRender(`{"replicaCount":3}`)
		require.NotEqual(t, base, menuHashOf(t, m, order))
	})

	t.Run("menu change that leaves the merge untouched is not hashed", func(t *testing.T) {
		m := menu()
		m.Status.ConfigHash = "sha256:other"
		m.Spec.Overrides.Patches.Policy = deliveryv1alpha1.OverridePolicyNone
		require.Equal(t, base, menuHashOf(t, m, order))
	})

	t.Run("pre-rendered menu publishing a new artifact produces different hash", func(t *testing.T) {
		preRendered := func(digest string) *deliveryv1alpha1.Menu {
			m := menu()
			m.Status.Source.PreRendered = true
			m.Status.Source.Digest = digest
			return m
		}
		plain := &deliveryv1alpha1.Order{Spec: deliveryv1alpha1.OrderSpec{MenuRef: &deliveryv1alpha1.MenuRef{Name: "app"}}}

		first := menuHashOf(t, preRendered("sha256:aa"), plain)
		require.Equal(t, first, menuHashOf(t, preRendered("sha256:aa"), plain))
		require.NotEqual(t, first, menuHashOf(t, preRendered("sha256:bb"), plain))
	})

	t.Run("standalone order with the merged configuration has the same hash", func(t *testing.T) {
		standalone := &deliveryv1alpha1.Order{
			Spec: deliveryv1alpha1.OrderSpec{
				Source: &deliveryv1alpha1.OCISource{OCI: hashSource, Version: hashVersion},
				Render: helmRender(`{"image":{"tag":"1.0.0"},"replicaCount":1}`),
			},
		}
		require.Equal(t, base, hashOf(t, standalone, hashSource, hashDest))
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

// TestIntent_golden pins the canonical form of a fully populated intent. The
// config hash is the SHA-256 of these bytes, so a change here renders a new
// Preparation for every Order and voids its approvals. Update the golden file
// (go test -update) only when that is intended. The file is indented for
// reading; the canonical form is the same JSON without whitespace.
func TestIntent_golden(t *testing.T) {
	const name = "podinfo"

	order := &deliveryv1alpha1.Order{
		Spec: deliveryv1alpha1.OrderSpec{
			Source: &deliveryv1alpha1.OCISource{OCI: hashSource, Version: hashVersion},
			Render: &deliveryv1alpha1.Render{
				Helm: &deliveryv1alpha1.HelmRender{
					ReleaseName: name,
					Namespace:   "shop",
					IncludeCRDs: true,
					Values:      &apiextensionsv1.JSON{Raw: []byte(`{"replicaCount": 2, "image": {"tag": "1.0.0"}, "args": "a && <b>", "id": 9007199254740993}`)},
				},
			},
			Patches: []deliveryv1alpha1.Patch{{
				Target: deliveryv1alpha1.PatchTarget{Kind: "Deployment", Name: name, Namespace: "shop"},
				Set:    map[string]string{".spec.replicas": "3", ".metadata.labels.tier": "web"},
			}},
			Edits: []deliveryv1alpha1.Patch{{
				Target: deliveryv1alpha1.PatchTarget{Kind: "Service", Name: name},
				Set:    map[string]string{".spec.type": "ClusterIP"},
			}},
			Promotion: &deliveryv1alpha1.PromotionSpec{
				Mode:      deliveryv1alpha1.PromotionModeManual,
				Approvals: &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 2, AllowedGroups: []string{"release", "security"}},
			},
		},
	}

	effective, err := resolve.FromOrder(order)
	require.NoError(t, err)

	canonical, err := resolve.NewIntent(order, effective, hashSource, hashDest).Canonical()
	require.NoError(t, err)

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
