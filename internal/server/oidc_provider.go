package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-logr/logr"
	"github.com/kokumi-dev/kokumi/api/v1alpha1"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// oidcStateCookie holds the CSRF state + PKCE verifier for the flow; short-lived, HttpOnly.
	oidcStateCookie = "kokumi.oidc.state"
	// oidcStateTTL bounds how long an in-flight authorization request stays valid.
	oidcStateTTL = 10 * time.Minute
)

// oidcProvider implements IdentityProvider via the OIDC authorization-code flow
// (PKCE). It verifies the issuer ID token and mints kokumi session tokens using
// the shared authenticator's signing key, so OIDC and admin sessions share one trust root.
type oidcProvider struct {
	verifier      *oidc.IDTokenVerifier
	oauth         oauth2.Config
	usernameClaim string
	auth          *authenticator
}

var _ IdentityProvider = (*oidcProvider)(nil)

// buildOIDCProvider reads the client secret and performs OIDC discovery. Returns
// (nil, nil) when unconfigured; an error on incomplete config or discovery failure
// (logged by the caller so a transient issuer outage doesn't disable admin login).
func buildOIDCProvider(
	ctx context.Context,
	reader client.Reader,
	namespace string,
	cfg *v1alpha1.OIDCConfig,
	auth *authenticator,
) (*oidcProvider, error) {
	if cfg == nil {
		return nil, nil
	}
	if auth == nil {
		return nil, fmt.Errorf("oidc requires a shared authenticator (signing key) to be configured")
	}
	if cfg.ClientSecretRef == nil {
		return nil, fmt.Errorf("oidc clientSecretRef not set")
	}

	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: namespace, Name: cfg.ClientSecretRef.Name}
	if err := reader.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("reading oidc secret %s/%s: %w", namespace, cfg.ClientSecretRef.Name, err)
	}
	clientSecret := strings.TrimSpace(string(secret.Data["client-secret"]))
	if clientSecret == "" {
		return nil, fmt.Errorf("oidc secret %s/%s missing %q", namespace, cfg.ClientSecretRef.Name, "client-secret")
	}

	// Discovery must succeed; a short timeout keeps startup responsive if the issuer is down.
	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(discoverCtx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", cfg.IssuerURL, err)
	}

	oauth := oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: clientSecret,
		Endpoint:     provider.Endpoint(),
		Scopes:       cfg.Scopes,
	}

	return &oidcProvider{
		verifier:      provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth:         oauth,
		usernameClaim: cfg.UsernameClaim,
		auth:          auth,
	}, nil
}

// redirectURI derives the callback URL from the request, honoring X-Forwarded-Proto.
func redirectURI(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/api/v1/auth/oidc/callback", scheme, r.Host)
}

// Login is unused for OIDC (flow is Start + Callback); satisfies the interface.
func (p *oidcProvider) Login(_ *http.Request) (*Session, error) {
	return nil, fmt.Errorf("oidc login is handled via the start/callback flow")
}

// Refresh is unused for OIDC (uses the shared refresh cookie); satisfies the interface.
func (p *oidcProvider) Refresh(_ *http.Request) (*Session, error) {
	return nil, fmt.Errorf("oidc refresh uses the shared refresh cookie")
}

// issueSession mints a kokumi access + refresh token pair; minting errors are returned.
func (p *oidcProvider) issueSession(username string) (*Session, error) {
	now := time.Now()
	access, expires, err := p.auth.issueAccessTokenFor(now, username)
	if err != nil {
		return nil, fmt.Errorf("issuing access token: %w", err)
	}
	refresh, _, err := p.auth.issueRefreshToken(now, username)
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

// handleOIDCStart generates a CSRF state + PKCE verifier, stores them in a cookie,
// and redirects to the issuer's authorization endpoint.
func handleOIDCStart(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := m.getOIDC()
		if p == nil {
			respondError(w, http.StatusServiceUnavailable, "oidc is not configured")
			return
		}
		state, err := randomToken(32)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to generate state")
			return
		}
		verifier, err := randomToken(64)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to generate pkce verifier")
			return
		}
		// Store state + PKCE verifier in a cookie so the callback validates without server state.
		http.SetCookie(w, &http.Cookie{
			Name:     oidcStateCookie,
			Value:    state + "." + verifier,
			Path:     "/api/v1/auth/oidc",
			HttpOnly: true,
			Secure:   isHTTPS(r),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(oidcStateTTL.Seconds()),
		})
		// Copy the oauth config and set the per-request redirect URL to avoid a data race on the shared struct.
		oauth := p.oauth
		oauth.RedirectURL = redirectURI(r)
		opts := []oauth2.AuthCodeOption{
			oauth2.S256ChallengeOption(verifier),
		}
		http.Redirect(w, r, oauth.AuthCodeURL(state, opts...), http.StatusFound)
	}
}

// handleOIDCCallback validates the state cookie, exchanges the code, verifies the
// ID token, extracts the username claim, mints a session, and redirects to the UI
// with the access token in the URL fragment (captured by the SPA, never sent to the server).
func handleOIDCCallback(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := m.getOIDC()
		if p == nil {
			respondError(w, http.StatusServiceUnavailable, "oidc is not configured")
			return
		}
		q := r.URL.Query()
		if errCode := q.Get("error"); errCode != "" {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("oidc error: %s", errCode))
			return
		}
		state := q.Get("state")
		code := q.Get("code")
		if state == "" || code == "" {
			respondError(w, http.StatusBadRequest, "missing state or code")
			return
		}
		c, err := r.Cookie(oidcStateCookie)
		if err != nil {
			respondError(w, http.StatusBadRequest, "missing oidc state cookie")
			return
		}
		parts := strings.SplitN(c.Value, ".", 2)
		if len(parts) != 2 || parts[0] != state {
			respondError(w, http.StatusBadRequest, "oidc state mismatch")
			return
		}
		// Consume the state cookie immediately to prevent reuse.
		http.SetCookie(w, &http.Cookie{
			Name:     oidcStateCookie,
			Value:    "",
			Path:     "/api/v1/auth/oidc",
			HttpOnly: true,
			Secure:   isHTTPS(r),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})

		// Copy the oauth config and set the per-request redirect URL to avoid a data race on the shared struct.
		oauth := p.oauth
		oauth.RedirectURL = redirectURI(r)
		token, err := oauth.Exchange(r.Context(), code, oauth2.VerifierOption(parts[1]))
		if err != nil {
			// Log the underlying error but do not echo it to the client.
			log := logr.FromContextOrDiscard(r.Context())
			log.Error(err, "OIDC token exchange failed")
			respondError(w, http.StatusBadRequest, "oidc token exchange failed")
			return
		}
		rawID, ok := token.Extra("id_token").(string)
		if !ok {
			respondError(w, http.StatusBadRequest, "oidc response missing id_token")
			return
		}
		idToken, err := p.verifier.Verify(r.Context(), rawID)
		if err != nil {
			log := logr.FromContextOrDiscard(r.Context())
			log.Error(err, "OIDC id token invalid")
			respondError(w, http.StatusBadRequest, "oidc id token invalid")
			return
		}
		var claims map[string]any
		if err := idToken.Claims(&claims); err != nil {
			respondError(w, http.StatusBadRequest, "failed to parse id token claims")
			return
		}
		username, err := extractClaim(claims, p.usernameClaim)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		session, err := p.issueSession(username)
		if err != nil {
			log := logr.FromContextOrDiscard(r.Context())
			log.Error(err, "Failed to issue OIDC session")
			respondError(w, http.StatusInternalServerError, "failed to issue session")
			return
		}
		// Set the refresh cookie now; the access token is delivered via the fragment for the SPA to hold in memory.
		p.auth.setRefreshCookie(w, r, session.RefreshToken)
		// Redirect to the app root with the access token in the fragment (never sent to the server).
		target := "/#access_token=" + url.QueryEscape(session.AccessToken)
		http.Redirect(w, r, target, http.StatusFound)
	}
}

// extractClaim reads a claim by name, supporting nested dotted paths (e.g. "email", "realm_access.roles").
func extractClaim(claims map[string]any, name string) (string, error) {
	segments := strings.Split(name, ".")
	var cur any = claims
	for _, seg := range segments {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("oidc claim %q not found", name)
		}
		cur, ok = m[seg]
		if !ok {
			return "", fmt.Errorf("oidc claim %q not found", name)
		}
	}
	if s, ok := cur.(string); ok && s != "" {
		return s, nil
	}
	return "", fmt.Errorf("oidc claim %q is not a non-empty string", name)
}

// randomToken returns a hex-encoded URL-safe random token of n bytes (OIDC state/PKCE verifier).
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
