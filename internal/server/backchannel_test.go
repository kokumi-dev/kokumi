package server

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

func TestBackchannelLogout(t *testing.T) {
	const providerSession = "op-session-1"
	// setup signs in at a fake provider whose ID tokens name providerSession,
	// with a record of ended sessions.
	setup := func(t *testing.T) (*fakeProvider, *authManager, client.Client) {
		t.Helper()
		provider := newFakeProvider(t, func(p *fakeProvider) { p.sessionID = providerSession })
		mgr := newOIDCManager(t, provider.URL, deliveryv1alpha1.ProviderSignOutNever)
		ended, c := newTestEndedSessions(t)
		mgr.ended = ended
		return provider, mgr, c
	}
	// logoutClaims returns the claims of a valid logout token of provider.
	logoutClaims := func(provider *fakeProvider) map[string]any {
		now := time.Now()
		return map[string]any{
			"iss": provider.URL, "aud": testClientID, "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(),
			"jti": "logout-1", "sub": testSubject, "sid": providerSession,
			"events": map[string]any{backchannelLogoutEvent: map[string]any{}},
		}
	}
	sign := func(t *testing.T, provider *fakeProvider, claims map[string]any) string {
		t.Helper()
		payload, err := json.Marshal(claims)
		require.NoError(t, err)
		return provider.sign(string(payload))
	}
	post := func(mgr *authManager, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oidc/backchannel-logout", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handleBackchannelLogout(mgr).ServeHTTP(w, r)
		return w
	}
	logout := func(mgr *authManager, token string) *httptest.ResponseRecorder {
		return post(mgr, url.Values{logoutTokenParam: {token}})
	}
	// signedIn signs in and returns the session's refresh cookie and a
	// fresh access token.
	signedIn := func(t *testing.T, mgr *authManager) (*http.Cookie, string) {
		t.Helper()
		refresh := cookieNamed(t, signInOIDC(t, mgr), refreshCookieName)
		w := postRefresh(mgr, refresh)
		require.Equal(t, http.StatusOK, w.Code)
		return cookieNamed(t, w.Result().Cookies(), refreshCookieName), accessTokenOf(t, w)
	}

	t.Run("a logout of the provider session ends the Kokumi sessions signed in with it", func(t *testing.T) {
		provider, mgr, _ := setup(t)
		refresh, access := signedIn(t, mgr)
		session, err := mgr.auth.parseToken(access, accessTokenType)
		require.NoError(t, err)
		assert.Equal(t, providerSession, session.session.ProviderSessionID)

		w := logout(mgr, sign(t, provider, logoutClaims(provider)))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		assert.Equal(t, http.StatusUnauthorized, protectedRequest(mgr, access))
		assert.Equal(t, http.StatusUnauthorized, postRefresh(mgr, refresh).Code)
	})

	t.Run("a logout of the user ends the sessions signed in before it", func(t *testing.T) {
		provider, mgr, _ := setup(t)
		_, before := signedIn(t, mgr)
		claims := logoutClaims(provider)
		delete(claims, "sid")
		claims["iat"] = time.Now().Add(time.Second).Unix()
		require.Equal(t, http.StatusOK, logout(mgr, sign(t, provider, claims)).Code)
		assert.Equal(t, http.StatusUnauthorized, protectedRequest(mgr, before))
	})

	t.Run("a sign-in after the logout is not ended", func(t *testing.T) {
		provider, mgr, _ := setup(t)
		claims := logoutClaims(provider)
		delete(claims, "sid")
		claims["iat"] = time.Now().Add(-time.Minute).Unix()
		require.Equal(t, http.StatusOK, logout(mgr, sign(t, provider, claims)).Code)
		_, after := signedIn(t, mgr)
		assert.Equal(t, http.StatusOK, protectedRequest(mgr, after))
	})

	t.Run("invalid logout tokens are refused and change nothing", func(t *testing.T) {
		provider, mgr, c := setup(t)
		other, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		changed := func(change func(map[string]any)) string {
			claims := logoutClaims(provider)
			change(claims)
			return sign(t, provider, claims)
		}
		cases := map[string]url.Values{
			"no logout_token": {},
			"not a JWT":       {logoutTokenParam: {"not-a-jwt"}},
			"signed with another key": {logoutTokenParam: {oidctest.SignIDToken(other, testKeyID, oidc.RS256,
				`{"iss":"`+provider.URL+`","aud":"`+testClientID+`","sub":"user-1","iat":1,"exp":9999999999,"events":{"`+backchannelLogoutEvent+`":{}}}`)}},
			"for another client":  {logoutTokenParam: {changed(func(c map[string]any) { c["aud"] = "another-client" })}},
			"from another issuer": {logoutTokenParam: {changed(func(c map[string]any) { c["iss"] = "https://other.example" })}},
			"expired":             {logoutTokenParam: {changed(func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() })}},
			"no exp":              {logoutTokenParam: {changed(func(c map[string]any) { delete(c, "exp") })}},
			"no iat":              {logoutTokenParam: {changed(func(c map[string]any) { delete(c, "iat") })}},
			"no events":           {logoutTokenParam: {changed(func(c map[string]any) { delete(c, "events") })}},
			"another event":       {logoutTokenParam: {changed(func(c map[string]any) { c["events"] = map[string]any{"https://example.com/other": map[string]any{}} })}},
			"a nonce":             {logoutTokenParam: {changed(func(c map[string]any) { c["nonce"] = "n-1" })}},
			"neither sub nor sid": {logoutTokenParam: {changed(func(c map[string]any) { delete(c, "sub"); delete(c, "sid") })}},
			"an ID token": {logoutTokenParam: {changed(func(c map[string]any) {
				delete(c, "events")
				c["nonce"] = "n-1"
				c["email"] = "user@example.com"
			})}},
		}
		for name, form := range cases {
			t.Run(name, func(t *testing.T) {
				w := post(mgr, form)
				assert.Equal(t, http.StatusBadRequest, w.Code)
				var body map[string]string
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				assert.Equal(t, "invalid_request", body["error"])
			})
		}
		assert.Empty(t, storedEntries(t, c).Data)
	})

	t.Run("a logout that cannot be recorded asks the provider to retry", func(t *testing.T) {
		provider, mgr, _ := setup(t)
		mgr.ended = newEndedSessions(fake.NewClientBuilder().Build(), testNS)
		assert.Equal(t, http.StatusInternalServerError, logout(mgr, sign(t, provider, logoutClaims(provider))).Code)
	})

	t.Run("without OIDC there is nothing to end", func(t *testing.T) {
		provider, _, _ := setup(t)
		mgr := &authManager{auth: newTestAuthenticator(t), logger: logr.Discard()}
		assert.Equal(t, http.StatusBadRequest, logout(mgr, sign(t, provider, logoutClaims(provider))).Code)
	})
}
