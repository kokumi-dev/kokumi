package server

import (
	"net/http"
	"time"
)

var _ IdentityProvider = (*adminProvider)(nil)

// adminProvider issues Sessions from the bcrypt-backed authenticator.
// It is the default provider and remains enabled alongside any future OIDC
// provider unless explicitly disabled by configuration.
type adminProvider struct {
	auth *authenticator
}

func newAdminProvider(auth *authenticator) *adminProvider {
	return &adminProvider{auth: auth}
}

func (p *adminProvider) Login(r *http.Request) (*Session, error) {
	var req loginRequest
	if err := decodeJSONBody(r, &req); err != nil {
		return nil, err
	}
	if !p.auth.verifyCredentials(req.Username, req.Password) {
		return nil, errInvalidCredentials
	}
	return p.issue(), nil
}

func (p *adminProvider) Refresh(r *http.Request) (*Session, error) {
	refresh := p.auth.refreshTokenFromCookie(r)
	if refresh == "" {
		return nil, errMissingRefresh
	}
	if _, err := p.auth.parseRefresh(refresh); err != nil {
		return nil, err
	}
	return p.issue(), nil
}

// issue mints a fresh access + refresh token pair for the current time.
func (p *adminProvider) issue() *Session {
	now := time.Now()
	access, expires, _ := p.auth.issueAccessToken(now)
	refresh, _, _ := p.auth.issueRefreshToken(now)
	return &Session{
		AccessToken:      access,
		AccessExpiresAt:  expires,
		RefreshToken:     refresh,
		SetRefreshCookie: true,
	}
}
