package server

import (
	"fmt"
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
	return p.issue(&Identity{Subject: p.auth.username, Provider: providerAdmin})
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
	// Re-mint with the refresh token's full identity so an OIDC session
	// stays an OIDC session (provider/email/groups preserved; it must not
	// silently become an admin-identity session).
	return p.issue(&Identity{
		Subject:  claims.Subject,
		Email:    claims.Email,
		Groups:   claims.Groups,
		Provider: claims.Provider,
	})
}

// issue mints an access + refresh token pair for the given identity. When the
// identity carries no provider it defaults to the admin identity.
func (p *adminProvider) issue(id *Identity) (*Session, error) {
	if id.Provider == "" {
		id = &Identity{Subject: id.Subject, Provider: providerAdmin}
	}
	now := time.Now()
	access, expires, err := p.auth.issueAccessTokenFor(now, id)
	if err != nil {
		return nil, fmt.Errorf("issuing access token: %w", err)
	}
	refresh, err := p.auth.issueRefreshToken(now, id)
	if err != nil {
		return nil, fmt.Errorf("issuing refresh token: %w", err)
	}
	return &Session{
		AccessToken:      access,
		AccessExpiresAt:  expires,
		RefreshToken:     refresh,
		SetRefreshCookie: true,
	}, nil
}
