package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const testPostLogoutRedirect = "http://localhost:8080/"

// newOIDCManager returns an auth manager with a token-only authenticator
// that signs users in at the issuer at issuerURL, as the Kitchen configures.
func newOIDCManager(t *testing.T, issuerURL string, signOut deliveryv1alpha1.ProviderSignOut) *authManager {
	t.Helper()
	return &authManager{
		auth: newTokenAuthenticator(), oidc: newOIDCProviderFor(t, issuerURL, signOut), oidcIssuer: issuerURL,
		logger: logr.Discard(),
	}
}

// manyGroups returns n group names of 36 characters, as Microsoft Entra ID
// emits them.
func manyGroups(n int) []string {
	groups := make([]string, n)
	for i := range groups {
		groups[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
	}
	return groups
}

// heldBy returns the session a browser holds after it got cookies: the ones
// it keeps, as it sends them back.
func heldBy(cookies ...*http.Cookie) browserSession {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	for _, c := range cookies {
		if c.MaxAge >= 0 && c.Value != "" {
			r.AddCookie(c)
		}
	}
	return browserSessionOf(r)
}

// assertCleared asserts that res clears the session's cookies.
func assertCleared(t *testing.T, res *sessionResult) {
	t.Helper()
	for _, name := range []string{refreshCookieName, providerTokenCookie, oidcIDTokenCookie} {
		assert.Equal(t, -1, cookieNamed(t, res.cookies, name).MaxAge, "%s is cleared", name)
	}
}

func TestSignInAdmin(t *testing.T) {
	mgr := &authManager{auth: newTestAuthenticator(t), adminLogin: true, logger: logr.Discard()}

	t.Run("the admin's credentials start an admin session", func(t *testing.T) {
		res, err := mgr.signInAdmin(testUsername, testPassword)
		require.NoError(t, err)
		got, err := mgr.auth.parseToken(res.tokens.accessToken, accessTokenType)
		require.NoError(t, err)
		assert.Equal(t, &Identity{Subject: testUsername, Provider: providerAdmin}, got.identity)
		assert.True(t, got.session.ProviderCheckAt.IsZero(), "an admin session is not checked at a provider")

		refresh := cookieNamed(t, res.cookies, refreshCookieName)
		assert.Equal(t, res.tokens.refreshToken, refresh.Value)
		assert.Equal(t, refreshCookiePath, refresh.Path)
		assert.True(t, refresh.HttpOnly)
		assert.Equal(t, -1, cookieNamed(t, res.cookies, providerTokenCookie).MaxAge,
			"the provider token of an earlier OIDC session is dropped")
		assert.Equal(t, -1, cookieNamed(t, res.cookies, oidcIDTokenCookie).MaxAge,
			"the ID token of an earlier OIDC session is dropped, so the sign-out stays at Kokumi")
	})

	for _, tc := range []struct {
		name               string
		mgr                *authManager
		username, password string
		want               error
	}{
		{"a wrong password", mgr, testUsername, "wrong", errInvalidCredentials},
		{"another username", mgr, testRootUser, testPassword, errInvalidCredentials},
		{"no admin account", &authManager{auth: newTokenAuthenticator()}, testUsername, testPassword, errAdminLoginDisabled},
		{"no signing key yet", &authManager{}, testUsername, testPassword, errAuthUnavailable},
	} {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			res, err := tc.mgr.signInAdmin(tc.username, tc.password)
			assert.ErrorIs(t, err, tc.want)
			assert.Nil(t, res.tokens)
			assert.Empty(t, res.cookies)
		})
	}
}

func TestStartSession(t *testing.T) {
	mgr := &authManager{auth: newTokenAuthenticator(), logger: logr.Discard()}
	id := &Identity{Subject: testSubject, Provider: providerOIDC, Issuer: testIssuerURL}
	// Tokens carry times in whole seconds.
	checkAt := time.Now().Add(time.Hour).Truncate(time.Second)
	start := func(t *testing.T, in signIn) (*sessionResult, *verifiedToken) {
		t.Helper()
		res, err := mgr.startSession(in)
		require.NoError(t, err)
		session, err := mgr.auth.parseToken(res.tokens.refreshToken, refreshTokenType)
		require.NoError(t, err)
		return res, session
	}

	t.Run("an OIDC sign-in keeps the provider's refresh token for its session", func(t *testing.T) {
		res, session := start(t, signIn{
			identity: id, providerSessionID: "op-1", idToken: "raw-id-token",
			providerRefreshToken: firstRefreshToken, providerCheckAt: checkAt,
		})
		assert.Equal(t, id, session.identity)
		assert.Equal(t, "op-1", session.session.ProviderSessionID)
		assert.Equal(t, checkAt, session.session.ProviderCheckAt)
		assert.WithinDuration(t, time.Now(), session.session.AuthTime, 2*time.Second)

		providerCookie := cookieNamed(t, res.cookies, providerTokenCookie)
		kept, err := mgr.auth.openProviderToken(providerCookie.Value)
		require.NoError(t, err)
		assert.Equal(t, providerToken{RefreshToken: firstRefreshToken, SessionID: session.session.ID}, kept)
		assert.Equal(t, cookieNamed(t, res.cookies, refreshCookieName).MaxAge, providerCookie.MaxAge,
			"both cookies expire at the idle deadline")

		idToken := cookieNamed(t, res.cookies, oidcIDTokenCookie)
		assert.Equal(t, "raw-id-token", idToken.Value)
		assert.Equal(t, oidcIDTokenCookiePath, idToken.Path, "only the sign-out reads the ID token")
		assert.InDelta(t, defaultSessionTTL.Seconds(), idToken.MaxAge, 2, "the ID token lasts as long as the session")
	})

	t.Run("without a provider refresh token the session is not checked", func(t *testing.T) {
		res, session := start(t, signIn{identity: id, idToken: "raw-id-token"})
		assert.True(t, session.session.ProviderCheckAt.IsZero())
		assert.Equal(t, -1, cookieNamed(t, res.cookies, providerTokenCookie).MaxAge)
	})

	tooLarge := func(t *testing.T, in signIn) {
		t.Helper()
		res, err := mgr.startSession(in)
		require.ErrorIs(t, err, errSessionTooLarge)
		assert.Nil(t, res.tokens)
		assert.Empty(t, res.cookies)
		w := httptest.NewRecorder()
		respondSessionError(w, err)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	}

	t.Run("a refresh token too large for a cookie fails the sign-in", func(t *testing.T) {
		many := &Identity{Subject: testSubject, Provider: providerOIDC, Issuer: testIssuerURL, Groups: manyGroups(200)}
		tooLarge(t, signIn{identity: many})
	})

	t.Run("a provider refresh token too large for a cookie fails the sign-in", func(t *testing.T) {
		tooLarge(t, signIn{identity: id, providerRefreshToken: strings.Repeat("x", maxCookieLen), providerCheckAt: checkAt})
	})

	t.Run("a refresh token and a provider token that fit alone but not together fail the sign-in", func(t *testing.T) {
		many := &Identity{Subject: testSubject, Provider: providerOIDC, Issuer: testIssuerURL, Groups: manyGroups(60)}
		res, err := mgr.startSession(signIn{identity: many})
		require.NoError(t, err)
		require.Greater(t, len(res.tokens.refreshToken), 3300, "the refresh token alone fits")
		tooLarge(t, signIn{identity: many, providerRefreshToken: strings.Repeat("x", 2700), providerCheckAt: checkAt})
	})

	t.Run("an ID token too large for a cookie is left out", func(t *testing.T) {
		res, _ := start(t, signIn{identity: id, idToken: strings.Repeat("x", maxCookieLen+1)})
		assert.Equal(t, -1, cookieNamed(t, res.cookies, oidcIDTokenCookie).MaxAge)
	})

	t.Run("an ID token that would take the session's cookies beyond their limit is left out", func(t *testing.T) {
		in := signIn{
			identity: id, idToken: strings.Repeat("x", maxCookieLen),
			providerRefreshToken: strings.Repeat("x", 2000), providerCheckAt: checkAt,
		}
		res, session := start(t, in)
		assert.Equal(t, -1, cookieNamed(t, res.cookies, oidcIDTokenCookie).MaxAge)
		assert.Equal(t, checkAt, session.session.ProviderCheckAt, "the session is still checked at the provider")

		in.idToken = strings.Repeat("x", 1000)
		res, _ = start(t, in)
		assert.Equal(t, in.idToken, cookieNamed(t, res.cookies, oidcIDTokenCookie).Value, "one that fits stays")
	})

	t.Run("no signing key yet", func(t *testing.T) {
		_, err := (&authManager{}).startSession(signIn{identity: id})
		assert.ErrorIs(t, err, errAuthUnavailable)
	})

	t.Run("an identity without a subject is a server error", func(t *testing.T) {
		res, err := mgr.startSession(signIn{identity: &Identity{Provider: providerOIDC}})
		require.Error(t, err)
		assert.Empty(t, res.cookies)
		w := httptest.NewRecorder()
		respondSessionError(w, err)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// TestOIDCSignInUsesTheCurrentSigningKey covers a rotated signing key while
// the OIDC Secret cannot be read: the reload keeps the OIDC provider, and
// sign-ins mint with the new key, which the middleware and refreshes check.
func TestOIDCSignInUsesTheCurrentSigningKey(t *testing.T) {
	provider := newFakeProvider(t, func(p *fakeProvider) { p.refreshTokens = true })
	oldKey, newKey := []byte("old-signing-key-of-at-least-32-bytes!!"), []byte("new-signing-key-of-at-least-32-bytes!!")
	var oidcSecretUnreadable bool
	c := fake.NewClientBuilder().WithObjects(&corev1.Secret{
		Name: deliveryv1alpha1.DefaultTokenSigningKeySecretName, Namespace: testNS,
		Data: map[string][]byte{secretKeySigningKey: oldKey},
	}, &corev1.Secret{
		Name: testOIDCSecret, Namespace: testNS,
		Data: map[string][]byte{secretKeyClientSecret: []byte(testClientSec)},
	}).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if oidcSecretUnreadable && key.Name == testOIDCSecret {
				return errors.New("connection refused")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}).Build()
	mgr := &authManager{reader: c, apiReader: c, ns: testNS, logger: logr.Discard()}
	kitchen := authKitchen(&deliveryv1alpha1.KitchenAuth{OIDC: deliveryv1alpha1.OIDCConfig{
		IssuerURL:       provider.URL,
		ClientID:        testClientID,
		ClientSecretRef: &corev1.LocalObjectReference{Name: testOIDCSecret},
	}})
	mgr.reload(context.Background(), kitchen)
	require.NotNil(t, mgr.getOIDC())

	tokenSecret := &corev1.Secret{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: deliveryv1alpha1.DefaultTokenSigningKeySecretName}, tokenSecret))
	tokenSecret.Data[secretKeySigningKey] = newKey
	require.NoError(t, c.Update(context.Background(), tokenSecret))
	oidcSecretUnreadable = true
	mgr.reload(context.Background(), kitchen)
	require.NotNil(t, mgr.getOIDC(), "an unreadable OIDC Secret keeps the provider")

	b := heldBy(signInOIDC(t, mgr)...)
	current := newAuthenticator("", nil, newKey)
	_, err := current.parseToken(b.refreshToken, refreshTokenType)
	require.NoError(t, err, "the session is signed with the new key")
	_, err = current.openProviderToken(b.providerToken)
	require.NoError(t, err, "the provider token is sealed with the new key")
	_, err = mgr.refreshSession(context.Background(), b, true)
	assert.NoError(t, err)
}

func TestRefreshSession(t *testing.T) {
	ended, _ := newTestEndedSessions(t)
	mgr := &authManager{
		auth: newTestAuthenticator(t), adminLogin: true, oidcIssuer: testIssuerURL, logger: logr.Discard(), ended: ended,
	}
	start := func(t *testing.T, id *Identity) *sessionResult {
		t.Helper()
		res, err := mgr.startSession(signIn{identity: id})
		require.NoError(t, err)
		return res
	}

	t.Run("a refresh keeps the session and its whole identity", func(t *testing.T) {
		// An OIDC session must not turn into the admin's, which would map
		// the user to the admin's ServiceAccount.
		id := &Identity{
			Subject: "admin@example.com", Email: "admin@example.com", Groups: []string{"devops"},
			Provider: providerOIDC, Issuer: testIssuerURL,
		}
		signedIn := start(t, id)
		res, err := mgr.refreshSession(context.Background(), heldBy(signedIn.cookies...), false)
		require.NoError(t, err)
		got, err := mgr.auth.parseToken(res.tokens.accessToken, accessTokenType)
		require.NoError(t, err)
		assert.Equal(t, id, got.identity)
		before, err := mgr.auth.parseToken(signedIn.tokens.refreshToken, refreshTokenType)
		require.NoError(t, err)
		assert.Equal(t, before.session, got.session)

		require.Len(t, res.cookies, 1, "a session without a provider token sets only the refresh cookie")
		assert.Equal(t, res.tokens.refreshToken, cookieNamed(t, res.cookies, refreshCookieName).Value)
	})

	other := newAuthenticator(testUsername, nil, []byte("another-signing-key-of-32-bytes-or-more"))
	foreign, err := other.signInTokens(&Identity{Subject: testUsername, Provider: providerAdmin}, newSessionState(time.Now()))
	require.NoError(t, err)
	signedOut := start(t, &Identity{Subject: testUsername, Provider: providerAdmin})
	signedOutToken, err := mgr.auth.parseToken(signedOut.tokens.refreshToken, refreshTokenType)
	require.NoError(t, err)
	require.NoError(t, ended.record(context.Background(), endedSessionKey(signedOutToken.session.ID), time.Now()))

	for _, tc := range []struct {
		name    string
		session browserSession
		want    error
	}{
		{"no refresh token", browserSession{}, errSessionInvalid},
		{"an access token", browserSession{refreshToken: signedOut.tokens.accessToken}, errSessionInvalid},
		{"a token of another signing key", browserSession{refreshToken: foreign.refreshToken}, errSessionInvalid},
		{"a signed-out session", browserSession{refreshToken: signedOut.tokens.refreshToken}, errSessionEnded},
	} {
		t.Run(tc.name+" ends the session", func(t *testing.T) {
			res, err := mgr.refreshSession(context.Background(), tc.session, true)
			assert.ErrorIs(t, err, tc.want)
			assert.Nil(t, res.tokens)
			assertCleared(t, res)
		})
	}

	t.Run("no signing key yet keeps the cookies", func(t *testing.T) {
		res, err := (&authManager{}).refreshSession(context.Background(), browserSession{refreshToken: "rt"}, true)
		assert.ErrorIs(t, err, errAuthUnavailable)
		assert.Empty(t, res.cookies)
	})
}

func TestSessionLifetime(t *testing.T) {
	auth := newTestAuthenticator(t)
	mgr := &authManager{auth: auth, adminLogin: true, logger: logr.Discard()}
	id := &Identity{Subject: testUsername, Provider: providerAdmin}

	// session returns the refresh token of a session the user signed in to
	// at signedIn, whose idle deadline is idleUntil.
	session := func(t *testing.T, signedIn, idleUntil time.Time) (string, sessionState) {
		t.Helper()
		state := newSessionState(signedIn)
		s, err := auth.issueTokens(signedIn, id, state, idleUntil)
		require.NoError(t, err)
		return s.refreshToken, state
	}
	refresh := func(refreshToken string, active bool) (*sessionResult, error) {
		return mgr.refreshSession(context.Background(), browserSession{refreshToken: refreshToken}, active)
	}
	// refreshed returns the token of the refresh cookie res sets.
	refreshed := func(t *testing.T, res *sessionResult, err error) *verifiedToken {
		t.Helper()
		require.NoError(t, err)
		cookie := cookieNamed(t, res.cookies, refreshCookieName)
		got, err := auth.parseToken(cookie.Value, refreshTokenType)
		require.NoError(t, err)
		assert.InDelta(t, time.Until(got.expires).Seconds(), cookie.MaxAge, 2, "the cookie expires with the token")
		return got
	}

	t.Run("a refresh with activity moves the idle deadline", func(t *testing.T) {
		token, state := session(t, time.Now().Add(-time.Hour), time.Now().Add(10*time.Minute))
		res, err := refresh(token, true)
		got := refreshed(t, res, err)
		assert.WithinDuration(t, time.Now().Add(defaultSessionIdleTTL), got.expires, 2*time.Second)
		assert.Equal(t, state.ID, got.session.ID, "a refresh keeps the session")
	})

	t.Run("a background refresh keeps the idle deadline", func(t *testing.T) {
		idleUntil := time.Now().Add(10 * time.Minute)
		token, _ := session(t, time.Now().Add(-time.Hour), idleUntil)
		res, err := refresh(token, false)
		assert.WithinDuration(t, idleUntil, refreshed(t, res, err).expires, 2*time.Second)
	})

	t.Run("an idle session cannot be refreshed", func(t *testing.T) {
		token, _ := session(t, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Second))
		res, err := refresh(token, true)
		assert.ErrorIs(t, err, errSessionInvalid)
		assertCleared(t, res)
	})

	t.Run("activity never extends a session beyond sessionTTL after its sign-in", func(t *testing.T) {
		signedIn := time.Now().Add(-defaultSessionTTL + time.Minute)
		token, _ := session(t, signedIn, time.Now().Add(time.Minute))
		res, err := refresh(token, true)
		got := refreshed(t, res, err)
		assert.WithinDuration(t, signedIn.Add(defaultSessionTTL), got.expires, 2*time.Second)
	})

	t.Run("a session past sessionTTL ends, as after the setting was lowered", func(t *testing.T) {
		token, _ := session(t, time.Now().Add(-defaultSessionTTL-time.Minute), time.Now().Add(time.Hour))
		_, err := refresh(token, true)
		assert.ErrorIs(t, err, errSessionInvalid)
	})

	t.Run("an access token expires at the idle deadline", func(t *testing.T) {
		idleUntil := time.Now().Add(time.Minute)
		s, err := auth.issueTokens(time.Now(), id, newSessionState(time.Now()), idleUntil)
		require.NoError(t, err)
		assert.Equal(t, idleUntil, s.accessExpiresAt)
	})

	t.Run("a sign-in starts with idleTTL", func(t *testing.T) {
		res, err := mgr.signInAdmin(testUsername, testPassword)
		got := refreshed(t, res, err)
		assert.WithinDuration(t, time.Now().Add(defaultSessionIdleTTL), got.expires, 2*time.Second)
		assert.WithinDuration(t, time.Now(), got.session.AuthTime, 2*time.Second)
		assert.NotEmpty(t, got.session.ID)
		assert.WithinDuration(t, time.Now().Add(defaultAccessTokenTTL), res.tokens.accessExpiresAt, time.Second)
	})
}

func TestOIDCSessionFollowsTheProvider(t *testing.T) {
	// signIn signs in at a new fake provider, configured by configure, and
	// returns it with the auth manager and the session the browser holds.
	signIn := func(t *testing.T, configure func(*fakeProvider)) (*fakeProvider, *authManager, browserSession) {
		t.Helper()
		provider := newFakeProvider(t, func(p *fakeProvider) {
			p.refreshTokens = true
			configure(p)
		})
		mgr := newOIDCManager(t, provider.URL, deliveryv1alpha1.ProviderSignOutNever)
		return provider, mgr, heldBy(signInOIDC(t, mgr)...)
	}
	due := func(p *fakeProvider) { p.expiresIn = -1 }
	refresh := func(mgr *authManager, b browserSession) (*sessionResult, error) {
		return mgr.refreshSession(context.Background(), b, true)
	}
	identityOf := func(t *testing.T, mgr *authManager, res *sessionResult, err error) *Identity {
		t.Helper()
		require.NoError(t, err)
		got, err := mgr.auth.parseToken(res.tokens.accessToken, accessTokenType)
		require.NoError(t, err)
		return got.identity
	}
	refreshes := func(p *fakeProvider) int { return p.refreshes }

	t.Run("the sign-in keeps the provider's refresh token for its session", func(t *testing.T) {
		_, mgr, b := signIn(t, func(*fakeProvider) {})
		session, err := mgr.auth.parseToken(b.refreshToken, refreshTokenType)
		require.NoError(t, err)
		kept, err := mgr.auth.openProviderToken(b.providerToken)
		require.NoError(t, err)
		assert.Equal(t, providerToken{RefreshToken: firstRefreshToken, SessionID: session.session.ID}, kept)
		assert.WithinDuration(t, time.Now().Add(time.Hour), session.session.ProviderCheckAt, 2*time.Second,
			"the provider is asked again when its access token expires")
	})

	t.Run("a refresh before the check is due does not ask the provider", func(t *testing.T) {
		provider, mgr, b := signIn(t, func(*fakeProvider) {})
		res, err := refresh(mgr, b)
		identityOf(t, mgr, res, err)
		assert.Zero(t, get(provider, refreshes))
		assert.NotEmpty(t, cookieNamed(t, res.cookies, providerTokenCookie).Value,
			"the provider token cookie expires with the rotated refresh cookie")
	})

	t.Run("a due check takes the identity from the provider and rotates its refresh token", func(t *testing.T) {
		provider, mgr, b := signIn(t, due)
		provider.set(func(p *fakeProvider) { p.groups = []string{"release-approvers"} })

		res, err := refresh(mgr, b)
		assert.Equal(t, []string{"release-approvers"}, identityOf(t, mgr, res, err).Groups)
		assert.Equal(t, 1, get(provider, refreshes))
		kept, err := mgr.auth.openProviderToken(cookieNamed(t, res.cookies, providerTokenCookie).Value)
		require.NoError(t, err)
		assert.Equal(t, "refresh-2", kept.RefreshToken)

		res, err = refresh(mgr, heldBy(res.cookies...))
		identityOf(t, mgr, res, err)
		assert.Equal(t, 2, get(provider, refreshes), "the rotated token works")
	})

	for _, code := range []string{"invalid_grant", "invalid_request"} {
		t.Run(code+" ends the session", func(t *testing.T) {
			provider, mgr, b := signIn(t, due)
			provider.set(func(p *fakeProvider) { p.refreshError = code })
			res, err := refresh(mgr, b)
			assert.ErrorIs(t, err, errProviderEndedSession)
			assertCleared(t, res)
			assert.Equal(t, 1, get(provider, refreshes), "the provider is asked once")
		})
	}

	t.Run("an ID token for another user ends the session", func(t *testing.T) {
		provider, mgr, b := signIn(t, due)
		provider.set(func(p *fakeProvider) { p.subject = "user-2" })
		_, err := refresh(mgr, b)
		assert.ErrorIs(t, err, errProviderEndedSession)
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
			provider, mgr, b := signIn(t, due)
			provider.set(pause.answer)
			res, err := refresh(mgr, b)
			assert.ErrorIs(t, err, errProviderUnavailable)
			assert.Empty(t, res.cookies, "the cookies stay")
			assert.Equal(t, 1, get(provider, refreshes), "the refresh token is sent once, not again in the other style")

			provider.set(pause.heal)
			res, err = refresh(mgr, b)
			identityOf(t, mgr, res, err)
		})
	}

	t.Run("an unreachable provider keeps the session for a later try", func(t *testing.T) {
		provider, mgr, b := signIn(t, due)
		provider.Close()
		_, err := refresh(mgr, b)
		assert.ErrorIs(t, err, errProviderUnavailable)
	})

	t.Run("a session whose OIDC provider is no longer configured ends", func(t *testing.T) {
		provider, mgr, b := signIn(t, due)
		mgr.oidc, mgr.oidcIssuer = nil, ""
		res, err := refresh(mgr, b)
		assert.ErrorIs(t, err, errSessionEnded)
		assertCleared(t, res)
		assert.Zero(t, get(provider, refreshes), "the provider is not asked")
	})

	t.Run("a configured provider that cannot be built keeps the session for a later try", func(t *testing.T) {
		// As while its client Secret cannot be read.
		_, mgr, b := signIn(t, due)
		p := mgr.oidc
		mgr.oidc = nil
		res, err := refresh(mgr, b)
		assert.ErrorIs(t, err, errProviderUnavailable)
		assert.Empty(t, res.cookies, "the cookies stay")

		mgr.oidc = p
		res, err = refresh(mgr, b)
		identityOf(t, mgr, res, err)
	})

	t.Run("groups that outgrow the browser's cookies at a check end the session", func(t *testing.T) {
		provider, mgr, b := signIn(t, due)
		provider.set(func(p *fakeProvider) { p.groups = manyGroups(200) })
		res, err := refresh(mgr, b)
		assert.ErrorIs(t, err, errSessionTooLarge)
		assertCleared(t, res)
	})

	t.Run("a check takes the email only when the provider verified it", func(t *testing.T) {
		provider, mgr, b := signIn(t, due)
		res, err := refresh(mgr, b)
		assert.Equal(t, "user@example.com", identityOf(t, mgr, res, err).Email)

		provider.set(func(p *fakeProvider) { p.emailUnverified = true })
		res, err = refresh(mgr, heldBy(res.cookies...))
		assert.Empty(t, identityOf(t, mgr, res, err).Email)
	})

	t.Run("a session without its provider token cookie ends", func(t *testing.T) {
		_, mgr, b := signIn(t, func(*fakeProvider) {})
		b.providerToken = ""
		_, err := refresh(mgr, b)
		assert.ErrorIs(t, err, errSessionInvalid)
	})

	t.Run("the provider token cookie of another session is refused", func(t *testing.T) {
		provider, mgr, b := signIn(t, func(*fakeProvider) {})
		b.providerToken = heldBy(signInOIDC(t, mgr)...).providerToken
		_, err := refresh(mgr, b)
		assert.ErrorIs(t, err, errSessionInvalid)
		assert.Zero(t, get(provider, refreshes))
	})

	t.Run("a provider without refresh tokens leaves the session unchecked", func(t *testing.T) {
		provider := newFakeProvider(t, due)
		mgr := newOIDCManager(t, provider.URL, deliveryv1alpha1.ProviderSignOutNever)
		cookies := signInOIDC(t, mgr)
		assert.Equal(t, -1, cookieNamed(t, cookies, providerTokenCookie).MaxAge)
		b := heldBy(cookies...)
		session, err := mgr.auth.parseToken(b.refreshToken, refreshTokenType)
		require.NoError(t, err)
		assert.True(t, session.session.ProviderCheckAt.IsZero())

		res, err := refresh(mgr, b)
		identityOf(t, mgr, res, err)
		assert.Zero(t, get(provider, refreshes))
	})
}

func TestEndSession(t *testing.T) {
	end := func(mgr *authManager, b browserSession) *sessionResult {
		return mgr.endSession(context.Background(), b, testPostLogoutRedirect)
	}

	t.Run("with providerSignOut Always an OIDC session ends at the provider", func(t *testing.T) {
		issuer := newFakeIssuer(t, true)
		mgr := newOIDCManager(t, issuer.URL, deliveryv1alpha1.ProviderSignOutAlways)
		b := heldBy(signInOIDC(t, mgr)...)
		require.NotEmpty(t, b.idToken)

		res := end(mgr, b)
		endSession, err := url.Parse(res.endSessionURL)
		require.NoError(t, err)
		assert.Equal(t, issuer.URL+"/logout", endSession.Scheme+"://"+endSession.Host+endSession.Path)
		assert.Equal(t, url.Values{
			"id_token_hint":            {b.idToken},
			"client_id":                {testClientID},
			"post_logout_redirect_uri": {testPostLogoutRedirect},
		}, endSession.Query())
		assertCleared(t, res)
		reauth := cookieNamed(t, res.cookies, oidcReauthCookie)
		assert.Positive(t, reauth.MaxAge,
			"the next sign-in asks for credentials, in case the user cancels the sign-out at the provider")
		assert.Equal(t, oidcCookiePath, reauth.Path)
	})

	t.Run("a provider without RP-initiated logout leaves re-authentication to the next sign-in", func(t *testing.T) {
		mgr := newOIDCManager(t, newFakeIssuer(t, false).URL, deliveryv1alpha1.ProviderSignOutAlways)
		res := end(mgr, heldBy(signInOIDC(t, mgr)...))
		assert.Empty(t, res.endSessionURL)
		assert.Positive(t, cookieNamed(t, res.cookies, oidcReauthCookie).MaxAge)
	})

	t.Run("with providerSignOut Never the provider's session stays", func(t *testing.T) {
		mgr := newOIDCManager(t, newFakeIssuer(t, true).URL, deliveryv1alpha1.ProviderSignOutNever)
		res := end(mgr, heldBy(signInOIDC(t, mgr)...))
		assert.Empty(t, res.endSessionURL, "no end-session URL, though the provider offers one")
		assertCleared(t, res)
		assert.Nil(t, findCookie(res.cookies, oidcReauthCookie), "the next sign-in uses the provider's session")
	})

	t.Run("an admin session stays at Kokumi with OIDC configured", func(t *testing.T) {
		mgr := newOIDCManager(t, newFakeIssuer(t, true).URL, deliveryv1alpha1.ProviderSignOutAlways)
		signedIn, err := mgr.startSession(signIn{identity: &Identity{Subject: testUsername, Provider: providerAdmin}})
		require.NoError(t, err)
		res := end(mgr, heldBy(signedIn.cookies...))
		assert.Empty(t, res.endSessionURL)
		assert.Nil(t, findCookie(res.cookies, oidcReauthCookie))
	})

	t.Run("the cookies go also without an authenticator", func(t *testing.T) {
		// The signing key may not be resolved yet, e.g. right after a
		// restart; the browser's cookies must go anyway.
		res := end(&authManager{}, browserSession{refreshToken: "rt", idToken: "id"})
		assertCleared(t, res)
		assert.Nil(t, findCookie(res.cookies, oidcReauthCookie), "no OIDC provider, no re-authentication")
	})

	for _, revocation := range []bool{true, false} {
		t.Run(fmt.Sprintf("the provider's refresh token is revoked (revocation endpoint %t)", revocation), func(t *testing.T) {
			provider := newFakeProvider(t, func(p *fakeProvider) {
				p.refreshTokens = true
				p.revocation = revocation
			})
			mgr := newOIDCManager(t, provider.URL, deliveryv1alpha1.ProviderSignOutNever)
			assertCleared(t, end(mgr, heldBy(signInOIDC(t, mgr)...)))
			revoked := get(provider, func(p *fakeProvider) []string { return p.revoked })
			if revocation {
				assert.Equal(t, []string{firstRefreshToken}, revoked)
			} else {
				assert.Empty(t, revoked)
			}
		})
	}
}

func TestRespondSessionError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{errAuthUnavailable, http.StatusServiceUnavailable},
		{errProviderUnavailable, http.StatusServiceUnavailable},
		{errAdminLoginDisabled, http.StatusForbidden},
		{errInvalidCredentials, http.StatusUnauthorized},
		{errSessionInvalid, http.StatusUnauthorized},
		{errSessionEnded, http.StatusUnauthorized},
		{errProviderEndedSession, http.StatusUnauthorized},
		{fmt.Errorf("%w: invalid_grant from the provider", errProviderEndedSession), http.StatusUnauthorized},
		{errors.New("signing failed"), http.StatusInternalServerError},
	} {
		w := httptest.NewRecorder()
		respondSessionError(w, tc.err)
		assert.Equal(t, tc.want, w.Code, tc.err.Error())
		assert.NotContains(t, w.Body.String(), "invalid_grant", "details are not told the client")
	}
}

// TestSessionsEndWithWhatSignedThemIn covers sessions whose login method,
// admin credentials or issuer changed after the sign-in: the middleware
// refuses their access tokens, and a refresh their refresh tokens.
func TestSessionsEndWithWhatSignedThemIn(t *testing.T) {
	admin := newTestAuthenticator(t)
	adminID := &Identity{Subject: testUsername, Provider: providerAdmin}
	oidcID := &Identity{Subject: testSubject, Provider: providerOIDC, Issuer: testIssuerURL}
	// signedIn returns a manager in the state the sessions sign in under.
	signedIn := func() *authManager {
		return &authManager{auth: admin, adminLogin: true, oidcIssuer: testIssuerURL, logger: logr.Discard()}
	}
	withAdmin := func(username string, passwordHash []byte) func(*authManager) {
		return func(m *authManager) {
			m.auth = m.auth.withAdmin(&adminCredentials{username: username, passwordHash: passwordHash})
		}
	}

	for _, tc := range []struct {
		name   string
		id     *Identity
		change func(*authManager)
		ends   bool
	}{
		{"an unchanged admin account keeps its session", adminID, withAdmin(testUsername, admin.passwordHash), false},
		{"turning admin off ends an admin session", adminID, func(m *authManager) { m.auth = m.auth.withAdmin(nil) }, true},
		{"a new admin password ends an admin session", adminID, withAdmin(testUsername, mustBcrypt(t, "new-password")), true},
		{"a new admin username ends an admin session", adminID, withAdmin(testRootUser, admin.passwordHash), true},
		{"removing OIDC leaves an admin session", adminID, func(m *authManager) { m.oidcIssuer = "" }, false},
		{"an unchanged issuer keeps an OIDC session", oidcID, func(*authManager) {}, false},
		{"removing OIDC ends an OIDC session", oidcID, func(m *authManager) { m.oidcIssuer = "" }, true},
		{"another issuer ends an OIDC session", oidcID, func(m *authManager) { m.oidcIssuer = testOtherIssuerURL }, true},
		{"turning admin off leaves an OIDC session", oidcID, func(m *authManager) { m.auth = m.auth.withAdmin(nil) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := signedIn()
			tokens, err := m.auth.signInTokens(tc.id, newSessionState(time.Now()))
			require.NoError(t, err)
			tc.change(m)

			res, err := m.refreshSession(context.Background(), browserSession{refreshToken: tokens.refreshToken}, true)
			if tc.ends {
				assert.Equal(t, http.StatusUnauthorized, protectedRequest(m, tokens.accessToken))
				assert.ErrorIs(t, err, errSessionEnded)
				assertCleared(t, res)
				return
			}
			assert.Equal(t, http.StatusOK, protectedRequest(m, tokens.accessToken))
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, protectedRequest(m, res.tokens.accessToken), "the refreshed session goes on")
		})
	}

	t.Run("an admin token without the password fingerprint is refused", func(t *testing.T) {
		// As one issued before tokens carried it, or by a token-only
		// authenticator with the same signing key.
		tokens, err := newTokenAuthenticator().signInTokens(adminID, newSessionState(time.Now()))
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, protectedRequest(signedIn(), tokens.accessToken))
	})

	t.Run("a token of an unknown login method is refused", func(t *testing.T) {
		tokens, err := admin.signInTokens(&Identity{Subject: testSubject, Provider: "ldap"}, newSessionState(time.Now()))
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, protectedRequest(signedIn(), tokens.accessToken))
	})
}

// TestAuthManagerOIDCSessionsFollowTheKitchen covers reloads of the Kitchen's
// OIDC configuration while a user is signed in through OIDC.
func TestAuthManagerOIDCSessionsFollowTheKitchen(t *testing.T) {
	const newOIDCSecret = "kokumi-server-oidc-2"
	oidcSecret := func(name string) *corev1.Secret {
		return &corev1.Secret{
			Name: name, Namespace: testNS,
			Data: map[string][]byte{secretKeyClientSecret: []byte(testClientSec)},
		}
	}
	kitchen := func(issuerURL, secret string) *deliveryv1alpha1.Kitchen {
		return authKitchen(&deliveryv1alpha1.KitchenAuth{OIDC: deliveryv1alpha1.OIDCConfig{
			IssuerURL: issuerURL, ClientID: testClientID,
			ClientSecretRef: &corev1.LocalObjectReference{Name: secret},
		}})
	}
	// setup signs a user in through OIDC at a fake provider and returns the
	// manager, its client, the provider's URL and the session's access token.
	setup := func(t *testing.T, get func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error) (*authManager, client.Client, string, string) {
		t.Helper()
		provider := newFakeProvider(t, func(*fakeProvider) {})
		c := fake.NewClientBuilder().WithObjects(testTokenSecret(), oidcSecret(testOIDCSecret)).
			WithInterceptorFuncs(interceptor.Funcs{Get: get}).Build()
		m := &authManager{reader: c, apiReader: c, ns: testNS, logger: logr.Discard()}
		m.reload(context.Background(), kitchen(provider.URL, testOIDCSecret))
		access := accessTokenFromCallback(t, oidcCallback(t, m))
		require.Equal(t, http.StatusOK, protectedRequest(m, access))
		return m, c, provider.URL, access
	}
	passThrough := func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		return c.Get(ctx, key, obj, opts...)
	}

	t.Run("removing OIDC ends its sessions", func(t *testing.T) {
		m, _, _, access := setup(t, passThrough)
		m.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{}))
		assert.Nil(t, m.getOIDC())
		assert.Equal(t, http.StatusUnauthorized, protectedRequest(m, access))
	})

	t.Run("another issuer ends the sessions of the old one, even while its provider cannot be built", func(t *testing.T) {
		m, _, _, access := setup(t, passThrough)
		m.reload(context.Background(), kitchen(testOtherIssuerURL, newOIDCSecret))
		assert.Nil(t, m.getOIDC(), "the old issuer no longer signs anyone in")
		assert.Equal(t, newOIDCSecret, m.oidcSecretName(), "creating the new Secret is noticed")
		assert.Equal(t, http.StatusUnauthorized, protectedRequest(m, access))
	})

	t.Run("an OIDC Secret that cannot be read keeps the provider and its sessions", func(t *testing.T) {
		var unreadable bool
		m, _, issuer, access := setup(t, func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if unreadable && key.Name == testOIDCSecret {
				return errors.New("connection refused")
			}
			return c.Get(ctx, key, obj, opts...)
		})
		unreadable = true
		m.reload(context.Background(), kitchen(issuer, testOIDCSecret))
		assert.NotNil(t, m.getOIDC())
		assert.Equal(t, http.StatusOK, protectedRequest(m, access))
	})

	t.Run("a deleted OIDC Secret keeps the sessions of the configured issuer", func(t *testing.T) {
		m, c, issuer, access := setup(t, passThrough)
		require.NoError(t, c.Delete(context.Background(), oidcSecret(testOIDCSecret)))
		m.reload(context.Background(), kitchen(issuer, testOIDCSecret))
		assert.Equal(t, http.StatusOK, protectedRequest(m, access))
	})
}
