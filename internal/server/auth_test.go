package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
)

// newTestAuthenticator builds an authenticator with a bcrypt hash of testPassword.
func newTestAuthenticator(t *testing.T) *authenticator {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	require.NoError(t, err)
	return newAuthenticator(testUsername, hash, []byte("test-signing-key-do-not-use-in-prod"))
}

func newAuthSecret(data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		Name: testSecret, Namespace: testNS,
		Data: data,
	}
}

func TestMiddlewareFailClosed(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	t.Run("intentionally disabled opens protected paths", func(t *testing.T) {
		reached = false
		mgr := &authManager{disabled: true}
		handler := mgr.middleware(next)
		r := httptest.NewRequest(http.MethodGet, "/api/v1/orders", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.True(t, reached, "disabled auth must pass through")
	})

	t.Run("unresolvable auth fails closed", func(t *testing.T) {
		reached = false
		// auth nil + disabled=false: configured but Secret missing.
		mgr := &authManager{}
		handler := mgr.middleware(next)
		r := httptest.NewRequest(http.MethodGet, "/api/v1/orders", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		assert.False(t, reached, "unresolvable auth must not pass through")
	})
}

func TestApplyAdminCredentials(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	require.NoError(t, err)
	secret := newAuthSecret(map[string][]byte{
		secretKeyUsername:     []byte(testUsername),
		secretKeyPasswordHash: hash,
	})
	c := fake.NewClientBuilder().WithObjects(secret).Build()

	t.Run("disabled leaves the authenticator token-only", func(t *testing.T) {
		auth, err := applyAdminCredentials(context.Background(), c, testNS, &deliveryv1alpha1.AdminUserConfig{
			Enabled:   new(false),
			SecretRef: &corev1.LocalObjectReference{Name: testSecret},
		}, newAuthenticator("", nil, []byte("test-signing-key-at-least-32-bytes-long!!")))
		require.NoError(t, err)
		assert.Empty(t, auth.username)
		assert.Empty(t, auth.passwordHash)
		assert.NotEmpty(t, auth.signingKey)
	})

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
			Data: map[string][]byte{"other": []byte("x")},
		}
		cr := fake.NewClientBuilder().WithObjects(incomplete).Build()
		_, err := buildTokenAuthenticator(context.Background(), cr, testNS, deliveryv1alpha1.DefaultTokenSigningKeySecretName, tokenLifetimes{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), secretKeySigningKey)
	})

	t.Run("enabled with custom username uses config username", func(t *testing.T) {
		auth, err := applyAdminCredentials(context.Background(), c, testNS, &deliveryv1alpha1.AdminUserConfig{
			Enabled:   new(true),
			Username:  testRootUser,
			SecretRef: &corev1.LocalObjectReference{Name: testSecret},
		}, newAuthenticator("", nil, []byte("test-signing-key-at-least-32-bytes-long!!")))
		require.NoError(t, err)
		assert.Equal(t, testRootUser, auth.username)
		assert.True(t, auth.verifyCredentials(testRootUser, testPassword))
	})

	t.Run("missing secret errors", func(t *testing.T) {
		_, err := applyAdminCredentials(context.Background(), c, testNS, &deliveryv1alpha1.AdminUserConfig{
			Enabled:   new(true),
			SecretRef: &corev1.LocalObjectReference{Name: "absent"},
		}, newAuthenticator("", nil, []byte("test-signing-key-at-least-32-bytes-long!!")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "absent")
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

func TestIssueAndParseToken(t *testing.T) {
	auth := newTestAuthenticator(t)
	now := time.Now()

	token, expires, err := auth.issueAccessToken(now)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	assert.WithinDuration(t, now.Add(defaultAccessTokenTTL), expires, time.Second)

	claims, err := auth.parseToken(token)
	require.NoError(t, err)
	assert.Equal(t, testUsername, claims.Subject)
	assert.Equal(t, tokenIssuer, claims.Issuer)
	assert.NotEmpty(t, claims.ID)
	require.NotNil(t, claims.ExpiresAt)
	assert.WithinDuration(t, expires, claims.ExpiresAt.Time, time.Second)
}

func TestParseTokenRejectsInvalidTokens(t *testing.T) {
	auth := newTestAuthenticator(t)
	now := time.Now()

	t.Run("garbage string", func(t *testing.T) {
		_, err := auth.parseToken("not-a-jwt")
		assert.Error(t, err)
	})

	t.Run("signed with a different key", func(t *testing.T) {
		other := &authenticator{
			username:   testUsername,
			signingKey: []byte("a-totally-different-key"),
			accessTTL:  defaultAccessTokenTTL,
		}
		token, _, err := other.issueAccessToken(now)
		require.NoError(t, err)
		_, err = auth.parseToken(token)
		assert.Error(t, err, "token signed with another key must be rejected")
	})

	t.Run("expired token", func(t *testing.T) {
		token, _, err := auth.issueAccessToken(now.Add(-2 * defaultAccessTokenTTL))
		require.NoError(t, err)
		_, err = auth.parseToken(token)
		require.Error(t, err)
		assert.ErrorIs(t, err, jwt.ErrTokenExpired)
	})

	t.Run("wrong issuer", func(t *testing.T) {
		claims := jwt.RegisteredClaims{
			Subject:   testUsername,
			Issuer:    "someone-else",
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		}
		raw := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		signed, err := raw.SignedString(auth.signingKey)
		require.NoError(t, err)
		_, err = auth.parseToken(signed)
		assert.Error(t, err, "token with unexpected issuer must be rejected")
	})

	t.Run("alg none is rejected", func(t *testing.T) {
		claims := jwt.RegisteredClaims{
			Subject:   testUsername,
			Issuer:    tokenIssuer,
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		}
		raw := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
		signed, err := raw.SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)
		_, err = auth.parseToken(signed)
		assert.Error(t, err, "alg=none must never be accepted")
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
	validToken, _, err := auth.issueAccessToken(time.Now())
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
		{"protected with valid token passes", testOrdersPath, "Bearer " + validToken, http.StatusOK, true},
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
		claims, err := auth.parseToken(resp.Token)
		require.NoError(t, err)
		assert.Equal(t, testUsername, claims.Subject)
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

func TestRandomTokenIDUnique(t *testing.T) {
	a := randomTokenID()
	b := randomTokenID()
	assert.Len(t, a, 32)
	assert.NotEqual(t, a, b)
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
	assert.InDelta(t, defaultSessionTTL.Seconds(), refresh.MaxAge, 2, "the cookie lasts as long as the session")

	// The refresh cookie value must validate as a refresh token.
	_, _, err := auth.parseRefresh(refresh.Value)
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
		_, err := auth.parseToken(resp.Token)
		assert.NoError(t, err)

		// The refresh cookie must have been rotated (different value).
		newCookies := w.Result().Cookies()
		require.Len(t, newCookies, 1)
		assert.NotEqual(t, refreshCookie.Value, newCookies[0].Value)
	})

	t.Run("missing refresh cookie is rejected", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
		w := httptest.NewRecorder()

		refreshHandler.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		// The (absent) cookie is cleared.
		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, refreshCookieName, cookies[0].Name)
		assert.Equal(t, -1, cookies[0].MaxAge)
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

	t.Run("default TTL when env unset", func(t *testing.T) {
		auth, err := buildTokenAuthenticator(context.Background(), reader, testNS, deliveryv1alpha1.DefaultTokenSigningKeySecretName, tokenLifetimes{})
		require.NoError(t, err)
		require.NotNil(t, auth)
		assert.Equal(t, defaultAccessTokenTTL, auth.accessTTL)
		assert.Equal(t, defaultSessionTTL, auth.sessionTTL)
	})
}

func TestAuthManagerFailClosed(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	require.NoError(t, err)
	secret := newAuthSecret(map[string][]byte{
		secretKeyUsername:     []byte(testUsername),
		secretKeyPasswordHash: hash,
	})
	tokenSecret := &corev1.Secret{
		Name: deliveryv1alpha1.DefaultTokenSigningKeySecretName, Namespace: testNS,
		Data: map[string][]byte{secretKeySigningKey: []byte("test-signing-key-at-least-32-bytes-long!!")},
	}
	reader := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(secret, tokenSecret).Build()

	logger := logr.Discard()

	t.Run("keeps last-known-good authenticator on transient error", func(t *testing.T) {
		m := &authManager{
			reader:    reader,
			apiReader: reader,
			ns:        testNS,
			logger:    logger,
		}
		// Establish a working authenticator.
		m.reload(context.Background(), &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNS,
			Spec: deliveryv1alpha1.KitchenSpec{
				Auth: &deliveryv1alpha1.KitchenAuth{
					AdminUser: &deliveryv1alpha1.AdminUserConfig{
						Enabled:   new(true),
						SecretRef: &corev1.LocalObjectReference{Name: testSecret},
					},
				},
			},
		})
		require.NotNil(t, m.get())

		// Point at a missing Secret: reload must fail closed and keep the previous authenticator.
		m.reload(context.Background(), &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNS,
			Spec: deliveryv1alpha1.KitchenSpec{
				Auth: &deliveryv1alpha1.KitchenAuth{
					AdminUser: &deliveryv1alpha1.AdminUserConfig{
						Enabled:   new(true),
						SecretRef: &corev1.LocalObjectReference{Name: "does-not-exist"},
					},
				},
			},
		})
		assert.NotNil(t, m.get(), "auth must remain enabled after a transient error")
		assert.False(t, m.disabled)
	})

	t.Run("intentional disable opens the API", func(t *testing.T) {
		m := &authManager{
			reader:    reader,
			apiReader: reader,
			ns:        testNS,
			logger:    logger,
		}
		m.reload(context.Background(), &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNS,
			Spec: deliveryv1alpha1.KitchenSpec{
				Auth: &deliveryv1alpha1.KitchenAuth{
					AdminUser: &deliveryv1alpha1.AdminUserConfig{
						Enabled:   new(false),
						SecretRef: &corev1.LocalObjectReference{Name: testSecret},
					},
				},
			},
		})
		// Admin disabled + no OIDC: intentionally off (open API); token-only authenticator still present.
		assert.True(t, m.disabled)
		assert.False(t, m.adminLoginEnabled())
		assert.Empty(t, m.providers())
	})
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

func TestRefreshCookieSecureFlagMatchesRequest(t *testing.T) {
	t.Run("Secure over HTTPS", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.TLS = &tls.ConnectionState{}
		w := httptest.NewRecorder()
		setRefreshCookie(w, r, "rt", time.Now().Add(time.Hour))

		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.True(t, cookies[0].Secure)
	})

	t.Run("not Secure over plain HTTP so Safari keeps it", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		w := httptest.NewRecorder()
		setRefreshCookie(w, r, "rt", time.Now().Add(time.Hour))

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

func TestSessionEndsAtSignInPlusSessionTTL(t *testing.T) {
	auth := newTestAuthenticator(t)
	provider := newAdminProvider(auth)
	id := &Identity{Subject: testUsername, Provider: providerAdmin}

	refreshWith := func(t *testing.T, s *Session) (*Session, error) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
		r.AddCookie(&http.Cookie{Name: refreshCookieName, Value: s.RefreshToken})
		return provider.Refresh(r)
	}

	t.Run("refreshing keeps the end of the session", func(t *testing.T) {
		ends := time.Now().Add(time.Hour)
		signedIn, err := auth.issueSession(time.Now(), id, ends)
		require.NoError(t, err)

		refreshed, err := refreshWith(t, signedIn)
		require.NoError(t, err)
		assert.WithinDuration(t, ends, refreshed.ExpiresAt, time.Second)
		_, refreshEnds, err := auth.parseRefresh(refreshed.RefreshToken)
		require.NoError(t, err)
		assert.WithinDuration(t, ends, refreshEnds, time.Second, "the rotated refresh token must not outlive the session")
	})

	t.Run("an access token expires with its session", func(t *testing.T) {
		ends := time.Now().Add(time.Minute)
		s, err := auth.issueSession(time.Now(), id, ends)
		require.NoError(t, err)
		assert.Equal(t, ends, s.AccessExpiresAt)
		_, expires, err := parseIdentityToken(auth, s.AccessToken)
		require.NoError(t, err)
		assert.WithinDuration(t, ends, expires, time.Second)
	})

	t.Run("an ended session cannot be refreshed", func(t *testing.T) {
		ended, err := auth.issueSession(time.Now().Add(-time.Hour), id, time.Now().Add(-time.Second))
		require.NoError(t, err)

		_, err = refreshWith(t, ended)
		assert.ErrorIs(t, err, errInvalidRefresh)
	})

	t.Run("a sign-in starts a session of sessionTTL", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"admin","password":"s3cret-passw0rd"}`))
		s, err := provider.Login(r)
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now().Add(defaultSessionTTL), s.ExpiresAt, time.Second)
		assert.WithinDuration(t, time.Now().Add(defaultAccessTokenTTL), s.AccessExpiresAt, time.Second)
	})
}

func TestTokenLifetimesSurviveAdminCredentials(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	require.NoError(t, err)
	c := fake.NewClientBuilder().WithObjects(newAuthSecret(map[string][]byte{secretKeyPasswordHash: hash})).Build()
	shared := newAuthenticator("", nil, []byte("test-signing-key-at-least-32-bytes-long!!"))
	shared.accessTTL, shared.sessionTTL = 15*time.Minute, 8*time.Hour

	for _, enabled := range []bool{true, false} {
		auth, err := applyAdminCredentials(context.Background(), c, testNS, &deliveryv1alpha1.AdminUserConfig{
			Enabled:   new(enabled),
			SecretRef: &corev1.LocalObjectReference{Name: testSecret},
		}, shared)
		require.NoError(t, err)
		assert.Equal(t, 15*time.Minute, auth.accessTTL, "KOKUMI_TOKEN_TTL must apply with an admin user (enabled=%t)", enabled)
		assert.Equal(t, 8*time.Hour, auth.sessionTTL, "KOKUMI_SESSION_TTL must apply with an admin user (enabled=%t)", enabled)
	}
	assert.Empty(t, shared.username, "the shared authenticator is not modified")
}

func TestHandleLogoutClearsCookiesWithoutAuthenticator(t *testing.T) {
	// The signing key may not be resolved yet, e.g. right after a restart;
	// the browser's cookies must go anyway.
	handler := handleLogout(&authManager{})

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "rt"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	cookies := w.Result().Cookies()
	assert.Equal(t, -1, cookieNamed(t, cookies, refreshCookieName).MaxAge)
	assert.Equal(t, -1, cookieNamed(t, cookies, oidcIDTokenCookie).MaxAge)
	assert.Nil(t, findCookie(cookies, oidcPromptCookie), "no OIDC provider, no prompt")
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
