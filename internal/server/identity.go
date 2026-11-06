package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Identity is the authenticated user resolved from a session token. It is
// carried in the request context and used to map the user to ServiceAccounts.
type Identity struct {
	// Subject is the stable identity string: the OIDC "sub" claim for OIDC
	// logins, or the configured admin username for admin logins.
	Subject string
	// Email is the OIDC "email" claim when present.
	Email string
	// Groups are the OIDC "groups" claim values when present.
	Groups []string
	// Provider is "admin" or "oidc".
	Provider string
	// Issuer is the verified OIDC issuer URL; empty for admin logins.
	Issuer string
	// Username is the display name from the configured username claim.
	Username string
}

// isPersonalLogin reports whether the identity is a verified personal OIDC
// login. The shared admin account is not personal, so it cannot be recorded as
// an approver.
func (id *Identity) isPersonalLogin() bool {
	return id.Provider == providerOIDC && id.Issuer != ""
}

// identityClaims extends the session JWT with identity fields used for
// ServiceAccount mapping. RegisteredClaims.Subject carries the OIDC sub (or
// admin username); email/groups/provider are private claims.
type identityClaims struct {
	jwt.RegisteredClaims
	TokenType string   `json:"typ,omitempty"`
	Email     string   `json:"email,omitempty"`
	Groups    []string `json:"groups,omitempty"`
	Provider  string   `json:"provider,omitempty"`
	IDPIssuer string   `json:"idp_iss,omitempty"`
	Username  string   `json:"name,omitempty"`
	// SessionID, AuthTime, IDPCheck and ProviderSessionID carry the
	// sessionState.
	SessionID         string           `json:"sid,omitempty"`
	AuthTime          *jwt.NumericDate `json:"auth_time,omitempty"`
	IDPCheck          *jwt.NumericDate `json:"idp_check,omitempty"`
	ProviderSessionID string           `json:"idp_sid,omitempty"`
}

// identityContextKey is the context key for the resolved Identity.
type identityContextKey struct{}

// withIdentity stores the identity in the request context.
func withIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, id)
}

// identityFromRequest returns the authenticated identity, or nil when the
// request carries none, as on a public path.
func identityFromRequest(r *http.Request) *Identity {
	id, _ := r.Context().Value(identityContextKey{}).(*Identity)
	return id
}

// sessionTokenContextKey is the context key for the request's verified
// access token.
type sessionTokenContextKey struct{}

// withSessionToken stores the request's verified access token, with its
// identity, in the request context.
func withSessionToken(ctx context.Context, t *sessionToken) context.Context {
	return withIdentity(context.WithValue(ctx, sessionTokenContextKey{}, t), t.identity)
}

// sessionTokenFrom returns the request's verified access token, or nil when
// the request carries none, as on a public path.
func sessionTokenFrom(ctx context.Context) *sessionToken {
	t, _ := ctx.Value(sessionTokenContextKey{}).(*sessionToken)
	return t
}

// identity returns the Identity carried by the session claims.
func (c *identityClaims) identity() *Identity {
	return &Identity{
		Subject:  c.Subject,
		Email:    c.Email,
		Groups:   c.Groups,
		Provider: c.Provider,
		Issuer:   c.IDPIssuer,
		Username: c.Username,
	}
}

// session returns the sessionState carried by the session claims.
func (c *identityClaims) session() sessionState {
	s := sessionState{ID: c.SessionID, ProviderSessionID: c.ProviderSessionID}
	if c.AuthTime != nil {
		s.AuthTime = c.AuthTime.Time
	}
	if c.IDPCheck != nil {
		s.IDPCheck = c.IDPCheck.Time
	}
	return s
}

// newIdentityClaims builds the claims of a token for id in session s.
func newIdentityClaims(registered jwt.RegisteredClaims, tokenType string, id *Identity, s sessionState) identityClaims {
	claims := identityClaims{
		RegisteredClaims:  registered,
		TokenType:         tokenType,
		Email:             id.Email,
		Groups:            id.Groups,
		Provider:          id.Provider,
		IDPIssuer:         id.Issuer,
		Username:          id.Username,
		SessionID:         s.ID,
		AuthTime:          jwt.NewNumericDate(s.AuthTime),
		ProviderSessionID: s.ProviderSessionID,
	}
	if !s.IDPCheck.IsZero() {
		claims.IDPCheck = jwt.NewNumericDate(s.IDPCheck)
	}
	return claims
}

// claimAtPath resolves a claim by name. A top-level claim of exactly that name
// wins, so names with dots such as "https://example.com/groups" work;
// otherwise the name is a dotted path into nested claims (e.g.
// "realm_access.roles"). Unlike extractClaim it returns the raw value so
// list-typed claims can be read.
func claimAtPath(claims map[string]any, name string) (any, bool) {
	if v, ok := claims[name]; ok {
		return v, true
	}
	var cur any = claims
	for seg := range strings.SplitSeq(name, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// identityFromClaims builds an OIDC Identity from verified ID-token claims.
// The subject is the OIDC "sub" claim (required); email and groups are
// optional. usernameClaim selects the display/username claim (default email)
// used as fallback subject when sub is absent. emailClaim and groupsClaim
// select the claims used for ServiceAccount identity mapping; both support
// dotted paths for nested claims (e.g. "realm_access.roles").
func identityFromClaims(claims map[string]any, usernameClaim, emailClaim, groupsClaim string) (*Identity, error) {
	sub, err := extractClaim(claims, "sub")
	if err != nil {
		// Some providers lack sub; fall back to the configured username claim.
		sub, err = extractClaim(claims, usernameClaim)
		if err != nil {
			return nil, err
		}
	}
	id := &Identity{Subject: sub, Provider: providerOIDC}
	if username, err := extractClaim(claims, defaultString(usernameClaim, "email")); err == nil {
		id.Username = username
	}
	if email, err := extractClaim(claims, emailClaim); err == nil {
		id.Email = email
	}
	if raw, ok := claimAtPath(claims, groupsClaim); ok {
		switch v := raw.(type) {
		case []any:
			for _, g := range v {
				if s, ok := g.(string); ok && s != "" {
					id.Groups = append(id.Groups, s)
				}
			}
		case []string:
			id.Groups = append(id.Groups, v...)
		case string:
			if v != "" {
				id.Groups = append(id.Groups, v)
			}
		}
	}
	return id, nil
}
