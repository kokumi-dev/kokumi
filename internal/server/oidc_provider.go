package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
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
	// secretKeyClientSecret is the key within the OIDC Secret that holds the client secret.
	secretKeyClientSecret = "client-secret"
	// oidcDiscoveryTimeout bounds one discovery request to the issuer.
	oidcDiscoveryTimeout = 15 * time.Second
	// oidcDiscoveryRetryInterval is how long a failed discovery is reported
	// without asking the issuer again, so sign-ins during an issuer outage do
	// not each wait for it.
	oidcDiscoveryRetryInterval = 5 * time.Second
	// ssoErrorUnavailable is the sso_error code for an issuer that cannot be
	// reached; the login page turns it into a message.
	ssoErrorUnavailable = "unavailable"
)

// oidcProvider implements IdentityProvider via the OIDC authorization-code flow
// (PKCE). It verifies the issuer ID token and mints kokumi session tokens using
// the shared authenticator's signing key, so OIDC and admin sessions share one trust root.
type oidcProvider struct {
	issuer        *oidcIssuer
	oauth         oauth2.Config // without Endpoint, which comes from discovery
	usernameClaim string
	emailClaim    string
	groupsClaim   string
	auth          *authenticator
}

var _ IdentityProvider = (*oidcProvider)(nil)

// oidcIssuer discovers an issuer on first use and keeps the result. A failure
// is not kept: a use after oidcDiscoveryRetryInterval asks the issuer again,
// so an issuer that was down when the server started is picked up once it can
// be reached.
type oidcIssuer struct {
	url string

	mu       sync.Mutex
	provider *oidc.Provider // nil until a discovery succeeds
	err      error          // the last failure, reported until the retry interval has passed
	failedAt time.Time
}

// discover returns the issuer's discovered metadata, asking the issuer when
// there is none yet. Concurrent callers share one request.
func (i *oidcIssuer) discover(ctx context.Context) (*oidc.Provider, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.provider != nil {
		return i.provider, nil
	}
	if i.err != nil && time.Since(i.failedAt) < oidcDiscoveryRetryInterval {
		return nil, i.err
	}
	// The result is shared with every caller, so one caller going away must
	// not cancel the request.
	discoverCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), oidcDiscoveryTimeout)
	defer cancel()
	provider, err := oidc.NewProvider(discoverCtx, i.url)
	if err != nil {
		i.err = fmt.Errorf("oidc discovery for %s: %w", i.url, err)
		i.failedAt = time.Now()
		return nil, i.err
	}
	i.provider, i.err = provider, nil
	return provider, nil
}

// buildOIDCProvider reads the client secret. It does not contact the issuer;
// discovery happens on first use. Returns (nil, nil) when unconfigured and an
// error on incomplete config (logged by the caller so a broken OIDC config
// doesn't disable admin login).
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
		return nil, fmt.Errorf("oidc requires a shared authenticator (token signing key) to be configured")
	}
	if cfg.ClientSecretRef == nil {
		return nil, fmt.Errorf("oidc clientSecretRef not set")
	}

	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: namespace, Name: cfg.ClientSecretRef.Name}
	if err := reader.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("reading oidc secret %s/%s: %w", namespace, cfg.ClientSecretRef.Name, err)
	}
	clientSecret := strings.TrimSpace(string(secret.Data[secretKeyClientSecret]))
	if clientSecret == "" {
		return nil, fmt.Errorf("oidc secret %s/%s missing %q", namespace, cfg.ClientSecretRef.Name, secretKeyClientSecret)
	}

	oauth := oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: clientSecret,
		Scopes:       cfg.Scopes,
	}

	return &oidcProvider{
		issuer:        &oidcIssuer{url: cfg.IssuerURL},
		oauth:         oauth,
		usernameClaim: cfg.UsernameClaim,
		emailClaim:    defaultString(cfg.EmailClaim, "email"),
		groupsClaim:   defaultString(cfg.GroupsClaim, "groups"),
		auth:          auth,
	}, nil
}

// defaultString returns the value when non-empty, else the fallback.
func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// oauthConfig returns a copy of the OAuth2 config with the discovered endpoints
// and the redirect URI of r, so requests never write to the shared struct.
func (p *oidcProvider) oauthConfig(r *http.Request, provider *oidc.Provider) oauth2.Config {
	oauth := p.oauth
	oauth.Endpoint = provider.Endpoint()
	oauth.RedirectURL = redirectURI(r)
	return oauth
}

// redirectSSOError sends the browser back to the login page with an sso_error
// code, which the page shows as a message.
func redirectSSOError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/?sso_error="+url.QueryEscape(code), http.StatusFound)
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

// issueSession mints a kokumi access + refresh token pair carrying the OIDC
// identity claims (sub, email, groups) used for ServiceAccount mapping;
// minting errors are returned.
func (p *oidcProvider) issueSession(id *Identity) (*Session, error) {
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

// handleOIDCStart generates a CSRF state + PKCE verifier, stores them in a cookie,
// and redirects to the issuer's authorization endpoint. An issuer that cannot be
// reached sends the browser back to the login page.
func handleOIDCStart(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := m.getOIDC()
		if p == nil {
			respondError(w, http.StatusServiceUnavailable, "oidc is not configured")
			return
		}
		provider, err := p.issuer.discover(r.Context())
		if err != nil {
			m.logger.Error(err, "Could not discover OIDC issuer")
			redirectSSOError(w, r, ssoErrorUnavailable)
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
		oauth := p.oauthConfig(r, provider)
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

		provider, err := p.issuer.discover(r.Context())
		if err != nil {
			m.logger.Error(err, "Could not discover OIDC issuer")
			redirectSSOError(w, r, ssoErrorUnavailable)
			return
		}
		oauth := p.oauthConfig(r, provider)
		token, err := oauth.Exchange(r.Context(), code, oauth2.VerifierOption(parts[1]))
		if err != nil {
			// Log the underlying error but do not echo it to the client.
			m.logger.Error(err, "OIDC token exchange failed")
			respondError(w, http.StatusBadRequest, "oidc token exchange failed")
			return
		}
		rawID, ok := token.Extra("id_token").(string)
		if !ok {
			respondError(w, http.StatusBadRequest, "oidc response missing id_token")
			return
		}
		idToken, err := provider.Verifier(&oidc.Config{ClientID: oauth.ClientID}).Verify(r.Context(), rawID)
		if err != nil {
			m.logger.Error(err, "OIDC id token invalid")
			respondError(w, http.StatusBadRequest, "oidc id token invalid")
			return
		}
		var claims map[string]any
		if err := idToken.Claims(&claims); err != nil {
			respondError(w, http.StatusBadRequest, "failed to parse id token claims")
			return
		}
		id, err := identityFromClaims(claims, p.usernameClaim, p.emailClaim, p.groupsClaim)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		id.Issuer = idToken.Issuer
		session, err := p.issueSession(id)
		if err != nil {
			m.logger.Error(err, "Failed to issue OIDC session")
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
