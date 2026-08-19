package server

import (
	"net/http"
	"time"
)

// Session is the pair of tokens returned by an IdentityProvider after a
// successful login or refresh. The access token is short-lived and presented
// as a Bearer header (or access_token query param for SSE); the refresh token
// is delivered to the client as an HttpOnly cookie.
//
// Both the admin (username/password) login and a future OIDC provider produce
// the same Session shape, so the rest of the server and the UI are agnostic to
// which identity backend issued the tokens.
type Session struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	SetRefreshCookie bool
}

// IdentityProvider authenticates users and issues/refreshes Sessions. The
// admin provider is implemented today; an OIDC provider can be added later
// behind the same interface and run in parallel with (or instead of) admin
// login.
type IdentityProvider interface {
	// Login validates the given credentials and returns a fresh Session.
	Login(r *http.Request) (*Session, error)
	// Refresh validates a refresh token and returns a rotated Session.
	Refresh(r *http.Request) (*Session, error)
}
