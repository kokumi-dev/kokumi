package registry

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/errcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

const (
	testNamespace = "team-a"
	bundledHost   = "kokumi-registry.kokumi.svc.cluster.local:5000"

	hostGHCR  = "ghcr.io"
	hostLocal = "localhost:5000"
	hostHub   = "docker.io"

	nameHub      = "hub"
	nameOrg      = "org"
	nameRendered = "rendered"
	userBot      = "bot"
	pathNginx    = "library/nginx"
)

func TestParseLocation(t *testing.T) {
	for in, want := range map[string]Location{
		"oci://ghcr.io":                       {Host: hostGHCR},
		"oci://GHCR.io/MyOrg":                 {Host: hostGHCR, Path: "MyOrg"},
		"oci://ghcr.io/org/app:1.0":           {Host: hostGHCR, Path: "org/app"},
		"oci://localhost:5000/app@sha256:abc": {Host: hostLocal, Path: "app"},
		"registry-1.docker.io/library/nginx":  {Host: hostHub, Path: pathNginx},
		"index.docker.io/library/nginx:1.27":  {Host: hostHub, Path: pathNginx},
		"mirror.example.com:8443/hub/":        {Host: "mirror.example.com:8443", Path: nameHub},
	} {
		got, err := ParseLocation(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	_, err := ParseLocation("oci:///app")
	require.Error(t, err)
}

func TestLocation_Covers(t *testing.T) {
	org := Location{Host: hostGHCR, Path: nameOrg}
	assert.True(t, org.Covers(Location{Host: hostGHCR, Path: nameOrg}))
	assert.True(t, org.Covers(Location{Host: hostGHCR, Path: "org/app/images/x"}))
	assert.False(t, org.Covers(Location{Host: hostGHCR, Path: "organization"}), "paths match at slash boundaries")
	assert.False(t, org.Covers(Location{Host: hostGHCR}))
	assert.False(t, org.Covers(Location{Host: "ghcr.io:5000", Path: nameOrg}), "ports must be equal")
	assert.True(t, Location{Host: hostGHCR}.Covers(org))
}

func testRegistry(name, url string) deliveryv1alpha1.Registry {
	return deliveryv1alpha1.Registry{
		Name: name, Namespace: testNamespace, UID: k8stypes.UID("uid-" + name),
		Spec: deliveryv1alpha1.RegistrySpec{URL: url},
	}
}

func TestMatch(t *testing.T) {
	registries := []deliveryv1alpha1.Registry{
		testRegistry("ghcr", "oci://ghcr.io"),
		testRegistry(nameOrg, "oci://ghcr.io/myorg"),
		testRegistry(nameRendered, "oci://ghcr.io/myorg/mychart-rendered"),
		testRegistry(nameHub, "oci://docker.io"),
	}

	for url, want := range map[string]string{
		"oci://ghcr.io/myorg/mychart":                                     nameOrg,
		"oci://ghcr.io/myorg/mychart-rendered":                            nameRendered,
		"oci://ghcr.io/myorg/mychart-rendered/images/docker.io/library/x": nameRendered,
		"oci://ghcr.io/other/app":                                         "ghcr",
		"registry-1.docker.io/library/nginx":                              nameHub,
		"quay.io/org/app":                                                 "",
	} {
		got, err := Match(registries, url)
		require.NoError(t, err, url)
		if want == "" {
			assert.Nil(t, got, url)
			continue
		}
		require.NotNil(t, got, url)
		assert.Equal(t, want, got.Name, url)
	}
}

func dockerConfigJSON(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	auths := ""
	for key, user := range entries {
		if auths != "" {
			auths += ","
		}
		auths += fmt.Sprintf(`%q:{"auth":%q}`, key, base64.StdEncoding.EncodeToString([]byte(user+":secret")))
	}
	return []byte(`{"auths":{` + auths + `}}`)
}

func TestCredentialFor(t *testing.T) {
	data := dockerConfigJSON(t, map[string]string{
		hostGHCR:                         "host",
		"ghcr.io/myorg":                  nameOrg,
		"https://ghcr.io/myorg/rendered": nameRendered,
		"*.example.com":                  "wildcard",
		"registry.example.com":           "exact",
		hostLocal:                        "local",
		"https://index.docker.io/v1/":    nameHub,
	})

	for loc, want := range map[Location]string{
		{Host: hostGHCR, Path: "myorg/app"}:             nameOrg,
		{Host: hostGHCR, Path: "myorg/rendered/images"}: nameRendered,
		{Host: hostGHCR, Path: "other"}:                 "host",
		{Host: "registry.example.com", Path: "a"}:       "exact",
		{Host: "other.example.com", Path: "a"}:          "wildcard",
		{Host: hostLocal, Path: "a"}:                    "local",
		{Host: hostHub, Path: pathNginx}:                nameHub,
	} {
		cred, err := credentialFor(data, loc)
		require.NoError(t, err, loc)
		assert.Equal(t, want, cred.Username, loc)
	}

	for _, loc := range []Location{
		{Host: "localhost:5001", Path: "a"},  // ports must be equal
		{Host: "a.b.example.com", Path: "a"}, // * is one label
		{Host: "quay.io", Path: "a"},
	} {
		_, err := credentialFor(data, loc)
		require.Error(t, err, loc)
	}
}

func TestCredentialFor_isDeterministic(t *testing.T) {
	data := dockerConfigJSON(t, map[string]string{hostGHCR: "a", "https://ghcr.io": "b"})
	for range 50 {
		cred, err := credentialFor(data, Location{Host: hostGHCR, Path: "x"})
		require.NoError(t, err)
		require.Equal(t, "a", cred.Username)
	}
}

// recordingFactory builds fake clients and records the options of each.
type recordingFactory struct {
	options []oci.Options
}

func (f *recordingFactory) newClient(opts oci.Options) oci.Client {
	f.options = append(f.options, opts)
	return &oci.FakeClient{}
}

func newTestClients() (*Clients, *recordingFactory) {
	factory := &recordingFactory{}
	return NewClients(bundledHost).WithClientFactory(factory.newClient), factory
}

func pullSecret(name string, data []byte) *corev1.Secret {
	return &corev1.Secret{
		Name: name, Namespace: testNamespace,
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: data},
	}
}

func TestScope_Connect(t *testing.T) {
	org := testRegistry(nameOrg, "oci://ghcr.io/myorg")
	org.Spec.SecretRef = &corev1.LocalObjectReference{Name: "ghcr"}
	secret := pullSecret("ghcr", dockerConfigJSON(t, map[string]string{hostGHCR: userBot}))
	clients, factory := newTestClients()
	scope := clients.Scope([]Entry{{Registry: &org, Secret: secret}})

	conn, err := scope.Connect("oci://ghcr.io/myorg/app")
	require.NoError(t, err)
	assert.Equal(t, nameOrg, conn.RegistryName())
	assert.True(t, conn.Authenticated())
	assert.Equal(t, credentials.Credential{Username: userBot, Password: "secret"}, conn.Credential)

	again, err := clients.Scope([]Entry{{Registry: &org, Secret: secret}}).Connect("oci://ghcr.io/myorg/other")
	require.NoError(t, err)
	assert.Same(t, conn.Client, again.Client, "one client per Registry keeps tokens and mounts blobs")
	require.Len(t, factory.options, 1)
	assert.Equal(t, hostGHCR, factory.options[0].Host)

	anon, err := scope.Connect("oci://quay.io/x/app")
	require.NoError(t, err)
	assert.Nil(t, anon.Registry)
	assert.False(t, anon.Authenticated())
	assert.False(t, anon.PlainHTTP)

	bundled, err := clients.Scope(nil).Connect("oci://" + bundledHost + "/team-a/app")
	require.NoError(t, err)
	assert.True(t, bundled.PlainHTTP, "the bundled registry is plain HTTP")
}

func TestScope_Connect_errors(t *testing.T) {
	missingSecret := testRegistry("missing-secret", "oci://ghcr.io/a")
	missingSecret.Spec.SecretRef = &corev1.LocalObjectReference{Name: "absent"}

	wrongType := testRegistry("wrong-type", "oci://ghcr.io/b")
	wrongType.Spec.SecretRef = &corev1.LocalObjectReference{Name: "opaque"}
	opaque := &corev1.Secret{Name: "opaque", Namespace: testNamespace, Type: corev1.SecretTypeOpaque}

	noEntry := testRegistry("no-entry", "oci://ghcr.io/c")
	noEntry.Spec.SecretRef = &corev1.LocalObjectReference{Name: "quay"}
	quay := pullSecret("quay", dockerConfigJSON(t, map[string]string{"quay.io": userBot}))

	badCert := testRegistry("bad-cert", "oci://ghcr.io/d")
	badCert.Spec.CertSecretRef = &corev1.LocalObjectReference{Name: "certs"}
	certs := &corev1.Secret{Name: "certs", Namespace: testNamespace, Data: map[string][]byte{"ca.crt": []byte("not pem")}}

	unreadable := testRegistry("unreadable", "oci://ghcr.io/e")
	unreadable.Spec.SecretRef = &corev1.LocalObjectReference{Name: "hidden"}

	clients, _ := newTestClients()
	scope := clients.Scope([]Entry{
		{Registry: &missingSecret},
		{Registry: &wrongType, Secret: opaque},
		{Registry: &noEntry, Secret: quay},
		{Registry: &badCert, CertSecret: certs},
		{Registry: &unreadable, Err: errors.New("not authorized")},
	})

	for _, tc := range []struct{ url, want string }{
		{"oci://ghcr.io/a/app", deliveryv1alpha1.ReasonSecretNotFound},
		{"oci://ghcr.io/b/app", deliveryv1alpha1.ReasonSecretInvalid},
		{"oci://ghcr.io/c/app", deliveryv1alpha1.ReasonSecretInvalid},
		{"oci://ghcr.io/d/app", deliveryv1alpha1.ReasonCertificateInvalid},
		{"oci://ghcr.io/e/app", ""},
	} {
		_, err := scope.Connect(tc.url)
		require.Error(t, err, tc.url)
		assert.Equal(t, tc.want, Reason(err, ""), tc.url)
	}

	_, err := scope.Connect("oci://quay.io/x/app")
	require.NoError(t, err, "an unreadable Secret fails only connections through its Registry")
}

func TestPingReason(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&errcode.ErrorResponse{StatusCode: http.StatusUnauthorized}, deliveryv1alpha1.ReasonUnauthorized},
		{fmt.Errorf("ping: %w", &errcode.ErrorResponse{StatusCode: http.StatusForbidden}), deliveryv1alpha1.ReasonUnauthorized},
		{fmt.Errorf("ping: %w", auth.ErrBasicCredentialNotFound), deliveryv1alpha1.ReasonUnauthorized},
		{&errcode.ErrorResponse{StatusCode: http.StatusNotFound}, deliveryv1alpha1.ReasonUnreachable},
		{errors.New("dial tcp: connection refused"), deliveryv1alpha1.ReasonUnreachable},
	} {
		assert.Equal(t, tc.want, PingReason(tc.err), tc.err.Error())
	}
}
