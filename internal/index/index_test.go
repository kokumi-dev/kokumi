package index

import (
	"testing"

	"github.com/stretchr/testify/require"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

func TestProductPantries(t *testing.T) {
	pantry := func(name string) *deliveryv1alpha1.PantryRef { return &deliveryv1alpha1.PantryRef{Name: name} }

	tests := []struct {
		name string
		spec deliveryv1alpha1.ProductSpec
		want []string
	}{
		{
			name: "no Pantries",
			spec: deliveryv1alpha1.ProductSpec{Vendor: &deliveryv1alpha1.OCIDestination{}},
		},
		{
			name: "source and vendor Pantries",
			spec: deliveryv1alpha1.ProductSpec{
				Source: deliveryv1alpha1.OCISource{PantryRef: pantry("upstream")},
				Vendor: &deliveryv1alpha1.OCIDestination{PantryRef: pantry("mirror")},
			},
			want: []string{"upstream", "mirror"},
		},
		{
			name: "the same Pantry once",
			spec: deliveryv1alpha1.ProductSpec{
				Source: deliveryv1alpha1.OCISource{PantryRef: pantry("registry")},
				Vendor: &deliveryv1alpha1.OCIDestination{PantryRef: pantry("registry")},
			},
			want: []string{"registry"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, productPantries(&deliveryv1alpha1.Product{Spec: tt.spec}))
		})
	}
}
