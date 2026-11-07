package server

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// fakeProvider is an OIDC provider whose answers a test can change. Its
// token endpoint answers the authorization code and refresh grants with an
// ID token for subject and groups, and rotates refresh tokens.
type fakeProvider struct {
	*httptest.Server
	priv *rsa.PrivateKey

	mu sync.Mutex
	// endSession advertises RP-initiated logout at /logout.
	endSession bool
	// revocation advertises a revocation endpoint at /revoke.
	revocation bool
	// authMethods, when set, is the token_endpoint_auth_methods_supported
	// it advertises.
	authMethods []string
	// clientAuth is how the client is registered to authenticate,
	// client_secret_basic or client_secret_post. The token and revocation
	// endpoints answer a request that authenticates another way with 401
	// invalid_client, as Okta does.
	clientAuth string
	// refreshTokens has token responses carry a refresh token.
	refreshTokens bool
	// expiresIn is the expires_in of token responses; negative makes the
	// provider check due at once.
	expiresIn int
	subject   string
	groups    []string
	// sessionID, when set, is the sid of issued ID tokens.
	sessionID string
	// refreshError, when set, answers refresh grants with this error code
	// and status 400; refreshStatus, when set, with this HTTP status.
	refreshError  string
	refreshStatus int

	refreshToken string   // the refresh token the provider accepts
	issued       int      // refresh tokens issued
	exchanges    int      // authorization code grants received
	refreshes    int      // refresh grants received
	revoked      []string // tokens revoked
}

// newFakeProvider starts a fakeProvider, configured by configure before it
// serves.
func newFakeProvider(t *testing.T, configure func(*fakeProvider)) *fakeProvider {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	op := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{{PublicKey: priv.Public(), KeyID: testKeyID, Algorithm: oidc.RS256}},
	}
	p := &fakeProvider{priv: priv, clientAuth: clientSecretBasic, expiresIn: 3600, subject: "user-1"}
	configure(p)
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			metadata := map[string]any{
				"issuer": p.URL, "authorization_endpoint": p.URL + "/auth", "token_endpoint": p.URL + "/token",
				"jwks_uri": p.URL + "/keys", "id_token_signing_alg_values_supported": []string{oidc.RS256},
			}
			if p.endSession {
				metadata["end_session_endpoint"] = p.URL + "/logout"
			}
			if p.revocation {
				metadata["revocation_endpoint"] = p.URL + "/revoke"
			}
			if p.authMethods != nil {
				metadata["token_endpoint_auth_methods_supported"] = p.authMethods
			}
			_ = json.NewEncoder(w).Encode(metadata)
		case "/token":
			p.serveToken(w, r, priv)
		case "/revoke":
			_ = r.ParseForm()
			if !p.authenticates(r) {
				writeOAuthError(w, http.StatusUnauthorized, "invalid_client")
				return
			}
			p.revoked = append(p.revoked, r.PostForm.Get("token"))
		default:
			op.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(p.Close)
	op.SetIssuer(p.URL)
	return p
}

// The client_secret_* methods of OpenID Connect Core section 9.
const (
	clientSecretBasic = "client_secret_basic"
	clientSecretPost  = "client_secret_post"
)

// authenticates reports whether r authenticates the client in the one way
// it is registered with, and in no other.
func (p *fakeProvider) authenticates(r *http.Request) bool {
	user, password, inHeader := r.BasicAuth()
	inForm := r.PostForm.Has("client_secret")
	if p.clientAuth == clientSecretPost {
		return !inHeader && r.PostForm.Get("client_id") == testClientID && r.PostForm.Get("client_secret") == testClientSec
	}
	return !inForm && inHeader && user == testClientID && password == testClientSec
}

// writeOAuthError answers with an OAuth error response (RFC 6749 section
// 5.2).
func writeOAuthError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (p *fakeProvider) serveToken(w http.ResponseWriter, r *http.Request, priv *rsa.PrivateKey) {
	_ = r.ParseForm()
	refresh := r.PostForm.Get("grant_type") == "refresh_token"
	if refresh {
		p.refreshes++
	} else {
		p.exchanges++
	}
	if !p.authenticates(r) {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	if refresh {
		switch {
		case p.refreshStatus != 0:
			w.WriteHeader(p.refreshStatus)
			return
		case p.refreshError != "":
			writeOAuthError(w, http.StatusBadRequest, p.refreshError)
			return
		case r.PostForm.Get("refresh_token") != p.refreshToken:
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
	}
	now := time.Now()
	groups, _ := json.Marshal(p.groups)
	claims := fmt.Sprintf(`{"iss":%q,"aud":%q,"sub":%q,"sid":%q,"email":"user@example.com","groups":%s,"iat":%d,"exp":%d}`,
		p.URL, testClientID, p.subject, p.sessionID, groups, now.Unix(), now.Add(time.Hour).Unix())
	response := map[string]any{
		"access_token": "upstream-access-token", "token_type": "Bearer", "expires_in": p.expiresIn,
		"id_token": oidctest.SignIDToken(priv, testKeyID, oidc.RS256, claims),
	}
	if p.refreshTokens {
		p.issued++
		p.refreshToken = fmt.Sprintf("refresh-%d", p.issued)
		response["refresh_token"] = p.refreshToken
	}
	_ = json.NewEncoder(w).Encode(response)
}

// sign signs claims, a JSON object, with the provider's key.
func (p *fakeProvider) sign(claims string) string {
	return oidctest.SignIDToken(p.priv, testKeyID, oidc.RS256, claims)
}

// set changes the provider under its lock.
func (p *fakeProvider) set(change func(*fakeProvider)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	change(p)
}

// get reads the provider under its lock.
func get[T any](p *fakeProvider, read func(*fakeProvider) T) T {
	p.mu.Lock()
	defer p.mu.Unlock()
	return read(p)
}

// refreshSession posts a refresh with cookies and returns the response.
func refreshSession(mgr *authManager, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/auth/refresh", strings.NewReader(`{"active":true}`))
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handleRefresh(mgr).ServeHTTP(w, r)
	return w
}

// sessionCookies returns the refresh and provider token cookies of cookies.
func sessionCookies(t *testing.T, cookies []*http.Cookie) []*http.Cookie {
	t.Helper()
	return []*http.Cookie{cookieNamed(t, cookies, refreshCookieName), cookieNamed(t, cookies, providerTokenCookie)}
}

func TestProviderTokenCookie(t *testing.T) {
	auth := newTestAuthenticator(t)
	const refreshToken, sessionID = "provider-refresh-token", "sid-1"
	sealed, err := auth.sealProviderToken(providerToken{RefreshToken: refreshToken, SessionID: sessionID})
	require.NoError(t, err)
	assert.NotContains(t, sealed, refreshToken, "the browser cannot read the token")

	opened, err := auth.openProviderToken(sealed)
	require.NoError(t, err)
	assert.Equal(t, providerToken{RefreshToken: refreshToken, SessionID: sessionID}, opened)

	t.Run("another signing key cannot open it", func(t *testing.T) {
		other := newAuthenticator("", nil, []byte("another-signing-key-of-32-bytes-or-more"))
		_, err := other.openProviderToken(sealed)
		assert.Error(t, err)
	})

	t.Run("a changed cookie is refused", func(t *testing.T) {
		parts := strings.Split(sealed, ".")
		parts[3] = strings.Repeat("A", len(parts[3]))
		_, err := auth.openProviderToken(strings.Join(parts, "."))
		assert.Error(t, err)
	})

	t.Run("the cookie of another session is refused", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.AddCookie(&http.Cookie{Name: providerTokenCookie, Value: sealed})
		_, err := auth.providerTokenFromCookie(r, sessionState{ID: "sid-2"})
		assert.Error(t, err)
		token, err := auth.providerTokenFromCookie(r, sessionState{ID: sessionID})
		require.NoError(t, err)
		assert.Equal(t, refreshToken, token)
	})
}

func TestOIDCSessionFollowsTheProvider(t *testing.T) {
	// signIn signs in at a new fake provider, configured by configure, and
	// returns it with the auth manager and the session's cookies.
	signIn := func(t *testing.T, configure func(*fakeProvider)) (*fakeProvider, *authManager, []*http.Cookie) {
		t.Helper()
		provider := newFakeProvider(t, func(p *fakeProvider) {
			p.refreshTokens = true
			configure(p)
		})
		p := newOIDCProviderFor(t, provider.URL, deliveryv1alpha1.ProviderSignOutNever)
		mgr := &authManager{auth: p.auth, oidc: p, logger: logr.Discard()}
		return provider, mgr, sessionCookies(t, signInOIDC(t, mgr))
	}
	due := func(p *fakeProvider) { p.expiresIn = -1 }
	identityOf := func(t *testing.T, mgr *authManager, w *httptest.ResponseRecorder) *Identity {
		t.Helper()
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var resp loginResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		got, err := mgr.auth.parseToken(resp.Token, accessTokenType)
		require.NoError(t, err)
		return got.identity
	}

	t.Run("the sign-in keeps the provider's refresh token for its session", func(t *testing.T) {
		_, mgr, cookies := signIn(t, func(*fakeProvider) {})
		session, err := mgr.auth.parseToken(cookies[0].Value, refreshTokenType)
		require.NoError(t, err)
		kept, err := mgr.auth.openProviderToken(cookies[1].Value)
		require.NoError(t, err)
		assert.Equal(t, providerToken{RefreshToken: "refresh-1", SessionID: session.session.ID}, kept)
		assert.WithinDuration(t, time.Now().Add(time.Hour), session.session.IDPCheck, 2*time.Second,
			"the provider is asked again when its access token expires")
		assert.True(t, cookies[1].HttpOnly)
		assert.Equal(t, cookies[0].MaxAge, cookies[1].MaxAge, "both cookies expire at the idle deadline")
	})

	t.Run("a refresh before the check is due does not ask the provider", func(t *testing.T) {
		provider, mgr, cookies := signIn(t, func(*fakeProvider) {})
		identityOf(t, mgr, refreshSession(mgr, cookies...))
		assert.Zero(t, get(provider, func(p *fakeProvider) int { return p.refreshes }))
	})

	t.Run("a due check takes the identity from the provider and rotates its refresh token", func(t *testing.T) {
		provider, mgr, cookies := signIn(t, due)
		provider.set(func(p *fakeProvider) { p.groups = []string{"release-approvers"} })

		w := refreshSession(mgr, cookies...)
		assert.Equal(t, []string{"release-approvers"}, identityOf(t, mgr, w).Groups)
		assert.Equal(t, 1, get(provider, func(p *fakeProvider) int { return p.refreshes }))
		kept, err := mgr.auth.openProviderToken(cookieNamed(t, w.Result().Cookies(), providerTokenCookie).Value)
		require.NoError(t, err)
		assert.Equal(t, "refresh-2", kept.RefreshToken)

		identityOf(t, mgr, refreshSession(mgr, sessionCookies(t, w.Result().Cookies())...))
		assert.Equal(t, 2, get(provider, func(p *fakeProvider) int { return p.refreshes }), "the rotated token works")
	})

	for _, code := range []string{"invalid_grant", "invalid_request"} {
		t.Run(code+" ends the session", func(t *testing.T) {
			provider, mgr, cookies := signIn(t, due)
			provider.set(func(p *fakeProvider) { p.refreshError = code })
			w := refreshSession(mgr, cookies...)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Equal(t, -1, cookieNamed(t, w.Result().Cookies(), refreshCookieName).MaxAge)
			assert.Equal(t, -1, cookieNamed(t, w.Result().Cookies(), providerTokenCookie).MaxAge)
			assert.Equal(t, 1, get(provider, func(p *fakeProvider) int { return p.refreshes }), "the provider is asked once")
		})
	}

	t.Run("an ID token for another user ends the session", func(t *testing.T) {
		provider, mgr, cookies := signIn(t, due)
		provider.set(func(p *fakeProvider) { p.subject = "user-2" })
		assert.Equal(t, http.StatusUnauthorized, refreshSession(mgr, cookies...).Code)
	})

	// Answers that say nothing about the user: the session waits for the
	// provider, or for the operator to fix the client, and goes on after.
	type pause struct {
		name         string
		answer, heal func(*fakeProvider)
	}
	answersError := func(code string) pause {
		return pause{
			name:   code,
			answer: func(p *fakeProvider) { p.refreshError = code },
			heal:   func(p *fakeProvider) { p.refreshError = "" },
		}
	}
	answersStatus := func(status int) pause {
		return pause{
			name:   fmt.Sprintf("status %d", status),
			answer: func(p *fakeProvider) { p.refreshStatus = status },
			heal:   func(p *fakeProvider) { p.refreshStatus = 0 },
		}
	}
	for _, pause := range []pause{
		{
			name:   "a refused client authentication (invalid_client)",
			answer: func(p *fakeProvider) { p.clientAuth = clientSecretPost },
			heal:   func(p *fakeProvider) { p.clientAuth = clientSecretBasic },
		},
		answersError("unauthorized_client"),
		answersError("unsupported_grant_type"),
		answersError("temporarily_unavailable"),
		answersStatus(http.StatusInternalServerError),
		answersStatus(http.StatusBadGateway),
		answersStatus(http.StatusTooManyRequests),
	} {
		t.Run(pause.name+" keeps the session for a later try", func(t *testing.T) {
			provider, mgr, cookies := signIn(t, due)
			provider.set(pause.answer)
			w := refreshSession(mgr, cookies...)
			assert.Equal(t, http.StatusServiceUnavailable, w.Code)
			assert.Empty(t, w.Result().Cookies(), "the cookies stay")
			assert.Equal(t, 1, get(provider, func(p *fakeProvider) int { return p.refreshes }),
				"the refresh token is sent once, not again in the other style")

			provider.set(pause.heal)
			identityOf(t, mgr, refreshSession(mgr, cookies...))
		})
	}

	t.Run("an unreachable provider keeps the session for a later try", func(t *testing.T) {
		provider, mgr, cookies := signIn(t, due)
		provider.Close()
		assert.Equal(t, http.StatusServiceUnavailable, refreshSession(mgr, cookies...).Code)
	})

	t.Run("a session without its provider token cookie ends", func(t *testing.T) {
		_, mgr, cookies := signIn(t, func(*fakeProvider) {})
		assert.Equal(t, http.StatusUnauthorized, refreshSession(mgr, cookies[0]).Code)
	})

	t.Run("the provider token cookie of another session is refused", func(t *testing.T) {
		provider, mgr, cookies := signIn(t, func(*fakeProvider) {})
		other := sessionCookies(t, signInOIDC(t, mgr))
		assert.Equal(t, http.StatusUnauthorized, refreshSession(mgr, cookies[0], other[1]).Code)
		assert.Zero(t, get(provider, func(p *fakeProvider) int { return p.refreshes }))
	})

	t.Run("a provider without refresh tokens leaves the session unchecked", func(t *testing.T) {
		provider := newFakeProvider(t, due)
		p := newOIDCProviderFor(t, provider.URL, deliveryv1alpha1.ProviderSignOutNever)
		mgr := &authManager{auth: p.auth, oidc: p, logger: logr.Discard()}
		cookies := signInOIDC(t, mgr)
		assert.Equal(t, -1, cookieNamed(t, cookies, providerTokenCookie).MaxAge)
		session, err := mgr.auth.parseToken(cookieNamed(t, cookies, refreshCookieName).Value, refreshTokenType)
		require.NoError(t, err)
		assert.True(t, session.session.IDPCheck.IsZero())

		identityOf(t, mgr, refreshSession(mgr, cookieNamed(t, cookies, refreshCookieName)))
		assert.Zero(t, get(provider, func(p *fakeProvider) int { return p.refreshes }))
	})
}

func TestClientAuthStyle(t *testing.T) {
	for _, tc := range []struct {
		methods []string
		want    oauth2.AuthStyle
	}{
		{nil, oauth2.AuthStyleInHeader},
		{[]string{clientSecretBasic}, oauth2.AuthStyleInHeader},
		{[]string{clientSecretPost, clientSecretBasic}, oauth2.AuthStyleInHeader},
		{[]string{clientSecretPost}, oauth2.AuthStyleInParams},
		{[]string{"private_key_jwt", clientSecretPost}, oauth2.AuthStyleInParams},
		{[]string{"private_key_jwt"}, oauth2.AuthStyleInHeader},
	} {
		assert.Equal(t, tc.want, clientAuthStyle(tc.methods), "methods %v", tc.methods)
	}
}

func TestOIDCTokenRequestsAuthenticateOneWay(t *testing.T) {
	for _, tc := range []struct {
		name       string
		advertised []string
		registered string
	}{
		{name: "basic when the provider lists no methods", registered: clientSecretBasic},
		{name: "basic when the provider lists it", advertised: []string{clientSecretPost, clientSecretBasic}, registered: clientSecretBasic},
		{name: "post when the provider lists only it", advertised: []string{clientSecretPost}, registered: clientSecretPost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := newFakeProvider(t, func(p *fakeProvider) {
				p.refreshTokens, p.revocation, p.expiresIn = true, true, -1
				p.authMethods, p.clientAuth = tc.advertised, tc.registered
			})
			p := newOIDCProviderFor(t, provider.URL, deliveryv1alpha1.ProviderSignOutNever)
			mgr := &authManager{auth: p.auth, oidc: p, logger: logr.Discard()}

			cookies := sessionCookies(t, signInOIDC(t, mgr))
			assert.Equal(t, 1, get(provider, func(p *fakeProvider) int { return p.exchanges }))

			w := refreshSession(mgr, cookies...)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, 1, get(provider, func(p *fakeProvider) int { return p.refreshes }))

			assert.Equal(t, http.StatusNoContent, signOut(mgr, sessionCookies(t, w.Result().Cookies())...).Code)
			assert.Equal(t, []string{"refresh-2"}, get(provider, func(p *fakeProvider) []string { return p.revoked }))
		})
	}

	t.Run("a client registered another way fails the sign-in after one request", func(t *testing.T) {
		provider := newFakeProvider(t, func(p *fakeProvider) {
			p.authMethods, p.clientAuth = []string{clientSecretBasic, clientSecretPost}, clientSecretPost
		})
		p := newOIDCProviderFor(t, provider.URL, deliveryv1alpha1.ProviderSignOutNever)
		mgr := &authManager{auth: p.auth, oidc: p, logger: logr.Discard()}

		assert.Equal(t, http.StatusBadRequest, oidcCallback(t, mgr).Code)
		assert.Equal(t, 1, get(provider, func(p *fakeProvider) int { return p.exchanges }),
			"the authorization code is sent once, not again in the other style")
	})
}

func TestOIDCSignOutRevokesTheProviderToken(t *testing.T) {
	for _, revocation := range []bool{true, false} {
		t.Run(fmt.Sprintf("revocation endpoint %t", revocation), func(t *testing.T) {
			provider := newFakeProvider(t, func(p *fakeProvider) {
				p.refreshTokens = true
				p.revocation = revocation
			})
			p := newOIDCProviderFor(t, provider.URL, deliveryv1alpha1.ProviderSignOutNever)
			mgr := &authManager{auth: p.auth, oidc: p, logger: logr.Discard()}

			w := signOut(mgr, sessionCookies(t, signInOIDC(t, mgr))...)
			assert.Equal(t, http.StatusNoContent, w.Code)
			assert.Equal(t, -1, cookieNamed(t, w.Result().Cookies(), providerTokenCookie).MaxAge)
			revoked := get(provider, func(p *fakeProvider) []string { return p.revoked })
			if revocation {
				assert.Equal(t, []string{"refresh-1"}, revoked)
			} else {
				assert.Empty(t, revoked)
			}
		})
	}
}
