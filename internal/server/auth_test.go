package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/namespace"
)

const (
	testUsername   = "admin"
	testPassword   = "s3cret-passw0rd"
	testNS         = "kokumi"
	testSecret     = "kokumi-server-auth"
	testRootUser   = "root"
	testOrdersPath = "/api/v1/orders"
	testToken      = "abc.def.ghi"

	// unrelatedSecretKey is a Secret key Kokumi does not read.
	unrelatedSecretKey = "other"
)

// testSigningKey signs the tokens of test sessions.
var testSigningKey = []byte("test-signing-key-do-not-use-in-prod")

// newTestAuthenticator builds an authenticator with a bcrypt hash of testPassword.
func newTestAuthenticator(t *testing.T) *authenticator {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	require.NoError(t, err)
	return newAuthenticator(testUsername, hash, testSigningKey)
}

// newTokenAuthenticator builds a token-only authenticator with
// testSigningKey, as for an OIDC-only server.
func newTokenAuthenticator() *authenticator {
	return newAuthenticator("", nil, testSigningKey)
}

// testTokenSecret returns the default token signing-key Secret holding
// testSigningKey.
func testTokenSecret() *corev1.Secret {
	return &corev1.Secret{
		Name: deliveryv1alpha1.DefaultTokenSigningKeySecretName, Namespace: testNS,
		Data: map[string][]byte{secretKeySigningKey: testSigningKey},
	}
}

func newAuthSecret(data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		Name: testSecret, Namespace: testNS,
		Data: data,
	}
}

func TestReadAdminCredentials(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	require.NoError(t, err)
	c := fake.NewClientBuilder().WithObjects(
		newAuthSecret(map[string][]byte{secretKeyPasswordHash: hash}),
		&corev1.Secret{Name: "incomplete", Namespace: testNS, Data: map[string][]byte{unrelatedSecretKey: []byte("x")}},
	).Build()
	read := func(cfg *deliveryv1alpha1.AdminUserConfig) (*adminCredentials, error) {
		return readAdminCredentials(context.Background(), c, testNS, cfg)
	}

	t.Run("custom username from the Kitchen", func(t *testing.T) {
		admin, err := read(&deliveryv1alpha1.AdminUserConfig{
			Username:  testRootUser,
			SecretRef: &corev1.LocalObjectReference{Name: testSecret},
		})
		require.NoError(t, err)
		auth := newAuthenticator("", nil, []byte("test-signing-key-at-least-32-bytes-long!!")).withAdmin(admin)
		assert.True(t, auth.verifyCredentials(testRootUser, testPassword))
	})

	t.Run("missing Secret has no credentials", func(t *testing.T) {
		_, err := read(&deliveryv1alpha1.AdminUserConfig{SecretRef: &corev1.LocalObjectReference{Name: "absent"}})
		require.ErrorIs(t, err, errNoAdminCredentials)
		assert.Contains(t, err.Error(), "absent")
	})

	t.Run("Secret without password hash has no credentials", func(t *testing.T) {
		_, err := read(&deliveryv1alpha1.AdminUserConfig{SecretRef: &corev1.LocalObjectReference{Name: "incomplete"}})
		require.ErrorIs(t, err, errNoAdminCredentials)
		assert.Contains(t, err.Error(), secretKeyPasswordHash)
	})
}

func TestBuildTokenAuthenticator(t *testing.T) {
	t.Run("token-only authenticator from signing-key Secret", func(t *testing.T) {
		signingOnly := &corev1.Secret{
			Name: deliveryv1alpha1.DefaultTokenSigningKeySecretName, Namespace: testNS,
			Data: map[string][]byte{secretKeySigningKey: []byte("test-signing-key-at-least-32-bytes-long!!")},
		}
		cr := fake.NewClientBuilder().WithObjects(signingOnly).Build()
		auth, err := buildTokenAuthenticator(context.Background(), cr, testNS, deliveryv1alpha1.DefaultTokenSigningKeySecretName, tokenLifetimes{})
		require.NoError(t, err)
		require.NotNil(t, auth)
		assert.Empty(t, auth.username)
		assert.Empty(t, auth.passwordHash)
		assert.NotEmpty(t, auth.signingKey)
	})

	t.Run("missing signing-key Secret errors", func(t *testing.T) {
		cr := fake.NewClientBuilder().Build()
		_, err := buildTokenAuthenticator(context.Background(), cr, testNS, deliveryv1alpha1.DefaultTokenSigningKeySecretName, tokenLifetimes{})
		require.Error(t, err)
	})

	t.Run("signing-key Secret without key errors", func(t *testing.T) {
		incomplete := &corev1.Secret{
			Name: deliveryv1alpha1.DefaultTokenSigningKeySecretName, Namespace: testNS,
			Data: map[string][]byte{unrelatedSecretKey: []byte("x")},
		}
		cr := fake.NewClientBuilder().WithObjects(incomplete).Build()
		_, err := buildTokenAuthenticator(context.Background(), cr, testNS, deliveryv1alpha1.DefaultTokenSigningKeySecretName, tokenLifetimes{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), secretKeySigningKey)
	})
}

func TestVerifyCredentials(t *testing.T) {
	auth := newTestAuthenticator(t)

	tests := []struct {
		name     string
		username string
		password string
		want     bool
	}{
		{"correct credentials", testUsername, testPassword, true},
		{"wrong password", testUsername, "nope", false},
		{"wrong username", testRootUser, testPassword, false},
		{"both wrong", testRootUser, "nope", false},
		{"empty password", testUsername, "", false},
		{"empty username", "", testPassword, false},
		{"username case sensitive", "Admin", testPassword, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, auth.verifyCredentials(tt.username, tt.password))
		})
	}
}

// TestVerifyCredentialsHtpasswdHash locks in compatibility with htpasswd -B ($2y$) hashes.
func TestVerifyCredentialsHtpasswdHash(t *testing.T) {
	// Produced by: htpasswd -nbB admin admin  (password = "admin")
	const htpasswdHash = "$2y$05$MG5FZ/WlMewHp8kwaoZixeQ8NCXjhp7ZWwx1N40pQ6oc3VfL9Xu0y"
	const testAdminUser = "admin"
	auth := &authenticator{
		username:     testAdminUser,
		passwordHash: []byte(htpasswdHash),
		signingKey:   []byte("key"),
		accessTTL:    defaultAccessTokenTTL,
	}

	assert.True(t, auth.verifyCredentials("admin", "admin"),
		"htpasswd -B ($2y$) hash must validate the correct password")
	assert.False(t, auth.verifyCredentials("admin", "wrong"),
		"htpasswd -B ($2y$) hash must reject a wrong password")
}

func TestIssueAndParseSession(t *testing.T) {
	auth := newTestAuthenticator(t)
	// Tokens carry times in whole seconds.
	now := time.Now().Truncate(time.Second)
	id := &Identity{Subject: testUsername, Provider: providerAdmin}
	state := newSessionState(now)
	state.ProviderCheckAt = now.Add(time.Minute)

	s, err := auth.issueTokens(now, id, state, now.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, now.Add(defaultAccessTokenTTL), s.accessExpiresAt)
	assert.Equal(t, now.Add(time.Hour), s.refreshExpiresAt)

	access, err := auth.parseToken(s.accessToken, accessTokenType)
	require.NoError(t, err)
	assert.Equal(t, id, access.identity)
	assert.Equal(t, state, access.session)
	assert.Equal(t, s.accessExpiresAt, access.expires)

	refresh, err := auth.parseToken(s.refreshToken, refreshTokenType)
	require.NoError(t, err)
	assert.Equal(t, id, refresh.identity)
	assert.Equal(t, state, refresh.session)
	assert.Equal(t, s.refreshExpiresAt, refresh.expires)
}

func TestIssueSessionRequiresSubject(t *testing.T) {
	_, err := newTestAuthenticator(t).signInTokens(&Identity{Provider: providerOIDC}, newSessionState(time.Now()))
	assert.Error(t, err)
}

// TestParseTokenRejectsInvalidTokens runs the parser the auth middleware
// checks access tokens with.
func TestParseTokenRejectsInvalidTokens(t *testing.T) {
	auth := newTestAuthenticator(t)
	now := time.Now()
	id := &Identity{Subject: testUsername, Provider: providerAdmin}
	// valid returns the claims of a valid access token, for a case to break.
	valid := func() identityClaims {
		return newIdentityClaims(jwt.RegisteredClaims{
			Subject:   testUsername,
			Issuer:    tokenIssuer,
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		}, accessTokenType, id, newSessionState(now))
	}
	sign := func(t *testing.T, method jwt.SigningMethod, key any, claims identityClaims) string {
		t.Helper()
		signed, err := jwt.NewWithClaims(method, claims).SignedString(key)
		require.NoError(t, err)
		return signed
	}
	parse := func(token string) error {
		_, err := auth.parseToken(token, accessTokenType)
		return err
	}

	t.Run("the token the cases break is valid", func(t *testing.T) {
		assert.NoError(t, parse(sign(t, jwt.SigningMethodHS256, auth.signingKey, valid())))
	})

	t.Run("garbage string", func(t *testing.T) {
		assert.Error(t, parse("not-a-jwt"))
	})

	t.Run("signed with a different key", func(t *testing.T) {
		assert.Error(t, parse(sign(t, jwt.SigningMethodHS256, []byte("a-totally-different-key"), valid())),
			"token signed with another key must be rejected")
	})

	t.Run("expired token", func(t *testing.T) {
		signedIn := now.Add(-2 * defaultAccessTokenTTL)
		s, err := auth.issueTokens(signedIn, id, newSessionState(signedIn), now.Add(time.Hour))
		require.NoError(t, err)
		assert.ErrorIs(t, parse(s.accessToken), jwt.ErrTokenExpired)
	})

	t.Run("no expiry", func(t *testing.T) {
		claims := valid()
		claims.ExpiresAt = nil
		assert.Error(t, parse(sign(t, jwt.SigningMethodHS256, auth.signingKey, claims)))
	})

	t.Run("wrong issuer", func(t *testing.T) {
		claims := valid()
		claims.Issuer = "someone-else"
		assert.Error(t, parse(sign(t, jwt.SigningMethodHS256, auth.signingKey, claims)),
			"token with unexpected issuer must be rejected")
	})

	t.Run("alg none is rejected", func(t *testing.T) {
		assert.Error(t, parse(sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, valid())),
			"alg=none must never be accepted")
	})

	t.Run("another HMAC algorithm is rejected", func(t *testing.T) {
		assert.Error(t, parse(sign(t, jwt.SigningMethodHS512, auth.signingKey, valid())))
	})

	t.Run("a refresh token is not an access token", func(t *testing.T) {
		s, err := auth.issueTokens(now, id, newSessionState(now), now.Add(time.Hour))
		require.NoError(t, err)
		assert.Error(t, parse(s.refreshToken))
	})

	t.Run("no subject", func(t *testing.T) {
		claims := valid()
		claims.Subject = ""
		assert.Error(t, parse(sign(t, jwt.SigningMethodHS256, auth.signingKey, claims)))
	})

	t.Run("no session ID", func(t *testing.T) {
		claims := valid()
		claims.SessionID = ""
		assert.Error(t, parse(sign(t, jwt.SigningMethodHS256, auth.signingKey, claims)))
	})

	t.Run("no sign-in time, as in a token from before sessions had one", func(t *testing.T) {
		claims := valid()
		claims.AuthTime = nil
		assert.Error(t, parse(sign(t, jwt.SigningMethodHS256, auth.signingKey, claims)))
	})
}

func TestRequiresAuth(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{testOrdersPath, true},
		{"/api/v1/products/default/foo", true},
		{"/api/v1/events", true},
		{"/api/v1/auth/login", false},
		{"/api/v1/info", false},
		{"/healthz", false},
		{"/readyz", false},
		{"/", false},
		{"/assets/index.js", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.want, requiresAuth(tt.path))
		})
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"valid bearer", "Bearer " + testToken, testToken},
		{"case-insensitive scheme", "bearer " + testToken, testToken},
		{"trims surrounding space", "Bearer   abc  ", "abc"},
		{"empty header", "", ""},
		{"no scheme", testToken, ""},
		{"wrong scheme", "Basic abc", ""},
		{"scheme only", "Bearer ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, testOrdersPath, nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			assert.Equal(t, tt.want, bearerToken(r))
		})
	}
}

func TestBearerTokenFromQueryParam(t *testing.T) {
	t.Run("reads access_token query param", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/events?access_token=qtoken", nil)
		assert.Equal(t, "qtoken", bearerToken(r))
	})

	t.Run("header takes precedence over query param", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/events?access_token=qtoken", nil)
		r.Header.Set("Authorization", "Bearer htoken")
		assert.Equal(t, "htoken", bearerToken(r))
	})
}

func TestMiddleware(t *testing.T) {
	auth := newTestAuthenticator(t)
	session, err := auth.signInTokens(&Identity{Subject: testUsername, Provider: providerAdmin}, newSessionState(time.Now()))
	require.NoError(t, err)

	// next records whether it was invoked so we can assert pass-through.
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	mgr := &authManager{auth: auth}
	handler := mgr.middleware(next)

	tests := []struct {
		name       string
		path       string
		authHeader string
		wantStatus int
		wantReach  bool
	}{
		{"public info passes without token", "/api/v1/info", "", http.StatusOK, true},
		{"login passes without token", "/api/v1/auth/login", "", http.StatusOK, true},
		{"static asset passes without token", "/assets/app.js", "", http.StatusOK, true},
		{"health passes without token", "/healthz", "", http.StatusOK, true},
		{"protected without token is rejected", testOrdersPath, "", http.StatusUnauthorized, false},
		{"protected with bad token is rejected", testOrdersPath, "Bearer bad", http.StatusUnauthorized, false},
		{"protected with a refresh token is rejected", testOrdersPath, "Bearer " + session.refreshToken, http.StatusUnauthorized, false},
		{"protected with valid token passes", testOrdersPath, "Bearer " + session.accessToken, http.StatusOK, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reached = false
			r := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.authHeader != "" {
				r.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assert.Equal(t, tt.wantStatus, w.Code)
			assert.Equal(t, tt.wantReach, reached)
		})
	}
}

func TestHandleLogin(t *testing.T) {
	auth := newTestAuthenticator(t)
	handler := handleLogin(&authManager{auth: auth, adminLogin: true})

	t.Run("successful login returns a usable token", func(t *testing.T) {
		body := strings.NewReader(`{"username":"admin","password":"s3cret-passw0rd"}`)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		var resp loginResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp.Token)
		assert.True(t, resp.ExpiresAt.After(time.Now()))

		// The issued token must validate against the same authenticator.
		got, err := auth.parseToken(resp.Token, accessTokenType)
		require.NoError(t, err)
		assert.Equal(t, &Identity{Subject: testUsername, Provider: providerAdmin}, got.identity)
	})

	t.Run("wrong password is rejected", func(t *testing.T) {
		body := strings.NewReader(`{"username":"admin","password":"wrong"}`)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.NotContains(t, w.Body.String(), "token")
	})

	t.Run("malformed body is rejected", func(t *testing.T) {
		body := strings.NewReader(`{not json`)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestCurrentNamespace(t *testing.T) {
	t.Run("prefers POD_NAMESPACE", func(t *testing.T) {
		getenv := func(k string) string {
			if k == "POD_NAMESPACE" {
				return "custom-ns"
			}
			return ""
		}
		assert.Equal(t, "custom-ns", namespace.Current(getenv))
	})

	t.Run("falls back to default when unset", func(t *testing.T) {
		// Exercises the final default branch (no SA file in test env).
		assert.Equal(t, namespace.Default, namespace.Current(func(string) string { return "" }))
	})

	t.Run("trims whitespace", func(t *testing.T) {
		getenv := func(k string) string {
			if k == "POD_NAMESPACE" {
				return "  spaced  "
			}
			return ""
		}
		assert.Equal(t, "spaced", namespace.Current(getenv))
	})
}

func TestHandleLoginSetsRefreshCookie(t *testing.T) {
	auth := newTestAuthenticator(t)
	handler := handleLogin(&authManager{auth: auth, adminLogin: true})

	body := strings.NewReader(`{"username":"admin","password":"s3cret-passw0rd"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	refresh := cookieNamed(t, w.Result().Cookies(), refreshCookieName)
	assert.True(t, refresh.HttpOnly)
	assert.NotEmpty(t, refresh.Value)
	assert.InDelta(t, defaultSessionIdleTTL.Seconds(), refresh.MaxAge, 2, "the cookie lasts until the idle deadline")

	// The refresh cookie value must validate as a refresh token.
	_, err := auth.parseToken(refresh.Value, refreshTokenType)
	assert.NoError(t, err)
	assert.Equal(t, -1, cookieNamed(t, w.Result().Cookies(), oidcIDTokenCookie).MaxAge,
		"an ID token of an earlier OIDC session is dropped")
}

func TestHandleRefresh(t *testing.T) {
	auth := newTestAuthenticator(t)
	loginHandler := handleLogin(&authManager{auth: auth, adminLogin: true})
	refreshHandler := handleRefresh(&authManager{auth: auth, adminLogin: true})

	// Log in to obtain a refresh cookie.
	loginBody := strings.NewReader(`{"username":"admin","password":"s3cret-passw0rd"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", loginBody)
	loginRec := httptest.NewRecorder()
	loginHandler.ServeHTTP(loginRec, loginReq)
	require.Equal(t, http.StatusOK, loginRec.Code)

	refreshCookie := cookieNamed(t, loginRec.Result().Cookies(), refreshCookieName)

	t.Run("valid refresh cookie issues a new access token and rotates the cookie", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
		r.AddCookie(refreshCookie)
		w := httptest.NewRecorder()

		refreshHandler.ServeHTTP(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		var resp loginResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp.Token)
		_, err := auth.parseToken(resp.Token, accessTokenType)
		assert.NoError(t, err)

		// The refresh cookie is set again with a refresh token of the same
		// session; within the same second, with the same claims, it may be
		// the same token.
		newCookies := w.Result().Cookies()
		require.Len(t, newCookies, 1)
		rotated, err := auth.parseToken(newCookies[0].Value, refreshTokenType)
		require.NoError(t, err)
		session, err := auth.parseToken(refreshCookie.Value, refreshTokenType)
		require.NoError(t, err)
		assert.Equal(t, session.session, rotated.session)
	})

	t.Run("missing refresh cookie is rejected", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
		w := httptest.NewRecorder()

		refreshHandler.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		// The (absent) cookies of the session are cleared.
		cookies := w.Result().Cookies()
		assert.Equal(t, -1, cookieNamed(t, cookies, refreshCookieName).MaxAge)
		assert.Equal(t, -1, cookieNamed(t, cookies, providerTokenCookie).MaxAge)
		assert.Equal(t, -1, cookieNamed(t, cookies, oidcIDTokenCookie).MaxAge)
	})

	t.Run("a refresh without a body is a background one", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
		r.AddCookie(refreshCookie)
		w := httptest.NewRecorder()
		refreshHandler.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		rotated, err := auth.parseToken(cookieNamed(t, w.Result().Cookies(), refreshCookieName).Value, refreshTokenType)
		require.NoError(t, err)
		session, err := auth.parseToken(refreshCookie.Value, refreshTokenType)
		require.NoError(t, err)
		assert.Equal(t, session.expires, rotated.expires, "the idle deadline stays")
	})

	t.Run("a malformed body is refused", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{not json`))
		r.AddCookie(refreshCookie)
		w := httptest.NewRecorder()
		refreshHandler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("access token cannot be used as a refresh token", func(t *testing.T) {
		// Extract the access token from the login response body.
		var loginResp loginResponse
		require.NoError(t, json.Unmarshal(loginRec.Body.Bytes(), &loginResp))

		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
		r.AddCookie(&http.Cookie{Name: refreshCookieName, Value: loginResp.Token})
		w := httptest.NewRecorder()

		refreshHandler.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestHandleLogoutClearsCookie(t *testing.T) {
	auth := newTestAuthenticator(t)
	handler := handleLogout(&authManager{auth: auth})

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, -1, cookieNamed(t, w.Result().Cookies(), refreshCookieName).MaxAge)
}

func TestHandleInfoReportsAuthProviders(t *testing.T) {
	auth := newTestAuthenticator(t)
	handler := handleInfo(&authManager{auth: auth, adminLogin: true})

	r := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp InfoResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp.AuthProviders, "admin")
}

func TestHandleInfoNoProvidersMeansLoginPage(t *testing.T) {
	handler := handleInfo(nil)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp InfoResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.AuthProviders)
}

func TestBuildTokenAuthenticatorAppliesTokenTTL(t *testing.T) {
	tokenSecret := &corev1.Secret{
		Name: deliveryv1alpha1.DefaultTokenSigningKeySecretName, Namespace: testNS,
		Data: map[string][]byte{secretKeySigningKey: []byte("test-signing-key-at-least-32-bytes-long!!")},
	}
	reader := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(tokenSecret).Build()

	t.Run("KOKUMI_TOKEN_TTL overrides the access token lifetime", func(t *testing.T) {
		auth, err := buildTokenAuthenticator(context.Background(), reader, testNS, deliveryv1alpha1.DefaultTokenSigningKeySecretName, tokenLifetimes{access: 30 * time.Minute})
		require.NoError(t, err)
		require.NotNil(t, auth)
		assert.Equal(t, 30*time.Minute, auth.accessTTL)
	})

	t.Run("KOKUMI_SESSION_TTL overrides the session lifetime", func(t *testing.T) {
		auth, err := buildTokenAuthenticator(context.Background(), reader, testNS, deliveryv1alpha1.DefaultTokenSigningKeySecretName, tokenLifetimes{session: 8 * time.Hour})
		require.NoError(t, err)
		require.NotNil(t, auth)
		assert.Equal(t, 8*time.Hour, auth.sessionTTL)
	})

	t.Run("KOKUMI_SESSION_IDLE_TTL overrides the idle limit", func(t *testing.T) {
		auth, err := buildTokenAuthenticator(context.Background(), reader, testNS, deliveryv1alpha1.DefaultTokenSigningKeySecretName, tokenLifetimes{idle: 15 * time.Minute})
		require.NoError(t, err)
		require.NotNil(t, auth)
		assert.Equal(t, 15*time.Minute, auth.idleTTL)
	})

	t.Run("default TTL when env unset", func(t *testing.T) {
		auth, err := buildTokenAuthenticator(context.Background(), reader, testNS, deliveryv1alpha1.DefaultTokenSigningKeySecretName, tokenLifetimes{})
		require.NoError(t, err)
		require.NotNil(t, auth)
		assert.Equal(t, defaultAccessTokenTTL, auth.accessTTL)
		assert.Equal(t, defaultSessionTTL, auth.sessionTTL)
	})
}

// authKitchen returns the Kitchen singleton with auth.
func authKitchen(auth *deliveryv1alpha1.KitchenAuth) *deliveryv1alpha1.Kitchen {
	return &deliveryv1alpha1.Kitchen{
		Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNS,
		Spec: deliveryv1alpha1.KitchenSpec{Auth: ptr.Deref(auth, deliveryv1alpha1.KitchenAuth{})},
	}
}

func TestAuthManagerAdminCredentials(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	require.NoError(t, err)
	tokenSecret := func() *corev1.Secret {
		return &corev1.Secret{
			Name: deliveryv1alpha1.DefaultTokenSigningKeySecretName, Namespace: testNS,
			Data: map[string][]byte{secretKeySigningKey: []byte("test-signing-key-at-least-32-bytes-long!!")},
		}
	}
	adminSecret := func() *corev1.Secret {
		return newAuthSecret(map[string][]byte{secretKeyPasswordHash: hash})
	}
	admin := &deliveryv1alpha1.AdminUserConfig{
		Enabled:   new(true),
		Username:  testUsername,
		SecretRef: &corev1.LocalObjectReference{Name: testSecret},
	}
	newManager := func(c client.Reader) *authManager {
		return &authManager{reader: c, apiReader: c, ns: testNS, logger: logr.Discard()}
	}
	canSignIn := func(m *authManager) bool {
		a := m.get()
		return m.adminLoginEnabled() && a != nil && a.verifyCredentials(testUsername, testPassword)
	}
	// adminSession signs in to the admin account of m and returns the access
	// token of the session.
	adminSession := func(t *testing.T, m *authManager) string {
		t.Helper()
		res, err := m.signInAdmin(testUsername, testPassword)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, protectedRequest(m, res.tokens.accessToken))
		return res.tokens.accessToken
	}

	t.Run("a missing admin Secret leaves OIDC and tokens working", func(t *testing.T) {
		c := fake.NewClientBuilder().WithObjects(tokenSecret(), &corev1.Secret{
			Name: testOIDCSecret, Namespace: testNS,
			Data: map[string][]byte{secretKeyClientSecret: []byte(testClientSec)},
		}).Build()
		m := newManager(c)
		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{
			AdminUser: *admin,
			OIDC: deliveryv1alpha1.OIDCConfig{
				IssuerURL: testIssuerURL, ClientID: testClientID,
				ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
			},
		}))
		assert.Equal(t, []string{providerOIDC}, m.providers())
		assert.NotNil(t, m.get(), "tokens must still verify")
	})

	t.Run("deleting the admin Secret stops admin login", func(t *testing.T) {
		secret := adminSecret()
		c := fake.NewClientBuilder().WithObjects(tokenSecret(), secret).Build()
		m := newManager(c)
		kitchen := authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: *admin})
		m.reload(context.Background(), kitchen)
		require.True(t, canSignIn(m))
		session := adminSession(t, m)

		require.NoError(t, c.Delete(context.Background(), secret))
		m.reload(context.Background(), kitchen)
		assert.False(t, canSignIn(m))
		assert.NotNil(t, m.get(), "tokens must still verify")
		assert.Equal(t, http.StatusUnauthorized, protectedRequest(m, session), "the running admin session ends")
	})

	t.Run("a new password ends running admin sessions", func(t *testing.T) {
		secret := adminSecret()
		c := fake.NewClientBuilder().WithObjects(tokenSecret(), secret).Build()
		m := newManager(c)
		kitchen := authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: *admin})
		m.reload(context.Background(), kitchen)
		session := adminSession(t, m)

		// Setting the same hash again changes nothing.
		m.reload(context.Background(), kitchen)
		assert.Equal(t, http.StatusOK, protectedRequest(m, session))

		secret.Data[secretKeyPasswordHash] = mustBcrypt(t, testPassword)
		require.NoError(t, c.Update(context.Background(), secret))
		m.reload(context.Background(), kitchen)
		assert.Equal(t, http.StatusUnauthorized, protectedRequest(m, session),
			"a new hash ends the session, also of the same password")
		adminSession(t, m)
	})

	t.Run("an admin Secret without a password hash stops admin login", func(t *testing.T) {
		secret := adminSecret()
		c := fake.NewClientBuilder().WithObjects(tokenSecret(), secret).Build()
		m := newManager(c)
		kitchen := authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: *admin})
		m.reload(context.Background(), kitchen)
		require.True(t, canSignIn(m))

		secret.Data = map[string][]byte{unrelatedSecretKey: []byte("x")}
		require.NoError(t, c.Update(context.Background(), secret))
		m.reload(context.Background(), kitchen)
		assert.False(t, canSignIn(m))
	})

	t.Run("a disabled admin account reads no Secret", func(t *testing.T) {
		var adminReads int
		c := fake.NewClientBuilder().WithObjects(tokenSecret(), &corev1.Secret{
			Name: testOIDCSecret, Namespace: testNS,
			Data: map[string][]byte{secretKeyClientSecret: []byte(testClientSec)},
		}).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if key.Name == testSecret {
					adminReads++
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
		m := newManager(c)
		disabled := *admin
		disabled.Enabled = new(false)
		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{
			AdminUser: disabled,
			OIDC: deliveryv1alpha1.OIDCConfig{
				IssuerURL: testIssuerURL, ClientID: testClientID,
				ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
			},
		}))
		assert.Equal(t, []string{providerOIDC}, m.providers())
		assert.Zero(t, adminReads)
	})

	t.Run("disabling the admin account stops admin login", func(t *testing.T) {
		c := fake.NewClientBuilder().WithObjects(tokenSecret(), adminSecret()).Build()
		m := newManager(c)
		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: *admin}))
		require.True(t, canSignIn(m))

		session := adminSession(t, m)

		disabled := *admin
		disabled.Enabled = new(false)
		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: disabled}))
		assert.False(t, canSignIn(m))
		assert.Equal(t, http.StatusUnauthorized, protectedRequest(m, session), "the running admin session ends")
	})

	t.Run("removing adminUser stops admin login", func(t *testing.T) {
		c := fake.NewClientBuilder().WithObjects(tokenSecret(), adminSecret()).Build()
		m := newManager(c)
		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: *admin}))
		require.True(t, canSignIn(m))

		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{}))
		assert.False(t, canSignIn(m))
	})

	t.Run("an unreadable admin Secret keeps the last credentials", func(t *testing.T) {
		var unreachable bool
		c := fake.NewClientBuilder().WithObjects(tokenSecret(), adminSecret()).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if unreachable && key.Name == testSecret {
					return errors.New("connection refused")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
		m := newManager(c)
		kitchen := authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: *admin})
		m.reload(context.Background(), kitchen)
		require.True(t, canSignIn(m))
		session := adminSession(t, m)

		unreachable = true
		m.reload(context.Background(), kitchen)
		assert.True(t, canSignIn(m))
		assert.Equal(t, http.StatusOK, protectedRequest(m, session), "the running admin session goes on")
	})

	t.Run("an unreadable token Secret keeps tokens and still stops admin login", func(t *testing.T) {
		var unreachable bool
		c := fake.NewClientBuilder().WithObjects(tokenSecret(), adminSecret()).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if unreachable && key.Name == deliveryv1alpha1.DefaultTokenSigningKeySecretName {
					return errors.New("connection refused")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
		m := newManager(c)
		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: *admin}))
		require.True(t, canSignIn(m))

		unreachable = true
		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{}))
		assert.False(t, canSignIn(m))
		assert.NotNil(t, m.get(), "tokens must still verify")
	})

	t.Run("the username comes from the Kitchen, not the Secret", func(t *testing.T) {
		c := fake.NewClientBuilder().WithObjects(tokenSecret(), newAuthSecret(map[string][]byte{
			secretKeyPasswordHash: hash,
			"username":            []byte(testRootUser),
		})).Build()
		m := newManager(c)
		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: deliveryv1alpha1.AdminUserConfig{
			Enabled:   new(true),
			SecretRef: &corev1.LocalObjectReference{Name: testSecret},
		}}))
		assert.True(t, canSignIn(m), "an unset username falls back to %q", testUsername)
	})

	t.Run("a new username ends running admin sessions", func(t *testing.T) {
		c := fake.NewClientBuilder().WithObjects(tokenSecret(), adminSecret()).Build()
		m := newManager(c)
		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: *admin}))
		session := adminSession(t, m)

		renamed := *admin
		renamed.Username = testRootUser
		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: renamed}))
		assert.Equal(t, http.StatusUnauthorized, protectedRequest(m, session))
	})
}

func TestPasswordFingerprint(t *testing.T) {
	hash, otherHash := mustBcrypt(t, testPassword), mustBcrypt(t, testPassword)
	fingerprint := passwordFingerprint(testSigningKey, hash)

	assert.Len(t, fingerprint, 16)
	assert.Equal(t, fingerprint, passwordFingerprint(testSigningKey, hash), "the same hash has the same fingerprint")
	assert.NotEqual(t, fingerprint, passwordFingerprint(testSigningKey, otherHash), "a new hash changes it")
	assert.NotEqual(t, fingerprint, passwordFingerprint([]byte("another-signing-key-of-32-bytes-or-more"), hash),
		"another signing key changes it")
	assert.Empty(t, passwordFingerprint(testSigningKey, nil), "without an admin account there is none")

	a := newAuthenticator(testUsername, hash, testSigningKey)
	tokens, err := a.signInTokens(&Identity{Subject: testUsername, Provider: providerAdmin}, newSessionState(time.Now()))
	require.NoError(t, err)
	claims := jwt.MapClaims{}
	_, _, err = jwt.NewParser().ParseUnverified(tokens.accessToken, claims)
	require.NoError(t, err)
	assert.Equal(t, fingerprint, claims["pwd"], "admin tokens carry it in the claim pwd")
	assert.NotContains(t, tokens.accessToken, string(hash))

	oidc, err := a.signInTokens(&Identity{Subject: testSubject, Provider: providerOIDC}, newSessionState(time.Now()))
	require.NoError(t, err)
	claims = jwt.MapClaims{}
	_, _, err = jwt.NewParser().ParseUnverified(oidc.accessToken, claims)
	require.NoError(t, err)
	assert.NotContains(t, claims, "pwd", "OIDC tokens carry none")
}

func TestAuthManagerWaitsForTheKitchen(t *testing.T) {
	c := fake.NewClientBuilder().WithObjects(testTokenSecret(), newAuthSecret(map[string][]byte{
		secretKeyPasswordHash: mustBcrypt(t, testPassword),
	})).Build()
	m := newAuthManager(t.Context(), c, c, testNS, tokenLifetimes{}, nil)
	session, err := newTestAuthenticator(t).signInTokens(
		&Identity{Subject: testUsername, Provider: providerAdmin}, newSessionState(time.Now()))
	require.NoError(t, err)

	assert.Nil(t, m.get(), "nothing is resolved before the Kitchen was read")
	assert.Equal(t, http.StatusServiceUnavailable, protectedRequest(m, session.accessToken),
		"the UI keeps its session and retries, instead of signing out")
	_, err = m.refreshSession(context.Background(), browserSession{refreshToken: session.refreshToken}, true)
	assert.ErrorIs(t, err, errAuthUnavailable)

	m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: deliveryv1alpha1.AdminUserConfig{
		Enabled: new(true), Username: testUsername, SecretRef: &corev1.LocalObjectReference{Name: testSecret},
	}}))
	res, err := m.signInAdmin(testUsername, testPassword)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, protectedRequest(m, res.tokens.accessToken))
}

func TestNoLoginMethodLocksTheAPI(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	for name, kitchen := range map[string]*deliveryv1alpha1.Kitchen{
		"no auth":     authKitchen(nil),
		"no provider": authKitchen(&deliveryv1alpha1.KitchenAuth{}),
	} {
		t.Run(name, func(t *testing.T) {
			reached = false
			c := fake.NewClientBuilder().Build()
			m := &authManager{reader: c, apiReader: c, ns: testNS, logger: logr.Discard()}
			m.reload(context.Background(), kitchen)
			assert.Empty(t, m.providers())

			w := httptest.NewRecorder()
			m.middleware(next).ServeHTTP(w, httptest.NewRequest(http.MethodGet, testOrdersPath, nil))
			assert.False(t, reached, "a protected path must not be reached")
			assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		})
	}
}

func TestIsHTTPS(t *testing.T) {
	t.Run("direct TLS request is https", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.TLS = &tls.ConnectionState{}
		assert.True(t, isHTTPS(r))
	})

	t.Run("X-Forwarded-Proto https is https", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Forwarded-Proto", "https")
		assert.True(t, isHTTPS(r))
	})

	t.Run("plain HTTP is not https", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		assert.False(t, isHTTPS(r))
	})

	t.Run("X-Forwarded-Proto http is not https", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Forwarded-Proto", "http")
		assert.False(t, isHTTPS(r))
	})
}

func TestCookiesAreSecureOverHTTPS(t *testing.T) {
	t.Run("Secure over HTTPS", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.TLS = &tls.ConnectionState{}
		w := httptest.NewRecorder()
		writeCookies(w, r, newCookie(refreshCookieName, "rt", refreshCookiePath, 60))

		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.True(t, cookies[0].Secure)
	})

	t.Run("not Secure over plain HTTP so Safari keeps it", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		w := httptest.NewRecorder()
		writeCookies(w, r, newCookie(refreshCookieName, "rt", refreshCookiePath, 60))

		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.False(t, cookies[0].Secure)
		assert.True(t, cookies[0].HttpOnly)
	})
}

// findCookie returns the cookie called name, or nil when none is set.
func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// cookieNamed returns the cookie called name, failing the test without one.
func cookieNamed(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	c := findCookie(cookies, name)
	if c == nil {
		t.Fatalf("no %s cookie in %v", name, cookies)
	}
	return c
}

func TestTokenLifetimesSurviveAdminCredentials(t *testing.T) {
	shared := newAuthenticator("", nil, []byte("test-signing-key-at-least-32-bytes-long!!"))
	shared.accessTTL, shared.sessionTTL = 15*time.Minute, 8*time.Hour

	for _, admin := range []*adminCredentials{{username: testUsername, passwordHash: []byte("hash")}, nil} {
		auth := shared.withAdmin(admin)
		assert.Equal(t, 15*time.Minute, auth.accessTTL, "KOKUMI_TOKEN_TTL must apply (admin=%t)", admin != nil)
		assert.Equal(t, 8*time.Hour, auth.sessionTTL, "KOKUMI_SESSION_TTL must apply (admin=%t)", admin != nil)
	}
	assert.Empty(t, shared.username, "the shared authenticator is not modified")
}

func TestServerRefusesCrossOriginRequests(t *testing.T) {
	handler := NewServer(&Config{}, newHub(), nil, &authManager{auth: newTestAuthenticator(t), adminLogin: true}, testNS)

	send := func(method, path, fetchSite string) int {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, nil)
		if fetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}

	assert.Equal(t, http.StatusForbidden, send(http.MethodPost, "/api/v1/auth/logout", "cross-site"),
		"another site must not sign the user out")
	assert.Equal(t, http.StatusForbidden, send(http.MethodPost, "/api/v1/auth/login", "cross-site"),
		"another site must not sign the user in")
	assert.Equal(t, http.StatusNoContent, send(http.MethodPost, "/api/v1/auth/logout", "same-origin"))
	assert.Equal(t, http.StatusNoContent, send(http.MethodPost, "/api/v1/auth/logout", ""),
		"clients without browser headers pass")
	assert.Equal(t, http.StatusOK, send(http.MethodGet, "/healthz", "cross-site"), "safe methods pass")
}
