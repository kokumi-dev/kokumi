package server

import (
	"context"
	"fmt"
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
}

// identityContextKey is the context key for the resolved Identity.
type identityContextKey struct{}

// withIdentity stores the identity in the request context.
func withIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, id)
}

// identityFromRequest returns the authenticated identity, or nil when the
// request is unauthenticated (auth disabled).
func identityFromRequest(r *http.Request) *Identity {
	id, _ := r.Context().Value(identityContextKey{}).(*Identity)
	return id
}

// parseIdentityToken verifies a session access token and returns the identity.
func parseIdentityToken(a *authenticator, token string) (*Identity, error) {
	claims := &identityClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.signingKey, nil
	},
		jwt.WithValidMethods([]string{signingMethod}),
		jwt.WithIssuer(tokenIssuer),
	)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != accessTokenType {
		return nil, fmt.Errorf("unexpected token type %q", claims.TokenType)
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("token missing subject")
	}
	return &Identity{
		Subject:  claims.Subject,
		Email:    claims.Email,
		Groups:   claims.Groups,
		Provider: claims.Provider,
	}, nil
}

// claimAtPath resolves a claim by name, supporting dotted paths for nested
// claims (e.g. "realm_access.roles"). Unlike extractClaim it returns the raw
// value so list-typed claims can be read.
func claimAtPath(claims map[string]any, name string) (any, bool) {
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
