package registry

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const hostQuay = "quay.io"

// fakeReader serves Registries and Secrets by name.
type fakeReader struct {
	registries map[string]*deliveryv1alpha1.Registry
	secrets    map[string]*corev1.Secret
}

func (f fakeReader) Registry(_ context.Context, _, name string) (*deliveryv1alpha1.Registry, error) {
	if reg, ok := f.registries[name]; ok {
		return reg, nil
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "registries"}, name)
}

func (f fakeReader) Secret(_ context.Context, _, name string) (*corev1.Secret, error) {
	if secret, ok := f.secrets[name]; ok {
		return secret, nil
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
}

func TestResolver_Scope(t *testing.T) {
	const creds = "creds"
	withSecret := &deliveryv1alpha1.Registry{Name: "with-secret", Namespace: testNamespace, Spec: deliveryv1alpha1.RegistrySpec{
		URL: "oci://ghcr.io/a", SecretRef: &corev1.LocalObjectReference{Name: creds},
	}}
	missingSecret := &deliveryv1alpha1.Registry{Name: "missing-secret", Namespace: testNamespace, Spec: deliveryv1alpha1.RegistrySpec{
		URL: "oci://ghcr.io/b", SecretRef: &corev1.LocalObjectReference{Name: "missing"},
	}}
	reader := fakeReader{
		registries: map[string]*deliveryv1alpha1.Registry{withSecret.Name: withSecret, missingSecret.Name: missingSecret},
		secrets:    map[string]*corev1.Secret{creds: {Name: creds, Namespace: testNamespace, Type: corev1.SecretTypeDockerConfigJson}},
	}
	resolver := NewResolver(NewClients(bundledHost), reader)
	ctx := context.Background()

	scope, err := resolver.Scope(ctx, testNamespace, []corev1.LocalObjectReference{{Name: "with-secret"}, {Name: "missing-secret"}})
	require.NoError(t, err)
	require.Len(t, scope.entries, 2)
	require.NoError(t, scope.entries[0].Err)
	assert.Equal(t, creds, scope.entries[0].Secret.Name)
	assert.Equal(t, deliveryv1alpha1.ReasonSecretNotFound, Reason(scope.entries[1].Err, ""),
		"a missing Secret fails only connections through its Registry")

	_, err = resolver.Scope(ctx, testNamespace, []corev1.LocalObjectReference{{Name: "unknown"}})
	assert.Equal(t, deliveryv1alpha1.ReasonRegistryNotFound, Reason(err, ""))
}

func TestResolver_ConnectRecorded(t *testing.T) {
	const (
		artifact      = "oci://ghcr.io/org/app@sha256:aa"
		nameRecorded  = "recorded"
		nameElsewhere = "elsewhere"
		nameCurrent   = "current"
	)
	recorded := &deliveryv1alpha1.Registry{Name: nameRecorded, Namespace: testNamespace, Spec: deliveryv1alpha1.RegistrySpec{URL: "oci://ghcr.io/org"}}
	elsewhere := &deliveryv1alpha1.Registry{Name: nameElsewhere, Namespace: testNamespace, Spec: deliveryv1alpha1.RegistrySpec{URL: "oci://" + hostQuay}}
	current := &deliveryv1alpha1.Registry{Name: nameCurrent, Namespace: testNamespace, Spec: deliveryv1alpha1.RegistrySpec{URL: "oci://ghcr.io"}}
	reader := fakeReader{registries: map[string]*deliveryv1alpha1.Registry{
		recorded.Name: recorded, elsewhere.Name: elsewhere, current.Name: current,
	}}
	resolver := NewResolver(NewClients(bundledHost), reader)

	fallbackTo := func(names ...string) (func(context.Context) ([]corev1.LocalObjectReference, error), *bool) {
		called := false
		return func(context.Context) ([]corev1.LocalObjectReference, error) {
			called = true
			refs := make([]corev1.LocalObjectReference, 0, len(names))
			for _, name := range names {
				refs = append(refs, corev1.LocalObjectReference{Name: name})
			}
			return refs, nil
		}, &called
	}

	tests := []struct {
		name         string
		recorded     *corev1.LocalObjectReference
		wantRegistry string
		wantFallback bool
	}{
		{name: "through the recorded Registry", recorded: &corev1.LocalObjectReference{Name: nameRecorded}, wantRegistry: nameRecorded},
		{name: "through the fallback when nothing is recorded", wantRegistry: nameCurrent, wantFallback: true},
		{name: "through the fallback when the recorded Registry is gone", recorded: &corev1.LocalObjectReference{Name: "deleted"},
			wantRegistry: nameCurrent, wantFallback: true},
		{name: "through the fallback when the recorded Registry no longer covers it", recorded: &corev1.LocalObjectReference{Name: nameElsewhere},
			wantRegistry: nameCurrent, wantFallback: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fallback, called := fallbackTo(nameCurrent)
			conn, err := resolver.ConnectRecorded(context.Background(), testNamespace, tt.recorded, artifact, fallback)
			require.NoError(t, err)
			assert.Equal(t, tt.wantRegistry, conn.RegistryName())
			assert.Equal(t, tt.wantFallback, *called)
		})
	}
}

func TestOrderAccess(t *testing.T) {
	product := NewClients(bundledHost).Scope([]Entry{{Registry: &deliveryv1alpha1.Registry{
		Name: nameHub, Spec: deliveryv1alpha1.RegistrySpec{URL: "oci://" + hostHub},
	}}})

	access := OrderAccess(OrderLocations{
		Product:         product,
		Source:          "oci://" + hostGHCR + "/org/chart",
		Destination:     "oci://" + hostLocal + "/order/app",
		DestinationConn: Connection{Registry: &deliveryv1alpha1.Registry{Name: nameRendered}},
		ImagesVendorURL: "oci://" + hostLocal + "/vendor",
		Images: []deliveryv1alpha1.PreparationImage{
			{Name: hostHub + "/" + pathNginx, Vendored: hostLocal + "/vendor/images/" + pathNginx},
			{Name: hostQuay + "/app"},
		},
	})

	type entry struct {
		url      string
		op       deliveryv1alpha1.RegistryOperation
		who      deliveryv1alpha1.RegistryClient
		registry string
	}
	got := make([]entry, 0, len(access))
	for _, a := range access {
		got = append(got, entry{a.URL, a.Operation, a.Client, a.Registry})
	}
	assert.Equal(t, []entry{
		{"oci://" + hostHub, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKokumi, nameHub},
		{"oci://" + hostGHCR + "/org/chart", deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKokumi, ""},
		{"oci://" + hostLocal + "/order/app", deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientArgoCD, ""},
		{"oci://" + hostLocal + "/order/app", deliveryv1alpha1.RegistryOperationPush, deliveryv1alpha1.RegistryClientKokumi, nameRendered},
		{"oci://" + hostLocal + "/vendor/images", deliveryv1alpha1.RegistryOperationPush, deliveryv1alpha1.RegistryClientKokumi, ""},
		{"oci://" + hostLocal + "/vendor/images", deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKubelet, ""},
		{"oci://" + hostQuay, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKokumi, ""},
		{"oci://" + hostQuay, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKubelet, ""},
	}, got)
}

func TestURLCovers(t *testing.T) {
	covered, err := URLCovers("oci://ghcr.io/org", "oci://ghcr.io/org/app")
	require.NoError(t, err)
	assert.True(t, covered)

	covered, err = URLCovers("oci://ghcr.io/org", "oci://ghcr.io/organization")
	require.NoError(t, err)
	assert.False(t, covered)

	_, err = URLCovers("oci://", "oci://ghcr.io/org")
	assert.Error(t, err)
}

func TestRetriable(t *testing.T) {
	assert.True(t, Retriable(errors.New("dial tcp: connection refused")))
	assert.False(t, Retriable(x509.UnknownAuthorityError{}), "an untrusted certificate stays until the configuration changes")
	assert.False(t, Retriable(errors.Join(errors.New("ping"), x509.HostnameError{})))
}
