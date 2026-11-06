package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	testOIDCSecret = "kokumi-server-oidc"
	testClientID   = "kokumi-client"
	testClientSec  = "kokumi-client-secret"
	testKeyID      = "test-key"
	testIssuerURL  = "https://issuer.example"
)

// newTestOIDCProvider spins up an in-process OIDC test OP and returns a provider + server.
func newTestOIDCProvider(t *testing.T) (*oidcProvider, *httptest.Server) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	srv := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{
			{PublicKey: priv.Public(), KeyID: testKeyID, Algorithm: oidc.RS256},
		},
	}
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)
	srv.SetIssuer(httpSrv.URL)
	return newOIDCProviderFor(t, httpSrv.URL, deliveryv1alpha1.ProviderSignOutNever), httpSrv
}

// newOIDCProviderFor returns a provider for the issuer at issuerURL, with
// the client testClientID and the providerSignOut mode signOut.
func newOIDCProviderFor(t *testing.T, issuerURL string, signOut deliveryv1alpha1.ProviderSignOut) *oidcProvider {
	t.Helper()
	client := fake.NewClientBuilder().WithObjects(&corev1.Secret{
		Name: testOIDCSecret, Namespace: testNS,
		Data: map[string][]byte{secretKeyClientSecret: []byte(testClientSec)},
	}, &corev1.Secret{
		Name: testSecret, Namespace: testNS,
		Data: map[string][]byte{"signing-key": []byte("test-signing-key-do-not-use-in-prod")},
	}).Build()

	cfg := &deliveryv1alpha1.OIDCConfig{
		IssuerURL:       issuerURL,
		ClientID:        testClientID,
		ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
		ProviderSignOut: signOut,
	}
	p, err := buildOIDCProvider(context.Background(), client, testNS, cfg, newAuthenticator("", nil, []byte("test-signing-key-do-not-use-in-prod")))
	require.NoError(t, err)
	require.NotNil(t, p)
	return p
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
	const (
		testEmailClaim  = "email"
		namespacedEmail = "https://kokumi.example.com/email"
	)
	claims := map[string]any{
		testEmailClaim:  "a@b.com",
		namespacedEmail: "c@d.com",
		testRealm: map[string]any{
			testRoles: []any{providerAdmin, "user"},
		},
	}
	v, err := extractClaim(claims, testEmailClaim)
	require.NoError(t, err)
	assert.Equal(t, "a@b.com", v)

	v, err = extractClaim(claims, namespacedEmail)
	require.NoError(t, err, "a top-level claim name with dots must resolve")
	assert.Equal(t, "c@d.com", v)

	_, err = extractClaim(claims, "realm_access.roles")
	require.Error(t, err, "non-string nested claim must error")

	_, err = extractClaim(claims, "missing")
	require.Error(t, err)
}

func TestAuthManagerProviders(t *testing.T) {
	a := newTestAuthenticator(t)
	mgr := &authManager{auth: a, adminLogin: true, oidc: &oidcProvider{}}
	assert.ElementsMatch(t, []string{providerAdmin, providerOIDC}, mgr.providers())

	mgr2 := &authManager{auth: a, adminLogin: true}
	assert.Equal(t, []string{providerAdmin}, mgr2.providers())

	mgr3 := &authManager{oidc: &oidcProvider{}}
	assert.Equal(t, []string{providerOIDC}, mgr3.providers())

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
		Name: testSecret, Namespace: testNS,
		Data: map[string][]byte{
			secretKeyPasswordHash: mustBcrypt(t, testPassword),
		},
	}, &corev1.Secret{
		Name: deliveryv1alpha1.DefaultTokenSigningKeySecretName, Namespace: testNS,
		Data: map[string][]byte{secretKeySigningKey: []byte("test-signing-key-do-not-use-in-prod")},
	}, &corev1.Secret{
		Name: testOIDCSecret, Namespace: testNS,
		Data: map[string][]byte{secretKeyClientSecret: []byte(testClientSec)},
	}).Build()

	m := &authManager{
		reader:    client,
		apiReader: client,
		ns:        testNS,
		logger:    logr.Discard(),
	}

	// Kitchen with both admin and a (valid) OIDC issuer.
	kitchen := &deliveryv1alpha1.Kitchen{
		Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNS,
		Spec: deliveryv1alpha1.KitchenSpec{
			Auth: &deliveryv1alpha1.KitchenAuth{
				AdminUser: &deliveryv1alpha1.AdminUserConfig{
					Enabled:   true,
					Username:  testUsername,
					SecretRef: &corev1.LocalObjectReference{Name: testSecret},
				},
				OIDC: &deliveryv1alpha1.OIDCConfig{
					IssuerURL:       testIssuerURL,
					ClientID:        testClientID,
					ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
				},
			},
		},
	}
	m.reload(context.Background(), kitchen)
	// Both resolve: discovery happens at sign-in, so an issuer that cannot be
	// reached does not hide SSO.
	assert.ElementsMatch(t, []string{providerAdmin, providerOIDC}, m.providers())

	// Remove the admin account and point OIDC at a reachable test OP to confirm it can be the sole provider.
	p, httpSrv := newTestOIDCProvider(t)
	defer httpSrv.Close()
	oidcKitchen := &deliveryv1alpha1.Kitchen{
		Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNS,
		Spec: deliveryv1alpha1.KitchenSpec{
			Auth: &deliveryv1alpha1.KitchenAuth{
				OIDC: &deliveryv1alpha1.OIDCConfig{
					IssuerURL:       httpSrv.URL,
					ClientID:        testClientID,
					ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
				},
			},
		},
	}
	m.reload(context.Background(), oidcKitchen)
	assert.ElementsMatch(t, []string{providerOIDC}, m.providers())
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

	const testOIDCUser = "admin@example.com"
	session, err := p.auth.startSession(&Identity{Subject: testOIDCUser, Email: testOIDCUser, Provider: providerOIDC}, newSessionState(time.Now()))
	require.NoError(t, err)
	require.NotEmpty(t, session.AccessToken)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/orders", nil)
	r.Header.Set("Authorization", "Bearer "+session.AccessToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, reached, "a valid OIDC-issued token must pass the middleware")
}

// TestRefreshPreservesSubject verifies a refresh round-trip keeps the original
// identity (an OIDC login must not flip to the admin identity on refresh,
// which would silently remap the user to the admin ServiceAccount).
func TestRefreshPreservesSubject(t *testing.T) {
	auth := newAuthenticator("admin", nil, []byte("test-signing-key-do-not-use-in-prod"))
	origID := &Identity{
		Subject:  "admin@example.com",
		Email:    "admin@example.com",
		Groups:   []string{"devops"},
		Provider: providerOIDC,
	}
	signedIn, err := auth.startSession(origID, newSessionState(time.Now()))
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	r.AddCookie(&http.Cookie{Name: refreshCookieName, Value: signedIn.RefreshToken})
	w := httptest.NewRecorder()
	handleRefresh(&authManager{auth: auth}).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp loginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	got, err := auth.parseToken(resp.Token, accessTokenType)
	require.NoError(t, err)
	assert.Equal(t, origID, got.identity, "refresh must preserve the full OIDC identity")
}

func mustBcrypt(t *testing.T, pw string) []byte {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	require.NoError(t, err)
	return h
}

// TestHandleLoginForbiddenWithoutAdmin verifies OIDC-only mode returns 403 (not 503) on login.
func TestHandleLoginForbiddenWithoutAdmin(t *testing.T) {
	client := fake.NewClientBuilder().WithObjects(&corev1.Secret{
		Name: deliveryv1alpha1.DefaultTokenSigningKeySecretName, Namespace: testNS,
		Data: map[string][]byte{secretKeySigningKey: []byte("test-signing-key-do-not-use-in-prod")},
	}, &corev1.Secret{
		Name: testOIDCSecret, Namespace: testNS,
		Data: map[string][]byte{secretKeyClientSecret: []byte(testClientSec)},
	}).Build()

	m := &authManager{
		reader:    client,
		apiReader: client,
		ns:        testNS,
		logger:    logr.Discard(),
	}
	kitchen := &deliveryv1alpha1.Kitchen{
		Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNS,
		Spec: deliveryv1alpha1.KitchenSpec{
			Auth: &deliveryv1alpha1.KitchenAuth{
				OIDC: &deliveryv1alpha1.OIDCConfig{
					IssuerURL:       testIssuerURL,
					ClientID:        testClientID,
					ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
				},
			},
		},
	}
	m.reload(context.Background(), kitchen)
	// No admin account (token-only authenticator); OIDC is the only provider, so login returns 403.
	assert.Equal(t, []string{providerOIDC}, m.providers())
	assert.False(t, m.adminLoginEnabled())

	handler := handleLogin(m)
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "admin login is disabled")
}

// flakyIssuer is an in-process OIDC test OP that answers 503 while down is set
// and counts discovery requests.
type flakyIssuer struct {
	url         string
	down        atomic.Bool
	discoveries atomic.Int32
}

func newFlakyIssuer(t *testing.T) *flakyIssuer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	op := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{
			{PublicKey: priv.Public(), KeyID: testKeyID, Algorithm: oidc.RS256},
		},
	}
	f := &flakyIssuer{}
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			f.discoveries.Add(1)
		}
		if f.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		op.ServeHTTP(w, r)
	}))
	t.Cleanup(httpSrv.Close)
	op.SetIssuer(httpSrv.URL)
	f.url = httpSrv.URL
	return f
}

// TestOIDCIssuerRetriesFailedDiscovery verifies an issuer that is down at first
// is discovered once it is up, without asking it again within the retry interval.
func TestOIDCIssuerRetriesFailedDiscovery(t *testing.T) {
	f := newFlakyIssuer(t)
	f.down.Store(true)
	issuer := &oidcIssuer{url: f.url}
	ctx := context.Background()

	_, err := issuer.discover(ctx)
	require.Error(t, err)
	_, err = issuer.discover(ctx)
	require.Error(t, err, "a failure is reported until the retry interval has passed")
	assert.Equal(t, int32(1), f.discoveries.Load(), "the issuer must not be asked again within the retry interval")

	f.down.Store(false)
	issuer.failedAt = issuer.failedAt.Add(-oidcDiscoveryRetryInterval)
	provider, err := issuer.discover(ctx)
	require.NoError(t, err, "the issuer must be asked again once the retry interval has passed")
	assert.True(t, strings.HasPrefix(provider.Endpoint().AuthURL, f.url))

	again, err := issuer.discover(ctx)
	require.NoError(t, err)
	assert.Same(t, provider, again, "a discovered issuer is kept")
	assert.Equal(t, int32(2), f.discoveries.Load())
}

// TestOIDCStartRedirectsWhenIssuerUnreachable verifies an issuer outage sends the
// browser back to the login page instead of rendering an error body.
func TestOIDCStartRedirectsWhenIssuerUnreachable(t *testing.T) {
	f := newFlakyIssuer(t)
	f.down.Store(true)
	p := &oidcProvider{
		issuer: &oidcIssuer{url: f.url},
		oauth:  oauth2.Config{ClientID: testClientID},
		auth:   newAuthenticator("", nil, []byte("test-signing-key-do-not-use-in-prod")),
	}
	mgr := &authManager{oidc: p}

	r := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/start", nil)
	w := httptest.NewRecorder()
	handleOIDCStart(mgr).ServeHTTP(w, r)

	require.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/?sso_error="+ssoErrorUnavailable, w.Header().Get("Location"))
	assert.Empty(t, w.Result().Cookies(), "no flow starts, so no state cookie")

	// Once the issuer is up, the next sign-in after the retry interval starts the flow.
	f.down.Store(false)
	p.issuer.failedAt = p.issuer.failedAt.Add(-oidcDiscoveryRetryInterval)
	w = httptest.NewRecorder()
	handleOIDCStart(mgr).ServeHTTP(w, r)
	require.Equal(t, http.StatusFound, w.Code)
	assert.True(t, strings.HasPrefix(w.Header().Get("Location"), f.url), "the flow must redirect to the issuer")
}

// TestOIDCCallbackRedirectsWhenIssuerUnreachable verifies the callback sends the
// browser back to the login page when the issuer cannot be discovered.
func TestOIDCCallbackRedirectsWhenIssuerUnreachable(t *testing.T) {
	f := newFlakyIssuer(t)
	f.down.Store(true)
	mgr := &authManager{oidc: &oidcProvider{
		issuer: &oidcIssuer{url: f.url},
		oauth:  oauth2.Config{ClientID: testClientID},
	}}

	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/callback?state=s&code=c", nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "s.verifier"})
	rec := httptest.NewRecorder()
	handleOIDCCallback(mgr).ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/?sso_error="+ssoErrorUnavailable, rec.Header().Get("Location"))
}

// TestAuthManagerReloadKeepsDiscoveredIssuer verifies a reload with the same
// issuer keeps its discovery, and a new issuer starts over.
func TestAuthManagerReloadKeepsDiscoveredIssuer(t *testing.T) {
	client := fake.NewClientBuilder().WithObjects(&corev1.Secret{
		Name: deliveryv1alpha1.DefaultTokenSigningKeySecretName, Namespace: testNS,
		Data: map[string][]byte{secretKeySigningKey: []byte("test-signing-key-do-not-use-in-prod")},
	}, &corev1.Secret{
		Name: testOIDCSecret, Namespace: testNS,
		Data: map[string][]byte{secretKeyClientSecret: []byte(testClientSec)},
	}).Build()
	m := &authManager{reader: client, apiReader: client, ns: testNS, logger: logr.Discard()}
	kitchen := func(issuerURL string, scopes ...string) *deliveryv1alpha1.Kitchen {
		return &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNS,
			Spec: deliveryv1alpha1.KitchenSpec{Auth: &deliveryv1alpha1.KitchenAuth{
				OIDC: &deliveryv1alpha1.OIDCConfig{
					IssuerURL:       issuerURL,
					ClientID:        testClientID,
					ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
					Scopes:          scopes,
				},
			}},
		}
	}
	f := newFlakyIssuer(t)
	ctx := context.Background()

	m.reload(ctx, kitchen(f.url))
	first := m.getOIDC().issuer
	_, err := first.discover(ctx)
	require.NoError(t, err)

	// The issuer goes down; a reload with the same issuer keeps the discovery,
	// and takes the rest of the new config.
	f.down.Store(true)
	m.reload(ctx, kitchen(f.url, "openid", "groups"))
	assert.Same(t, first, m.getOIDC().issuer)
	assert.Equal(t, []string{"openid", "groups"}, m.getOIDC().oauth.Scopes)
	_, err = m.getOIDC().issuer.discover(ctx)
	require.NoError(t, err, "sign-in must keep working while the issuer is down")

	m.reload(ctx, kitchen("https://other-issuer.example"))
	assert.NotSame(t, first, m.getOIDC().issuer, "a new issuer must be discovered on its own")
}

// newFakeIssuer starts an OIDC provider whose token endpoint answers every
// code with a signed ID token for user@example.com. With endSession it
// advertises RP-initiated logout at /logout.
func newFakeIssuer(t *testing.T, endSession bool) *httptest.Server {
	t.Helper()
	return newFakeProvider(t, func(p *fakeProvider) { p.endSession = endSession }).Server
}

// signInOIDC runs the sign-in flow against mgr with the browser's cookies
// and returns the cookies the callback sets.
func signInOIDC(t *testing.T, mgr *authManager, cookies ...*http.Cookie) []*http.Cookie {
	t.Helper()
	start := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/start", nil)
	for _, c := range cookies {
		start.AddCookie(c)
	}
	startRec := httptest.NewRecorder()
	handleOIDCStart(mgr).ServeHTTP(startRec, start)
	require.Equal(t, http.StatusFound, startRec.Code)
	loc, err := url.Parse(startRec.Header().Get("Location"))
	require.NoError(t, err)

	cb := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/callback?code=c&state="+loc.Query().Get("state"), nil)
	cb.AddCookie(cookieNamed(t, startRec.Result().Cookies(), oidcStateCookie))
	cbRec := httptest.NewRecorder()
	handleOIDCCallback(mgr).ServeHTTP(cbRec, cb)
	require.Equal(t, http.StatusFound, cbRec.Code, cbRec.Body.String())
	assert.Contains(t, cbRec.Header().Get("Location"), "/#access_token=")
	return cbRec.Result().Cookies()
}

// signOut posts a sign-out with cookies and returns the response.
func signOut(mgr *authManager, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/auth/logout", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handleLogout(mgr).ServeHTTP(w, r)
	return w
}

func TestOIDCSignOutEndsTheSessionAtTheProvider(t *testing.T) {
	issuer := newFakeIssuer(t, true)
	p := newOIDCProviderFor(t, issuer.URL, deliveryv1alpha1.ProviderSignOutAlways)
	mgr := &authManager{auth: p.auth, oidc: p, logger: logr.Discard()}

	signedIn := signInOIDC(t, mgr)
	refresh := cookieNamed(t, signedIn, refreshCookieName)
	assert.InDelta(t, defaultSessionIdleTTL.Seconds(), refresh.MaxAge, 2, "the refresh cookie lasts until the idle deadline")
	idToken := cookieNamed(t, signedIn, oidcIDTokenCookie)
	assert.Equal(t, oidcIDTokenCookiePath, idToken.Path, "only the sign-out reads the ID token")
	assert.True(t, idToken.HttpOnly)

	w := signOut(mgr, refresh, idToken)
	require.Equal(t, http.StatusOK, w.Code)
	var resp logoutResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	endSession, err := url.Parse(resp.EndSessionURL)
	require.NoError(t, err)
	assert.Equal(t, issuer.URL+"/logout", endSession.Scheme+"://"+endSession.Host+endSession.Path)
	assert.Equal(t, url.Values{
		"id_token_hint":            {idToken.Value},
		"client_id":                {testClientID},
		"post_logout_redirect_uri": {"http://localhost:8080/"},
	}, endSession.Query())

	cookies := w.Result().Cookies()
	assert.Equal(t, -1, cookieNamed(t, cookies, refreshCookieName).MaxAge)
	assert.Equal(t, -1, cookieNamed(t, cookies, oidcIDTokenCookie).MaxAge)
	assert.Positive(t, cookieNamed(t, cookies, oidcReauthCookie).MaxAge,
		"the next sign-in asks for credentials, in case the user cancels the sign-out at the provider")
}

func TestOIDCSignOutWithoutProviderLogoutAsksForReauthentication(t *testing.T) {
	issuer := newFakeIssuer(t, false)
	p := newOIDCProviderFor(t, issuer.URL, deliveryv1alpha1.ProviderSignOutAlways)
	mgr := &authManager{auth: p.auth, oidc: p, logger: logr.Discard()}

	start := httptest.NewRecorder()
	handleOIDCStart(mgr).ServeHTTP(start, httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/start", nil))
	assert.NotContains(t, start.Header().Get("Location"), "max_age=", "a first sign-in uses the provider's session")

	// An ID token too large for a cookie leaves only the refresh cookie to
	// tell the session came from OIDC.
	refresh := cookieNamed(t, signInOIDC(t, mgr), refreshCookieName)
	w := signOut(mgr, refresh)
	require.Equal(t, http.StatusNoContent, w.Code, "the provider offers no end_session_endpoint")
	reauth := cookieNamed(t, w.Result().Cookies(), oidcReauthCookie)
	assert.Equal(t, oidcCookiePath, reauth.Path)

	start = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/start", nil)
	r.AddCookie(reauth)
	handleOIDCStart(mgr).ServeHTTP(start, r)
	loc, err := url.Parse(start.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "0", loc.Query().Get("max_age"))
	assert.False(t, loc.Query().Has("prompt"), "prompt=login is refused by some providers")

	assert.Equal(t, -1, cookieNamed(t, signInOIDC(t, mgr, reauth), oidcReauthCookie).MaxAge,
		"signing in again is done with the sign-out")
}

func TestOIDCSignOutNeverLeavesTheProviderSession(t *testing.T) {
	issuer := newFakeIssuer(t, true)
	p := newOIDCProviderFor(t, issuer.URL, deliveryv1alpha1.ProviderSignOutNever)
	mgr := &authManager{auth: p.auth, oidc: p, logger: logr.Discard()}

	signedIn := signInOIDC(t, mgr)
	w := signOut(mgr, cookieNamed(t, signedIn, refreshCookieName), cookieNamed(t, signedIn, oidcIDTokenCookie))
	assert.Equal(t, http.StatusNoContent, w.Code, "no end-session URL, though the provider offers one")
	cookies := w.Result().Cookies()
	assert.Equal(t, -1, cookieNamed(t, cookies, refreshCookieName).MaxAge)
	assert.Equal(t, -1, cookieNamed(t, cookies, oidcIDTokenCookie).MaxAge)
	assert.Nil(t, findCookie(cookies, oidcReauthCookie), "the next sign-in uses the provider's session")
}

func TestOIDCCallbackErrorEndsReauthentication(t *testing.T) {
	issuer := newFakeIssuer(t, false)
	p := newOIDCProviderFor(t, issuer.URL, deliveryv1alpha1.ProviderSignOutAlways)
	mgr := &authManager{auth: p.auth, oidc: p, logger: logr.Discard()}

	r := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/oidc/callback?error=invalid_request", nil)
	r.AddCookie(&http.Cookie{Name: oidcReauthCookie, Value: "1"})
	w := httptest.NewRecorder()
	handleOIDCCallback(mgr).ServeHTTP(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, -1, cookieNamed(t, w.Result().Cookies(), oidcReauthCookie).MaxAge,
		"a provider that refuses the request must not refuse every later sign-in")
}

func TestAdminSignOutStaysLocalWithOIDCConfigured(t *testing.T) {
	issuer := newFakeIssuer(t, true)
	p := newOIDCProviderFor(t, issuer.URL, deliveryv1alpha1.ProviderSignOutAlways)
	mgr := &authManager{auth: p.auth, oidc: p, logger: logr.Discard()}
	s, err := p.auth.startSession(&Identity{Subject: testUsername, Provider: providerAdmin}, newSessionState(time.Now()))
	require.NoError(t, err)

	w := signOut(mgr, &http.Cookie{Name: refreshCookieName, Value: s.RefreshToken})
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Nil(t, findCookie(w.Result().Cookies(), oidcReauthCookie))
}
