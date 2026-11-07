package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// The outcomes of a session operation that issues no tokens.
// respondSessionError answers them.
var (
	// errAuthUnavailable: no token signing key was read yet, so no session
	// can start or go on.
	errAuthUnavailable = errors.New("authentication is not available")
	// errAdminLoginDisabled: there is no admin account to sign in to.
	errAdminLoginDisabled = errors.New("admin login is disabled")
	// errInvalidCredentials: the username or the password is wrong.
	errInvalidCredentials = errors.New("invalid username or password")
	// errSessionInvalid: the browser holds no valid refresh token of a
	// running session, or holds a provider token cookie that does not
	// belong to it.
	errSessionInvalid = errors.New("invalid or expired refresh token")
	// errSessionEnded: the session ended before its tokens expire: at a
	// sign-out or a back-channel logout, which the record of ended sessions
	// holds, or because its OIDC provider is no longer configured or its
	// provider token cannot be kept.
	errSessionEnded = errors.New("the session has ended")
	// errProviderUnavailable: the session's check at the OIDC provider is
	// due, and the provider cannot be asked right now. The session stays.
	errProviderUnavailable = errors.New("the session cannot be checked at the OIDC provider")
)

// browserSession is what a browser holds of its session: the values of its
// session cookies, "" for one it does not send.
type browserSession struct {
	// refreshToken is the value of refreshCookieName.
	refreshToken string
	// providerToken is the sealed value of providerTokenCookie.
	providerToken string
	// idToken is the value of oidcIDTokenCookie, which the browser sends
	// only to the sign-out.
	idToken string
}

// browserSessionOf returns the session cookies r carries.
func browserSessionOf(r *http.Request) browserSession {
	value := func(name string) string {
		c, err := r.Cookie(name)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(c.Value)
	}
	return browserSession{
		refreshToken:  value(refreshCookieName),
		providerToken: value(providerTokenCookie),
		idToken:       value(oidcIDTokenCookie),
	}
}

// signIn is a sign-in, which starts a session for identity.
type signIn struct {
	identity *Identity
	// providerSessionID is the sid of the OIDC provider's session the user
	// signed in with, when the provider names it.
	providerSessionID string
	// idToken is the OIDC provider's ID token, the hint of a sign-out at the
	// provider.
	idToken string
	// providerRefreshToken is the OIDC provider's refresh token, or "" when
	// it issued none: the session is then not checked at the provider.
	providerRefreshToken string
	// providerCheckAt is when to ask the provider again whether the user
	// stays signed in; set with providerRefreshToken.
	providerCheckAt time.Time
}

// sessionResult is what a session operation hands the browser. An
// operation returns one also when it refuses: it then holds the cookies of a
// session that is over, to clear.
type sessionResult struct {
	// tokens are the tokens issued, or nil.
	tokens *sessionTokens
	// cookies are the cookies to set, or to clear (MaxAge -1). They are not
	// Secure yet; writeCookies makes them so over HTTPS.
	cookies []*http.Cookie
	// endSessionURL, after a sign-out, is where the browser goes to sign out
	// at the OIDC provider, or "" when it does not.
	endSessionURL string
}

// signInAdmin starts a session of the admin account when username and
// password are its credentials.
func (m *authManager) signInAdmin(username, password string) (*sessionResult, error) {
	a := m.get()
	if a == nil {
		return &sessionResult{}, errAuthUnavailable
	}
	if !m.adminLoginEnabled() {
		return &sessionResult{}, errAdminLoginDisabled
	}
	if !a.verifyCredentials(username, password) {
		return &sessionResult{}, errInvalidCredentials
	}
	return m.start(a, signIn{identity: &Identity{Subject: a.username, Provider: providerAdmin}})
}

// startSession starts the session of a sign-in at the OIDC provider.
func (m *authManager) startSession(in signIn) (*sessionResult, error) {
	a := m.get()
	if a == nil {
		return &sessionResult{}, errAuthUnavailable
	}
	return m.start(a, in)
}

// start mints the first tokens of a new session for in and sets its cookies.
// The provider's refresh token goes, sealed, into the provider token cookie,
// so that refreshes check the session at the provider; one that cannot be
// kept leaves the session unchecked. A sign-in without provider tokens clears
// the cookies of an earlier OIDC session, so that they do not turn this
// session's sign-out into one at the provider.
func (m *authManager) start(a *authenticator, in signIn) (*sessionResult, error) {
	s := newSessionState(time.Now())
	s.ProviderSessionID = in.providerSessionID
	var sealed string
	if in.providerRefreshToken != "" {
		var err error
		sealed, err = a.sealProviderToken(providerToken{RefreshToken: in.providerRefreshToken, SessionID: s.ID})
		if err != nil {
			m.logger.Error(err, "Could not keep the OIDC provider's refresh token; the session is not checked at the provider")
		} else {
			s.ProviderCheckAt = in.providerCheckAt
		}
	}
	tokens, err := a.signInTokens(in.identity, s)
	if err != nil {
		m.logger.Error(err, "Could not issue the tokens of a new session")
		return &sessionResult{}, err
	}
	return &sessionResult{tokens: tokens, cookies: []*http.Cookie{
		newRefreshCookie(tokens),
		newProviderTokenCookie(sealed, tokens.refreshExpiresAt),
		newIDTokenCookie(in.idToken, a.sessionEnd(s)),
	}}, nil
}

// refreshSession exchanges the browser's refresh token for new tokens. A
// session ends sessionTTL after its sign-in, and idleTTL after the last
// refresh that reported user activity (active). When the session's check at
// the OIDC provider is due, the provider is asked first: a refusal ends the
// session with errProviderEndedSession, and a provider that cannot be asked
// is errProviderUnavailable, which keeps the session for a later try. A
// refusal that ends the session clears its cookies.
func (m *authManager) refreshSession(ctx context.Context, b browserSession, active bool) (*sessionResult, error) {
	a := m.get()
	if a == nil {
		return &sessionResult{}, errAuthUnavailable
	}
	end := func(outcome error) (*sessionResult, error) {
		return &sessionResult{cookies: clearSessionCookies()}, outcome
	}
	now := time.Now()
	t, err := a.parseToken(b.refreshToken, refreshTokenType)
	if err != nil || !now.Before(a.sessionEnd(t.session)) {
		return end(errSessionInvalid)
	}
	if m.ended.ended(t) {
		return end(errSessionEnded)
	}
	id, s := t.identity, t.session

	var providerRefresh string
	if !s.ProviderCheckAt.IsZero() {
		if providerRefresh, err = a.providerTokenOf(b.providerToken, s); err != nil {
			m.logger.Info("Refused a session refresh without its OIDC provider token", "reason", err.Error())
			return end(errSessionInvalid)
		}
		if !now.Before(s.ProviderCheckAt) {
			checked, err := m.checkAtProvider(ctx, providerRefresh, id)
			if errors.Is(err, errProviderUnavailable) {
				return &sessionResult{}, err
			}
			if err != nil {
				return end(err)
			}
			id, s.ProviderCheckAt, providerRefresh = checked.identity, checked.next, checked.refreshToken
		}
	}

	tokens, err := a.issueTokens(now, id, s, a.idleDeadline(now, s, t.expires, active))
	if err != nil {
		m.logger.Error(err, "Could not issue the tokens of a refreshed session")
		return &sessionResult{}, err
	}
	res := &sessionResult{tokens: tokens, cookies: []*http.Cookie{newRefreshCookie(tokens)}}
	if providerRefresh == "" {
		return res, nil
	}
	sealed, err := a.sealProviderToken(providerToken{RefreshToken: providerRefresh, SessionID: s.ID})
	if err != nil {
		m.logger.Error(err, "Could not keep the OIDC provider's refresh token")
		return end(errSessionEnded)
	}
	res.cookies = append(res.cookies, newProviderTokenCookie(sealed, tokens.refreshExpiresAt))
	return res, nil
}

// checkAtProvider asks the OIDC provider whether the user of id stays signed
// in, with the provider's refresh token of the session. It returns
// errProviderEndedSession when the provider refuses, errSessionEnded without
// a configured provider, and errProviderUnavailable when the provider cannot
// be asked.
func (m *authManager) checkAtProvider(ctx context.Context, refreshToken string, id *Identity) (*providerCheck, error) {
	p := m.getOIDC()
	if p == nil {
		m.logger.Info("Ended a session whose OIDC provider is no longer configured", "subject", id.Subject)
		return nil, errSessionEnded
	}
	checked, err := p.checkSession(ctx, refreshToken, id)
	if errors.Is(err, errProviderEndedSession) {
		m.logger.Info("OIDC provider ended a session", "subject", id.Subject, "reason", err.Error())
		return nil, errProviderEndedSession
	}
	if err != nil {
		m.logger.Error(err, "Could not check a session at the OIDC provider", "subject", id.Subject)
		return nil, errProviderUnavailable
	}
	return checked, nil
}

// endSession ends the browser's session, also while authentication cannot
// be resolved: it clears the session's cookies, records the session in the
// record of ended sessions, so that copies of its access token and refresh
// cookie stop working at once, and revokes the provider's refresh token of an
// OIDC session at the provider. With providerSignOut Always, an OIDC session
// also ends at the provider when it offers RP-initiated logout: the result
// carries the provider's end-session URL, which sends the browser back to
// postLogoutRedirect. The next SSO sign-in then asks the provider to
// authenticate the user again, in case its session outlives this, as it does
// at a provider without RP-initiated logout. Failures are logged; the browser
// signs out either way.
func (m *authManager) endSession(ctx context.Context, b browserSession, postLogoutRedirect string) *sessionResult {
	// The browser may leave for the provider before this ends.
	ctx = context.WithoutCancel(ctx)
	res := &sessionResult{cookies: clearSessionCookies()}
	a, p := m.get(), m.getOIDC()
	oidcSession := b.idToken != ""
	if a != nil {
		if t, err := a.parseToken(b.refreshToken, refreshTokenType); err == nil {
			oidcSession = oidcSession || t.identity.Provider == providerOIDC
			// Best effort: a copy of the session's tokens then works until
			// they expire, as before the record.
			if err := m.ended.record(ctx, endedSessionKey(t.session.ID), time.Now()); err != nil {
				m.logger.Error(err, "Could not record a signed-out session")
			}
		}
		if p != nil && b.providerToken != "" {
			if t, err := a.openProviderToken(b.providerToken); err == nil {
				if err := p.revoke(ctx, t.RefreshToken); err != nil {
					m.logger.Error(err, "Could not revoke the OIDC provider's refresh token")
				}
			}
		}
	}
	if !oidcSession || p == nil || !p.signOutAtProvider {
		return res
	}
	res.cookies = append(res.cookies, newCookie(oidcReauthCookie, "1", oidcCookiePath, oidcReauthCookieMaxAge))
	endSessionURL, err := p.endSessionURL(ctx, postLogoutRedirect, b.idToken)
	if err != nil {
		m.logger.Error(err, "Could not build the OIDC end-session URL")
	}
	res.endSessionURL = endSessionURL
	return res
}

// newRefreshCookie returns the refresh cookie of tokens, which expires with
// its token.
func newRefreshCookie(tokens *sessionTokens) *http.Cookie {
	return newCookie(refreshCookieName, tokens.refreshToken, refreshCookiePath, maxAgeUntil(tokens.refreshExpiresAt))
}

// newProviderTokenCookie returns the provider token cookie holding sealed,
// which expires with the refresh cookie at expires, or clears it when sealed
// is "".
func newProviderTokenCookie(sealed string, expires time.Time) *http.Cookie {
	if sealed == "" {
		return expiredCookie(providerTokenCookie, refreshCookiePath)
	}
	return newCookie(providerTokenCookie, sealed, refreshCookiePath, maxAgeUntil(expires))
}

// newIDTokenCookie returns the ID token cookie holding idToken until the
// session ends at sessionEnd, or clears it. An ID token too large for a
// cookie is left out; the sign-out at the provider then names only the
// client.
func newIDTokenCookie(idToken string, sessionEnd time.Time) *http.Cookie {
	if idToken == "" || len(idToken) > maxIDTokenCookieLen {
		return expiredCookie(oidcIDTokenCookie, oidcIDTokenCookiePath)
	}
	return newCookie(oidcIDTokenCookie, idToken, oidcIDTokenCookiePath, maxAgeUntil(sessionEnd))
}

// clearSessionCookies returns the cookies that clear a session from the
// browser: its refresh token, its provider token and its ID token.
func clearSessionCookies() []*http.Cookie {
	return []*http.Cookie{
		expiredCookie(refreshCookieName, refreshCookiePath),
		expiredCookie(providerTokenCookie, refreshCookiePath),
		expiredCookie(oidcIDTokenCookie, oidcIDTokenCookiePath),
	}
}

// respondSessionError answers a session operation that refused with err:
// 503 while it cannot decide, so that the UI keeps the session and retries;
// 401 or 403 when it refuses; 500 when minting tokens failed, which is not
// the client's fault.
func respondSessionError(w http.ResponseWriter, err error) {
	for _, outcome := range sessionOutcomes {
		if errors.Is(err, outcome.err) {
			respondError(w, outcome.status, outcome.err.Error())
			return
		}
	}
	respondError(w, http.StatusInternalServerError, "failed to issue session")
}

// sessionOutcomes are the statuses of the outcomes of session operations.
var sessionOutcomes = []struct {
	err    error
	status int
}{
	{errAuthUnavailable, http.StatusServiceUnavailable},
	{errProviderUnavailable, http.StatusServiceUnavailable},
	{errAdminLoginDisabled, http.StatusForbidden},
	{errInvalidCredentials, http.StatusUnauthorized},
	{errSessionInvalid, http.StatusUnauthorized},
	{errSessionEnded, http.StatusUnauthorized},
	{errProviderEndedSession, http.StatusUnauthorized},
}
