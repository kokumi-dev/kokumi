package server

import (
	"net/http"
	"time"
)

var _ IdentityProvider = (*adminProvider)(nil)

// adminProvider issues Sessions from the bcrypt-backed authenticator (default provider).
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
	return p.issue(p.auth.username), nil
}

func (p *adminProvider) Refresh(r *http.Request) (*Session, error) {
	refresh := p.auth.refreshTokenFromCookie(r)
	if refresh == "" {
		return nil, errMissingRefresh
	}
	claims, err := p.auth.parseRefresh(refresh)
	if err != nil {
		return nil, err
	}
	// Re-mint with the refresh token's subject so identity is preserved (an OIDC login stays OIDC).
	return p.issue(claims.Subject), nil
}

// issue mints an access + refresh token pair; sub overrides the admin username when set.
func (p *adminProvider) issue(sub string) *Session {
	now := time.Now()
	access, expires, _ := p.auth.issueAccessTokenFor(now, sub)
	refresh, _, _ := p.auth.issueRefreshToken(now, sub)
	return &Session{
		AccessToken:      access,
		AccessExpiresAt:  expires,
		RefreshToken:     refresh,
		SetRefreshCookie: true,
	}
}
