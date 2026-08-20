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

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kokumi-dev/kokumi/internal/namespace"
)

const (
	testUsername = "admin"
	testPassword = "s3cret-passw0rd"
	testNS       = "kokumi"
	testSecret   = "kokumi-server-auth"
)

// newTestAuthenticator builds an authenticator backed by a freshly generated
// bcrypt hash of testPassword and a fixed signing key.
func newTestAuthenticator(t *testing.T) *authenticator {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	require.NoError(t, err)
	return newAuthenticator(testUsername, hash, []byte("test-signing-key-do-not-use-in-prod"))
}

func newAuthSecret(data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSecret, Namespace: testNS},
		Data:       data,
	}
}

func TestLoadAuthenticator(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	require.NoError(t, err)

	validData := map[string][]byte{
		secretKeyUsername:     []byte(testUsername),
		secretKeyPasswordHash: hash,
		secretKeySigningKey:   []byte("a-signing-key"),
	}

	tests := []struct {
		name      string
		objects   []*corev1.Secret
		wantErr   string
		wantUser  string
		checkPass bool
	}{
		{
			name:      "valid secret",
			objects:   []*corev1.Secret{newAuthSecret(validData)},
			wantUser:  testUsername,
			checkPass: true,
		},
		{
			name:    "secret not found",
			objects: nil,
			wantErr: "reading auth secret",
		},
		{
			name: "missing username",
			objects: []*corev1.Secret{newAuthSecret(map[string][]byte{
				secretKeyPasswordHash: hash,
				secretKeySigningKey:   []byte("a-signing-key"),
			})},
			wantErr: secretKeyUsername,
		},
		{
			name: "blank username trimmed to empty",
			objects: []*corev1.Secret{newAuthSecret(map[string][]byte{
				secretKeyUsername:     []byte("   "),
				secretKeyPasswordHash: hash,
				secretKeySigningKey:   []byte("a-signing-key"),
			})},
			wantErr: secretKeyUsername,
		},
		{
			name: "missing password hash",
			objects: []*corev1.Secret{newAuthSecret(map[string][]byte{
				secretKeyUsername:   []byte(testUsername),
				secretKeySigningKey: []byte("a-signing-key"),
			})},
			wantErr: secretKeyPasswordHash,
		},
		{
			name: "missing signing key",
			objects: []*corev1.Secret{newAuthSecret(map[string][]byte{
				secretKeyUsername:     []byte(testUsername),
				secretKeyPasswordHash: hash,
			})},
			wantErr: secretKeySigningKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder()
			for _, o := range tt.objects {
				builder = builder.WithObjects(o)
			}
			c := builder.Build()

			auth, err := loadAuthenticator(context.Background(), c, testNS, testSecret)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, auth)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, auth)
			assert.Equal(t, tt.wantUser, auth.username)
			assert.Equal(t, defaultAccessTokenTTL, auth.accessTTL)
			if tt.checkPass {
				assert.True(t, auth.verifyCredentials(testUsername, testPassword))
			}
		})
	}
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
		{"wrong username", "root", testPassword, false},
		{"both wrong", "root", "nope", false},
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

// TestVerifyCredentialsHtpasswdHash locks in compatibility with hashes produced
// by `htpasswd -B`, which emit the "$2y$" bcrypt prefix. Operators are expected
// to generate the password hash this way, so a regression here would break the
// documented install flow.
func TestVerifyCredentialsHtpasswdHash(t *testing.T) {
	// Produced by: htpasswd -nbB admin admin  (password = "admin")
	const htpasswdHash = "$2y$05$MG5FZ/WlMewHp8kwaoZixeQ8NCXjhp7ZWwx1N40pQ6oc3VfL9Xu0y"
	auth := &authenticator{
		username:     "admin",
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
		{"/api/v1/orders", true},
		{"/api/v1/menus/foo", true},
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
		{"valid bearer", "Bearer abc.def.ghi", "abc.def.ghi"},
		{"case-insensitive scheme", "bearer abc.def.ghi", "abc.def.ghi"},
		{"trims surrounding space", "Bearer   abc  ", "abc"},
		{"empty header", "", ""},
		{"no scheme", "abc.def.ghi", ""},
		{"wrong scheme", "Basic abc", ""},
		{"scheme only", "Bearer ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/orders", nil)
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
	handler := auth.middleware(next)

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
		{"protected without token is rejected", "/api/v1/orders", "", http.StatusUnauthorized, false},
		{"protected with bad token is rejected", "/api/v1/orders", "Bearer bad", http.StatusUnauthorized, false},
		{"protected with valid token passes", "/api/v1/orders", "Bearer " + validToken, http.StatusOK, true},
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
	handler := handleLogin(auth)

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
		// In the test environment the in-cluster SA file is absent, so this
		// exercises the final default branch.
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

func TestAuthSecretName(t *testing.T) {
	assert.Equal(t, defaultAuthSecretName, authSecretName(func(string) string { return "" }))
	assert.Equal(t, "my-secret", authSecretName(func(k string) string {
		if k == "AUTH_SECRET_NAME" {
			return "my-secret"
		}
		return ""
	}))
}

func TestRandomTokenIDUnique(t *testing.T) {
	a := randomTokenID()
	b := randomTokenID()
	assert.Len(t, a, 32)
	assert.NotEqual(t, a, b)
}

func TestHandleLoginSetsRefreshCookie(t *testing.T) {
	auth := newTestAuthenticator(t)
	handler := handleLogin(auth)

	body := strings.NewReader(`{"username":"admin","password":"s3cret-passw0rd"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, refreshCookieName, cookies[0].Name)
	assert.True(t, cookies[0].HttpOnly)
	assert.NotEmpty(t, cookies[0].Value)

	// The refresh cookie value must validate as a refresh token.
	_, err := auth.parseRefresh(cookies[0].Value)
	assert.NoError(t, err)
}

func TestHandleRefresh(t *testing.T) {
	auth := newTestAuthenticator(t)
	loginHandler := handleLogin(auth)
	refreshHandler := handleRefresh(auth)

	// Log in to obtain a refresh cookie.
	loginBody := strings.NewReader(`{"username":"admin","password":"s3cret-passw0rd"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", loginBody)
	loginRec := httptest.NewRecorder()
	loginHandler.ServeHTTP(loginRec, loginReq)
	require.Equal(t, http.StatusOK, loginRec.Code)

	refreshCookie := loginRec.Result().Cookies()[0]

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
	handler := handleLogout(auth)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, refreshCookieName, cookies[0].Name)
	assert.Equal(t, -1, cookies[0].MaxAge)
}

func TestHandleInfoReportsAuthEnabled(t *testing.T) {
	auth := newTestAuthenticator(t)
	handler := handleInfo(auth)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp InfoResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.AuthEnabled)
}

func TestHandleInfoNoAuthReportsDisabled(t *testing.T) {
	handler := handleInfo(nil)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp InfoResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.AuthEnabled)
}

func TestApplyAuthConfig(t *testing.T) {
	a := newTestAuthenticator(t)

	t.Run("KOKUMI_TOKEN_TTL overrides the access token lifetime", func(t *testing.T) {
		applyAuthConfig(a, func(k string) string {
			if k == "KOKUMI_TOKEN_TTL" {
				return "30m"
			}
			return ""
		})
		assert.Equal(t, 30*time.Minute, a.accessTTL)
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
	auth := newTestAuthenticator(t)

	t.Run("Secure over HTTPS", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.TLS = &tls.ConnectionState{}
		w := httptest.NewRecorder()
		auth.setRefreshCookie(w, r, "rt")

		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.True(t, cookies[0].Secure)
	})

	t.Run("not Secure over plain HTTP so Safari keeps it", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		w := httptest.NewRecorder()
		auth.setRefreshCookie(w, r, "rt")

		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.False(t, cookies[0].Secure)
		assert.True(t, cookies[0].HttpOnly)
	})
}
