package credential

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	testNamespace  = "team-a"
	testSecretName = "registry-creds"
	testPantryURL  = "oci://registry.example/team-a"
	testVersion    = "1.0.0"
	testPantryName = "private"
)

func testResolver(t *testing.T, objs ...runtime.Object) *KubeResolver {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, deliveryv1alpha1.AddToScheme(scheme))
	return NewKubeResolver(fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build())
}

func testSecret() *corev1.Secret {
	return &corev1.Secret{
		Name:      testSecretName,
		Namespace: testNamespace,
		Data:      map[string][]byte{".dockerconfigjson": []byte(`{"auths":{"registry.example":{"auth":"dXNlcjpwYXNz"}}}`)},
	}
}

func testPantry(name string, mutate func(*deliveryv1alpha1.Pantry)) *deliveryv1alpha1.Pantry {
	p := &deliveryv1alpha1.Pantry{
		Name:      name,
		Namespace: testNamespace,
		Spec: deliveryv1alpha1.PantrySpec{
			URL:       testPantryURL,
			SecretRef: &corev1.LocalObjectReference{Name: testSecretName},
		},
	}
	if mutate != nil {
		mutate(p)
	}
	return p
}

func TestResolveSource(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		objs       []runtime.Object
		src        deliveryv1alpha1.OCISource
		wantURL    string
		wantClient bool
		wantErr    bool
	}{
		{
			name:    "plain OCI source passes through without client",
			objs:    []runtime.Object{},
			src:     deliveryv1alpha1.OCISource{OCI: "oci://registry.example/other", Version: testVersion},
			wantURL: "oci://registry.example/other",
		},
		{
			name:       "pantry ref resolves to pantry URL with authenticated client",
			objs:       []runtime.Object{testSecret(), testPantry(testPantryName, nil)},
			src:        deliveryv1alpha1.OCISource{PantryRef: &deliveryv1alpha1.PantryRef{Name: testPantryName}, Version: testVersion},
			wantURL:    testPantryURL,
			wantClient: true,
		},
		{
			name: "pantry without secretRef resolves anonymously",
			objs: []runtime.Object{testPantry("public", func(p *deliveryv1alpha1.Pantry) {
				p.Spec.SecretRef = nil
			})},
			src:     deliveryv1alpha1.OCISource{PantryRef: &deliveryv1alpha1.PantryRef{Name: "public"}, Version: testVersion},
			wantURL: testPantryURL,
		},
		{
			name: "pantry with secret missing dockerconfigjson resolves anonymously",
			objs: []runtime.Object{
				&corev1.Secret{Name: testSecretName, Namespace: testNamespace, Data: map[string][]byte{"other": []byte("x")}},
				testPantry(testPantryName, nil),
			},
			src:     deliveryv1alpha1.OCISource{PantryRef: &deliveryv1alpha1.PantryRef{Name: testPantryName}, Version: testVersion},
			wantURL: testPantryURL,
		},
		{
			name:    "missing pantry is an error",
			objs:    []runtime.Object{},
			src:     deliveryv1alpha1.OCISource{PantryRef: &deliveryv1alpha1.PantryRef{Name: "nope"}, Version: testVersion},
			wantErr: true,
		},
		{
			name:    "missing credential secret is an error",
			objs:    []runtime.Object{testPantry(testPantryName, nil)},
			src:     deliveryv1alpha1.OCISource{PantryRef: &deliveryv1alpha1.PantryRef{Name: testPantryName}, Version: testVersion},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := testResolver(t, tt.objs...)
			url, client, err := resolver.ResolveSource(ctx, tt.src, testNamespace)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, url)
			if tt.wantClient {
				assert.NotNil(t, client)
			} else {
				assert.Nil(t, client)
			}
		})
	}
}

func TestResolveDestination(t *testing.T) {
	ctx := context.Background()
	defaultURL := "oci://registry.kokumi.svc.cluster.local:5000/default"

	tests := []struct {
		name       string
		objs       []runtime.Object
		dest       *deliveryv1alpha1.OCIDestination
		wantURL    string
		wantClient bool
		wantErr    bool
	}{
		{
			name:    "nil destination falls back to default",
			objs:    []runtime.Object{},
			dest:    nil,
			wantURL: defaultURL,
		},
		{
			name:    "empty destination falls back to default",
			objs:    []runtime.Object{},
			dest:    &deliveryv1alpha1.OCIDestination{},
			wantURL: defaultURL,
		},
		{
			name:    "plain OCI destination passes through without client",
			objs:    []runtime.Object{},
			dest:    &deliveryv1alpha1.OCIDestination{OCI: "oci://registry.example/dest"},
			wantURL: "oci://registry.example/dest",
		},
		{
			name:       "pantry ref resolves to pantry URL with authenticated client",
			objs:       []runtime.Object{testSecret(), testPantry(testPantryName, nil)},
			dest:       &deliveryv1alpha1.OCIDestination{PantryRef: &deliveryv1alpha1.PantryRef{Name: testPantryName}},
			wantURL:    testPantryURL,
			wantClient: true,
		},
		{
			name:    "missing pantry is an error",
			objs:    []runtime.Object{},
			dest:    &deliveryv1alpha1.OCIDestination{PantryRef: &deliveryv1alpha1.PantryRef{Name: "nope"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := testResolver(t, tt.objs...)
			url, client, err := resolver.ResolveDestination(ctx, tt.dest, defaultURL, testNamespace)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, url)
			if tt.wantClient {
				assert.NotNil(t, client)
			} else {
				assert.Nil(t, client)
			}
		})
	}
}

func TestClientForPantry(t *testing.T) {
	ctx := context.Background()

	t.Run("pantry with credentials yields an authenticated client", func(t *testing.T) {
		resolver := testResolver(t, testSecret(), testPantry(testPantryName, nil))
		client, err := resolver.ClientForPantry(ctx, testNamespace, testPantryName)
		require.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("pantry without secretRef yields a nil client", func(t *testing.T) {
		resolver := testResolver(t, testPantry("public", func(p *deliveryv1alpha1.Pantry) {
			p.Spec.SecretRef = nil
		}))
		client, err := resolver.ClientForPantry(ctx, testNamespace, "public")
		require.NoError(t, err)
		assert.Nil(t, client)
	})

	t.Run("missing pantry is an error", func(t *testing.T) {
		resolver := testResolver(t)
		client, err := resolver.ClientForPantry(ctx, testNamespace, "nope")
		assert.Error(t, err)
		assert.Nil(t, client)
	})
}
