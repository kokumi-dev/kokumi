package renderer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/renderer"
)

func TestCalculateSpecHash(t *testing.T) {
	spec := deliveryv1alpha1.OrderSpec{
		Source: &deliveryv1alpha1.OCISource{
			PantryRef: &deliveryv1alpha1.PantryRef{Name: "charts"},
			Version:   "1.0.0",
		},
	}

	source := "oci://ghcr.io/org/charts/app"
	dest := "oci://registry.kokumi.svc.cluster.local:5000/ns/app"

	base, err := renderer.CalculateSpecHash(spec, source, dest)
	require.NoError(t, err)

	t.Run("same inputs produce same hash", func(t *testing.T) {
		again, err := renderer.CalculateSpecHash(spec, source, dest)
		require.NoError(t, err)
		require.Equal(t, base, again)
	})

	t.Run("source URL change produces different hash", func(t *testing.T) {
		got, err := renderer.CalculateSpecHash(spec, source+"-v2", dest)
		require.NoError(t, err)
		require.NotEqual(t, base, got)
	})

	t.Run("dest URL change produces different hash", func(t *testing.T) {
		got, err := renderer.CalculateSpecHash(spec, source, dest+"-v2")
		require.NoError(t, err)
		require.NotEqual(t, base, got)
	})

	t.Run("pantryRef name is not hashed", func(t *testing.T) {
		renamed := spec
		renamed.Source = &deliveryv1alpha1.OCISource{
			PantryRef: &deliveryv1alpha1.PantryRef{Name: "other-charts"},
			Version:   spec.Source.Version,
		}
		got, err := renderer.CalculateSpecHash(renamed, source, dest)
		require.NoError(t, err)
		require.Equal(t, base, got)
	})
}
