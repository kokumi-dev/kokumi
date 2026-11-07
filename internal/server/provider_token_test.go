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
	p := &fakeProvider{priv: priv, clientAuth: clientSecretBasic, expiresIn: 3600, subject: testSubject}
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

const (
	// testSubject is the subject of a fake provider's ID tokens unless a
	// test changes it.
	testSubject = "user-1"
	// firstRefreshToken is the first refresh token a fake provider issues.
	firstRefreshToken = "refresh-1"
)

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

// postRefresh posts a refresh with cookies and returns the response.
func postRefresh(mgr *authManager, cookies ...*http.Cookie) *httptest.ResponseRecorder {
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
		_, err := auth.providerTokenOf(sealed, sessionState{ID: "sid-2"})
		assert.Error(t, err)
		token, err := auth.providerTokenOf(sealed, sessionState{ID: sessionID})
		require.NoError(t, err)
		assert.Equal(t, refreshToken, token)
		_, err = auth.providerTokenOf("", sessionState{ID: sessionID})
		assert.Error(t, err, "no cookie, no token")
	})

	t.Run("a token too large for a cookie is not sealed", func(t *testing.T) {
		_, err := auth.sealProviderToken(providerToken{RefreshToken: strings.Repeat("x", maxProviderTokenCookieLen), SessionID: sessionID})
		assert.Error(t, err)
	})
}

func TestHandleRefreshKeepsTheSessionWhileTheProviderIsDown(t *testing.T) {
	provider := newFakeProvider(t, func(p *fakeProvider) { p.refreshTokens, p.expiresIn = true, -1 })
	mgr := newOIDCManager(t, provider.URL, deliveryv1alpha1.ProviderSignOutNever)
	cookies := sessionCookies(t, signInOIDC(t, mgr))
	provider.set(func(p *fakeProvider) { p.refreshStatus = http.StatusBadGateway })

	w := postRefresh(mgr, cookies...)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "the UI keeps the session and retries")
	assert.Empty(t, w.Result().Cookies(), "the cookies stay")
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
			mgr := &authManager{auth: newTokenAuthenticator(), oidc: p, logger: logr.Discard()}

			cookies := sessionCookies(t, signInOIDC(t, mgr))
			assert.Equal(t, 1, get(provider, func(p *fakeProvider) int { return p.exchanges }))

			w := postRefresh(mgr, cookies...)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, 1, get(provider, func(p *fakeProvider) int { return p.refreshes }))

			assert.Equal(t, http.StatusNoContent, postSignOut(mgr, sessionCookies(t, w.Result().Cookies())...).Code)
			assert.Equal(t, []string{"refresh-2"}, get(provider, func(p *fakeProvider) []string { return p.revoked }))
		})
	}

	t.Run("a client registered another way fails the sign-in after one request", func(t *testing.T) {
		provider := newFakeProvider(t, func(p *fakeProvider) {
			p.authMethods, p.clientAuth = []string{clientSecretBasic, clientSecretPost}, clientSecretPost
		})
		p := newOIDCProviderFor(t, provider.URL, deliveryv1alpha1.ProviderSignOutNever)
		mgr := &authManager{auth: newTokenAuthenticator(), oidc: p, logger: logr.Discard()}

		assert.Equal(t, http.StatusBadRequest, oidcCallback(t, mgr).Code)
		assert.Equal(t, 1, get(provider, func(p *fakeProvider) int { return p.exchanges }),
			"the authorization code is sent once, not again in the other style")
	})
}
