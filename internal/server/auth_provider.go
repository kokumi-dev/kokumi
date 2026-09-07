package server

import (
	"net/http"
	"time"
)

// Session is the pair of tokens returned after a successful login or refresh
// by either the admin (username/password) provider or the OIDC provider. The
// access token is short-lived and presented as a Bearer header (or
// access_token query param for SSE); the refresh token is delivered to the
// client as an HttpOnly cookie.
type Session struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	SetRefreshCookie bool
}

// IdentityProvider authenticates users and issues/refreshes Sessions. The
// admin provider and the OIDC provider implement it and can run in parallel
// (or one can be disabled).
type IdentityProvider interface {
	// Login validates the given credentials and returns a fresh Session.
	Login(r *http.Request) (*Session, error)
	// Refresh validates a refresh token and returns a rotated Session.
	Refresh(r *http.Request) (*Session, error)
}
