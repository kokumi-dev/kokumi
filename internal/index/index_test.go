package index

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

func TestRegistryRefNames(t *testing.T) {
	const upstream, mirror = "upstream", "mirror"
	refs := []corev1.LocalObjectReference{{Name: upstream}, {Name: mirror}}

	require.Equal(t, []string{upstream, mirror},
		productRegistries(&deliveryv1alpha1.Product{Spec: deliveryv1alpha1.ProductSpec{RegistryRefs: refs}}))
	require.Equal(t, []string{upstream, mirror},
		orderRegistries(&deliveryv1alpha1.Order{Spec: deliveryv1alpha1.OrderSpec{RegistryRefs: refs}}))
	require.Empty(t, productRegistries(&deliveryv1alpha1.Product{}))
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
