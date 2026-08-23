package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	testOIDCSecret = "kokumi-server-oidc"
	testClientID   = "kokumi-client"
	testClientSec  = "kokumi-client-secret"
)

// newTestOIDCProvider spins up an in-process OIDC test OP and returns a provider + server.
func newTestOIDCProvider(t *testing.T) (*oidcProvider, *httptest.Server) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	srv := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{
			{PublicKey: priv.Public(), KeyID: "test-key", Algorithm: oidc.RS256},
		},
	}
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)
	srv.SetIssuer(httpSrv.URL)

	client := fake.NewClientBuilder().WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testOIDCSecret, Namespace: testNS},
		Data:       map[string][]byte{"client-secret": []byte(testClientSec)},
	}, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSecret, Namespace: testNS},
		Data:       map[string][]byte{"signing-key": []byte("test-signing-key-do-not-use-in-prod")},
	}).Build()

	cfg := &deliveryv1alpha1.OIDCConfig{
		IssuerURL:       httpSrv.URL,
		ClientID:        testClientID,
		ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
	}
	p, err := buildOIDCProvider(context.Background(), client, testNS, cfg, newAuthenticator("", nil, []byte("test-signing-key-do-not-use-in-prod")))
	require.NoError(t, err)
	require.NotNil(t, p)
	return p, httpSrv
}

func TestBuildOIDCProviderDisabled(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	p, err := buildOIDCProvider(context.Background(), client, testNS, nil, newAuthenticator("", nil, []byte("k")))
	require.NoError(t, err)
	assert.Nil(t, p, "disabled config must yield a nil provider")
}

func TestBuildOIDCProviderMissingSecret(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	cfg := &deliveryv1alpha1.OIDCConfig{
		IssuerURL: "http://example.invalid",
		ClientID:  testClientID,
	}
	_, err := buildOIDCProvider(context.Background(), client, testNS, cfg, newAuthenticator("", nil, []byte("k")))
	require.Error(t, err, "missing client secret must error")
}

func TestBuildOIDCProviderRequiresAuthenticator(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	cfg := &deliveryv1alpha1.OIDCConfig{
		IssuerURL: "http://example.invalid",
		ClientID:  testClientID,
	}
	_, err := buildOIDCProvider(context.Background(), client, testNS, cfg, nil)
	require.Error(t, err, "a nil shared authenticator must error")
}

func TestOIDCStartSetsStateCookieAndRedirects(t *testing.T) {
	p, httpSrv := newTestOIDCProvider(t)
	defer httpSrv.Close()

	mgr := &authManager{oidc: p}
	handler := handleOIDCStart(mgr)

	r := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/start", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusFound, w.Code)
	loc := w.Header().Get("Location")
	assert.Contains(t, loc, "client_id="+testClientID)
	assert.Contains(t, loc, "code_challenge=")
	assert.Contains(t, loc, "state=")

	// State cookie must be set, HttpOnly, and scoped to the oidc path.
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, oidcStateCookie, cookies[0].Name)
	assert.True(t, cookies[0].HttpOnly)
	assert.Equal(t, "/api/v1/auth/oidc", cookies[0].Path)
}

func TestOIDCCallbackFullFlow(t *testing.T) {
	p, httpSrv := newTestOIDCProvider(t)
	defer httpSrv.Close()

	mgr := &authManager{oidc: p}
	startHandler := handleOIDCStart(mgr)
	cbHandler := handleOIDCCallback(mgr)

	// 1. Start the flow to obtain a valid state + PKCE verifier cookie.
	startReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/start", nil)
	startRec := httptest.NewRecorder()
	startHandler.ServeHTTP(startRec, startReq)
	require.Equal(t, http.StatusFound, startRec.Code)
	stateCookie := startRec.Result().Cookies()[0]

	// Replay the cookie; the handler reads state from the redirect URL.
	loc, err := url.Parse(startRec.Header().Get("Location"))
	require.NoError(t, err)
	state := loc.Query().Get("state")

	// 2. Callback with state cookie + bad code; the test OP has no real token endpoint, so expect 400.
	cbReq := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/callback?state="+state+"&code=bad-code", nil)
	cbReq.AddCookie(stateCookie)
	cbRec := httptest.NewRecorder()
	cbHandler.ServeHTTP(cbRec, cbReq)
	// The exchange fails (no real token endpoint), so expect 400, not a panic or 500.
	assert.Equal(t, http.StatusBadRequest, cbRec.Code)
}

func TestOIDCCallbackStateMismatch(t *testing.T) {
	p, httpSrv := newTestOIDCProvider(t)
	defer httpSrv.Close()

	mgr := &authManager{oidc: p}
	cbHandler := handleOIDCCallback(mgr)

	// No state cookie at all.
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/callback?state=x&code=y", nil)
	rec := httptest.NewRecorder()
	cbHandler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOIDCCallbackMissingState(t *testing.T) {
	p, httpSrv := newTestOIDCProvider(t)
	defer httpSrv.Close()

	mgr := &authManager{oidc: p}
	cbHandler := handleOIDCCallback(mgr)

	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/callback?code=y", nil)
	rec := httptest.NewRecorder()
	cbHandler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestExtractClaim(t *testing.T) {
	claims := map[string]any{
		"email": "a@b.com",
		"realm_access": map[string]any{
			"roles": []any{"admin", "user"},
		},
	}
	v, err := extractClaim(claims, "email")
	require.NoError(t, err)
	assert.Equal(t, "a@b.com", v)

	_, err = extractClaim(claims, "realm_access.roles")
	require.Error(t, err, "non-string nested claim must error")

	_, err = extractClaim(claims, "missing")
	require.Error(t, err)
}

func TestIssueSessionFromOIDC(t *testing.T) {
	p, httpSrv := newTestOIDCProvider(t)
	defer httpSrv.Close()

	session, err := p.issueSession("admin@example.com")
	require.NoError(t, err)
	require.NotEmpty(t, session.AccessToken)
	require.True(t, session.SetRefreshCookie)

	// The issued access token must verify against the shared authenticator with the OIDC subject.
	claims, err := p.auth.parseToken(session.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "admin@example.com", claims.Subject)
}

func TestAuthManagerProviders(t *testing.T) {
	a := newTestAuthenticator(t)
	mgr := &authManager{auth: a, adminLogin: true, oidc: &oidcProvider{}}
	assert.ElementsMatch(t, []string{"admin", "oidc"}, mgr.providers())

	mgr2 := &authManager{auth: a, adminLogin: true}
	assert.Equal(t, []string{"admin"}, mgr2.providers())

	mgr3 := &authManager{oidc: &oidcProvider{}}
	assert.Equal(t, []string{"oidc"}, mgr3.providers())

	mgr4 := &authManager{}
	assert.Empty(t, mgr4.providers())
}

func TestRedirectURIDerivesFromHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://localhost:8080/some/path", nil)
	assert.Equal(t, "http://localhost:8080/api/v1/auth/oidc/callback", redirectURI(r))

	rHTTPS := httptest.NewRequest(http.MethodGet, "https://kokumi.example/api/v1/info", nil)
	assert.Equal(t, "https://kokumi.example/api/v1/auth/oidc/callback", redirectURI(rHTTPS))
}

// TestAuthManagerReloadWithKitchenOIDC verifies reload picks up OIDC config and a
// broken OIDC config does not disable a working admin account.
func TestAuthManagerReloadWithKitchenOIDC(t *testing.T) {
	client := fake.NewClientBuilder().WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSecret, Namespace: testNS},
		Data: map[string][]byte{
			secretKeyUsername:     []byte(testUsername),
			secretKeyPasswordHash: mustBcrypt(t, testPassword),
			secretKeySigningKey:   []byte("test-signing-key-do-not-use-in-prod"),
		},
	}, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testOIDCSecret, Namespace: testNS},
		Data:       map[string][]byte{"client-secret": []byte(testClientSec)},
	}).Build()

	m := &authManager{
		reader:    client,
		apiReader: client,
		ns:        testNS,
		getenv:    func(string) string { return "" },
		logger:    logr.Discard(),
	}

	// Kitchen with both admin and a (valid) OIDC issuer.
	kitchen := &deliveryv1alpha1.Kitchen{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: testNS},
		Spec: deliveryv1alpha1.KitchenSpec{
			AdminUser: &deliveryv1alpha1.AdminUserConfig{
				Enabled:   new(true),
				Username:  testUsername,
				SecretRef: &corev1.LocalObjectReference{Name: testSecret},
			},
			OIDC: &deliveryv1alpha1.OIDCConfig{
				IssuerURL:       "https://issuer.example",
				ClientID:        testClientID,
				ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
			},
		},
	}
	m.reload(context.Background(), kitchen)
	// Admin resolves; OIDC fails discovery (fake issuer) so only admin is active and auth stays enabled.
	assert.ElementsMatch(t, []string{"admin"}, m.providers())
	assert.False(t, m.disabled)

	// Disable admin and point OIDC at a reachable test OP to confirm it can be the sole provider.
	disabled := false
	p, httpSrv := newTestOIDCProvider(t)
	defer httpSrv.Close()
	oidcKitchen := &deliveryv1alpha1.Kitchen{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: testNS},
		Spec: deliveryv1alpha1.KitchenSpec{
			AdminUser: &deliveryv1alpha1.AdminUserConfig{
				Enabled:   &disabled,
				SecretRef: &corev1.LocalObjectReference{Name: "kokumi-server-auth"},
			},
			OIDC: &deliveryv1alpha1.OIDCConfig{
				IssuerURL:       httpSrv.URL,
				ClientID:        testClientID,
				ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
			},
		},
	}
	m.reload(context.Background(), oidcKitchen)
	assert.ElementsMatch(t, []string{"oidc"}, m.providers())
	assert.False(t, m.disabled)
	_ = p
}

// TestOIDCMiddlewareAcceptsIssuedToken verifies the middleware accepts a shared-authenticator
// token in OIDC-only mode (regression: admin authenticator nil used to return 503).
func TestOIDCMiddlewareAcceptsIssuedToken(t *testing.T) {
	p, httpSrv := newTestOIDCProvider(t)
	defer httpSrv.Close()

	mgr := &authManager{auth: p.auth, oidc: p, adminLogin: false}
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	handler := mgr.middleware(next)

	session, err := p.issueSession("admin@example.com")
	require.NoError(t, err)
	require.NotEmpty(t, session.AccessToken)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/orders", nil)
	r.Header.Set("Authorization", "Bearer "+session.AccessToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, reached, "a valid OIDC-issued token must pass the middleware")
}

// TestRefreshPreservesSubject verifies a refresh round-trip keeps the original subject
// (an OIDC login must not flip to the admin username on refresh).
func TestRefreshPreservesSubject(t *testing.T) {
	auth := newAuthenticator("admin", nil, []byte("test-signing-key-do-not-use-in-prod"))
	provider := newAdminProvider(auth)

	// Mint an initial session as an OIDC subject.
	now := time.Now()
	access, _, err := auth.issueAccessTokenFor(now, "admin@example.com")
	require.NoError(t, err)
	refresh, _, err := auth.issueRefreshToken(now, "admin@example.com")
	require.NoError(t, err)

	// Simulate a refresh request carrying the refresh cookie.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	r.AddCookie(&http.Cookie{Name: refreshCookieName, Value: refresh})

	// Parse the original access token subject for comparison.
	origClaims, err := auth.parseToken(access)
	require.NoError(t, err)
	assert.Equal(t, "admin@example.com", origClaims.Subject)

	// The refresh handler re-mints using the refresh token's subject.
	session, err := provider.Refresh(r)
	require.NoError(t, err)
	newClaims, err := auth.parseToken(session.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "admin@example.com", newClaims.Subject, "refresh must preserve the OIDC subject")
}

func mustBcrypt(t *testing.T, pw string) []byte {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	require.NoError(t, err)
	return h
}

// TestHandleLoginForbiddenWhenAdminDisabled verifies OIDC-only mode returns 403 (not 503) on login.
func TestHandleLoginForbiddenWhenAdminDisabled(t *testing.T) {
	client := fake.NewClientBuilder().WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSecret, Namespace: testNS},
		Data: map[string][]byte{
			secretKeySigningKey: []byte("test-signing-key-do-not-use-in-prod"),
		},
	}, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testOIDCSecret, Namespace: testNS},
		Data:       map[string][]byte{"client-secret": []byte(testClientSec)},
	}).Build()

	m := &authManager{
		reader:    client,
		apiReader: client,
		ns:        testNS,
		getenv:    func(string) string { return "" },
		logger:    logr.Discard(),
	}
	disabled := false
	kitchen := &deliveryv1alpha1.Kitchen{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: testNS},
		Spec: deliveryv1alpha1.KitchenSpec{
			AdminUser: &deliveryv1alpha1.AdminUserConfig{
				Enabled:   &disabled,
				SecretRef: &corev1.LocalObjectReference{Name: "kokumi-server-auth"},
			},
			OIDC: &deliveryv1alpha1.OIDCConfig{
				IssuerURL:       "https://issuer.example",
				ClientID:        testClientID,
				ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
			},
		},
	}
	m.reload(context.Background(), kitchen)
	// Admin disabled (token-only authenticator); OIDC discovery fails so auth is off but login returns 403.
	assert.NotContains(t, m.providers(), "admin")
	assert.False(t, m.adminLoginEnabled())
	assert.True(t, m.disabled)

	handler := handleLogin(m)
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "admin login is disabled")
}
