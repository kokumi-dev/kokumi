package index

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/registry"
)

func TestRegistryEntries(t *testing.T) {
	const ns = "team-a"
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, deliveryv1alpha1.AddToScheme(scheme))

	withSecret := &deliveryv1alpha1.Registry{Name: "with-secret", Namespace: ns, Spec: deliveryv1alpha1.RegistrySpec{
		URL: "oci://ghcr.io/a", SecretRef: &corev1.LocalObjectReference{Name: "creds"},
	}}
	missingSecret := &deliveryv1alpha1.Registry{Name: "missing-secret", Namespace: ns, Spec: deliveryv1alpha1.RegistrySpec{
		URL: "oci://ghcr.io/b", SecretRef: &corev1.LocalObjectReference{Name: "absent"},
	}}
	creds := &corev1.Secret{Name: "creds", Namespace: ns, Type: corev1.SecretTypeDockerConfigJson}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(withSecret, missingSecret, creds).Build()
	ctx := context.Background()

	entries, err := RegistryEntries(ctx, reader, ns, []corev1.LocalObjectReference{{Name: "with-secret"}, {Name: "missing-secret"}})
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.NoError(t, entries[0].Err)
	assert.Equal(t, "creds", entries[0].Secret.Name)
	assert.Equal(t, deliveryv1alpha1.ReasonSecretNotFound, registry.Reason(entries[1].Err, ""),
		"a missing Secret fails only connections through its Registry")

	_, err = RegistryEntries(ctx, reader, ns, []corev1.LocalObjectReference{{Name: "absent"}})
	assert.Equal(t, deliveryv1alpha1.ReasonRegistryNotFound, registry.Reason(err, ""))
}
