package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	// backchannelLogoutEvent is the event a logout token carries (OpenID
	// Connect Back-Channel Logout 1.0, section 2.4).
	backchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"
	// maxLogoutRequestBytes bounds the body of a back-channel logout request.
	maxLogoutRequestBytes = 64 << 10
	// logoutTokenParam is the form parameter that carries the logout token.
	logoutTokenParam = "logout_token"
)

// errInvalidLogoutToken is returned for a logout token that fails a check.
var errInvalidLogoutToken = errors.New("invalid logout token")

// logoutToken is what a verified logout token ends: the provider session
// sessionID of the issuer, or, without one, every session of the user subject
// that signed in before issuedAt.
type logoutToken struct {
	issuer    string
	subject   string
	sessionID string
	issuedAt  time.Time
}

// entry returns the key of the record of ended sessions that ends what t
// names: the provider session when t names one, else the user.
func (t *logoutToken) entry() string {
	if t.sessionID != "" {
		return endedProviderSessionKey(t.issuer, t.sessionID)
	}
	return endedProviderUserKey(t.issuer, t.subject)
}

// verifyLogoutToken validates a logout token as Back-Channel Logout 1.0
// section 2.6 asks: its signature, iss, aud, iat and exp as for an ID token,
// with the issuer's keys and algorithms; the backchannel-logout event; a sub
// or a sid; and no nonce, so that an ID token cannot pass as a logout token.
// A token that fails a check is errInvalidLogoutToken.
func (p *oidcProvider) verifyLogoutToken(ctx context.Context, raw string) (*logoutToken, error) {
	d, err := p.issuer.discover(ctx)
	if err != nil {
		return nil, err
	}
	invalid := func(reason string, args ...any) error {
		return fmt.Errorf("%w: %s", errInvalidLogoutToken, fmt.Sprintf(reason, args...))
	}
	token, err := d.provider.Verifier(&oidc.Config{ClientID: p.oauth.ClientID}).Verify(ctx, raw)
	if err != nil {
		return nil, invalid("%v", err)
	}
	var claims map[string]json.RawMessage
	if err := token.Claims(&claims); err != nil {
		return nil, invalid("%v", err)
	}
	if token.IssuedAt.IsZero() {
		return nil, invalid("no iat")
	}
	if _, ok := claims["nonce"]; ok {
		return nil, invalid("a nonce, as in an ID token")
	}
	var events map[string]json.RawMessage
	if err := json.Unmarshal(claims["events"], &events); err != nil {
		return nil, invalid("no events object")
	}
	var event map[string]any
	if err := json.Unmarshal(events[backchannelLogoutEvent], &event); err != nil || event == nil {
		return nil, invalid("no %s event", backchannelLogoutEvent)
	}
	var sid string
	if raw, ok := claims["sid"]; ok {
		if err := json.Unmarshal(raw, &sid); err != nil {
			return nil, invalid("sid is not a string")
		}
	}
	if token.Subject == "" && sid == "" {
		return nil, invalid("neither sub nor sid")
	}
	return &logoutToken{issuer: token.Issuer, subject: token.Subject, sessionID: sid, issuedAt: token.IssuedAt}, nil
}

// handleBackchannelLogout receives the OIDC provider's logout tokens (OpenID
// Connect Back-Channel Logout 1.0): when the provider ends a session, it ends
// the Kokumi sessions signed in with it, at their next request. It answers
// 200 once the record of ended sessions has the entry, 400 for a token that
// fails a check, and 500 when the token cannot be verified or recorded, so
// that the provider may retry. Recording the same session twice changes
// nothing, so no replay cache is needed.
func handleBackchannelLogout(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		refuse := func(status int, code, description string) {
			respondJSON(w, status, map[string]string{"error": code, "error_description": description})
		}
		p := m.getOIDC()
		if p == nil {
			refuse(http.StatusBadRequest, "invalid_request", "OIDC sign-in is not configured")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxLogoutRequestBytes)
		if err := r.ParseForm(); err != nil {
			refuse(http.StatusBadRequest, "invalid_request", "invalid request body")
			return
		}
		raw := r.PostForm.Get(logoutTokenParam)
		if raw == "" {
			refuse(http.StatusBadRequest, "invalid_request", "missing logout_token")
			return
		}
		t, err := p.verifyLogoutToken(r.Context(), raw)
		if errors.Is(err, errInvalidLogoutToken) {
			m.logger.Info("Refused an OIDC logout token", "reason", err.Error())
			refuse(http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if err != nil {
			m.logger.Error(err, "Could not verify an OIDC logout token")
			refuse(http.StatusInternalServerError, "server_error", "the logout token cannot be verified now")
			return
		}
		if err := m.ended.record(r.Context(), t.entry(), t.issuedAt); err != nil {
			m.logger.Error(err, "Could not record a session the OIDC provider ended")
			refuse(http.StatusInternalServerError, "server_error", "the logout cannot be recorded now")
			return
		}
		m.logger.Info("OIDC provider ended a session", "subject", t.subject, "providerSession", t.sessionID != "")
		w.WriteHeader(http.StatusOK)
	}
}
