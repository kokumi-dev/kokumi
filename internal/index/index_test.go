package index

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

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

func TestProductVerifySecrets(t *testing.T) {
	secret := func(name string) *corev1.LocalObjectReference { return &corev1.LocalObjectReference{Name: name} }

	tests := []struct {
		name   string
		verify *deliveryv1alpha1.OCISourceVerification
		want   []string
	}{
		{name: "no verification"},
		{
			name:   "keys and trusted root",
			verify: &deliveryv1alpha1.OCISourceVerification{SecretRef: secret("keys"), TrustedRootSecretRef: secret("sigstore-root")},
			want:   []string{"keys", "sigstore-root"},
		},
		{
			name:   "the same Secret once",
			verify: &deliveryv1alpha1.OCISourceVerification{SecretRef: secret("trust"), TrustedRootSecretRef: secret("trust")},
			want:   []string{"trust"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			product := &deliveryv1alpha1.Product{Spec: deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{Verify: tt.verify}}}
			require.Equal(t, tt.want, productVerifySecrets(product))
		})
	}
}
