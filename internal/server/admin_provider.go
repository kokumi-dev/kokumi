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
	return p.issue(&Identity{Subject: p.auth.username, Provider: providerAdmin}, time.Now().Add(p.auth.sessionTTL))
}

func (p *adminProvider) Refresh(r *http.Request) (*Session, error) {
	refresh := p.auth.refreshTokenFromCookie(r)
	if refresh == "" {
		return nil, errMissingRefresh
	}
	claims, expires, err := p.auth.parseRefresh(refresh)
	if err != nil {
		return nil, err
	}
	// Re-mint with the refresh token's full identity so an OIDC session
	// stays an OIDC session (provider/email/groups preserved; it must not
	// silently become an admin-identity session), and keep its end.
	return p.issue(claims, expires)
}

// issue mints an access + refresh token pair for the given identity, for a
// session that ends at expires. When the identity carries no provider it
// defaults to the admin identity.
func (p *adminProvider) issue(id *Identity, expires time.Time) (*Session, error) {
	if id.Provider == "" {
		id = &Identity{Subject: id.Subject, Provider: providerAdmin}
	}
	return p.auth.issueSession(time.Now(), id, expires)
}
