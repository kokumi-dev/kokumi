package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
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
	// oidcCookiePath scopes the cookies of the sign-in flow to its endpoints.
	oidcCookiePath = "/api/v1/auth/oidc"
	// oidcStateCookie holds the CSRF state + PKCE verifier for the flow; short-lived, HttpOnly.
	oidcStateCookie = "kokumi.oidc.state"
	// oidcStateTTL bounds how long an in-flight authorization request stays valid.
	oidcStateTTL = 10 * time.Minute
	// oidcIDTokenCookie holds the provider's ID token for the session, sent as
	// id_token_hint when the user signs out at the provider. It is set only
	// with providerSignOut Always.
	oidcIDTokenCookie = "kokumi.oidc.id_token"
	// oidcIDTokenCookiePath scopes the ID token cookie to the sign-out.
	oidcIDTokenCookiePath = "/api/v1/auth/logout"
	// oidcReauthCookie is set by a sign-out at the provider and makes the
	// next sign-in ask the provider to authenticate the user again
	// (max_age=0), in case the provider session outlives the sign-out.
	oidcReauthCookie = "kokumi.oidc.reauth"
	// oidcReauthCookieMaxAge keeps the re-authentication cookie until the
	// next sign-in; 400 days is the longest lifetime browsers allow.
	oidcReauthCookieMaxAge = 400 * 24 * 60 * 60
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
	// ssoErrorTooLarge is the sso_error code for a sign-in whose session does
	// not fit in the browser's cookies.
	ssoErrorTooLarge = "too_large"
)

// oidcProvider signs users in through the OIDC authorization-code flow
// (PKCE) and verifies the issuer's ID tokens. The sessions it signs users in
// to are minted by the session module with the active authenticator, so OIDC
// and admin sessions share one trust root.
type oidcProvider struct {
	issuer        *oidcIssuer
	oauth         oauth2.Config // without Endpoint, which comes from discovery
	usernameClaim string
	groupsClaim   string
	// signOutAtProvider is providerSignOut Always: signing out of Kokumi
	// also signs the user out of the provider.
	signOutAtProvider bool
	// noRefreshToken logs once that the provider issues no refresh tokens.
	noRefreshToken sync.Once
}

// oidcIssuer discovers an issuer on first use and keeps the result. A failure
// is not kept: a use after oidcDiscoveryRetryInterval asks the issuer again,
// so an issuer that was down when the server started is picked up once it can
// be reached.
type oidcIssuer struct {
	url string

	mu         sync.Mutex
	discovered *oidcDiscovery // nil until a discovery succeeds
	err        error          // the last failure, reported until the retry interval has passed
	failedAt   time.Time
}

// discover returns the issuer's discovered metadata, asking the issuer when
// there is none yet. Concurrent callers share one request.
func (i *oidcIssuer) discover(ctx context.Context) (*oidcDiscovery, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.discovered != nil {
		return i.discovered, nil
	}
	if i.err != nil && time.Since(i.failedAt) < oidcDiscoveryRetryInterval {
		return nil, i.err
	}
	// The result is shared with every caller, so one caller going away must
	// not cancel the request.
	discoverCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), oidcDiscoveryTimeout)
	defer cancel()
	discovered, err := discoverIssuer(discoverCtx, i.url)
	if err != nil {
		i.err = fmt.Errorf("oidc discovery for %s: %w", i.url, err)
		i.failedAt = time.Now()
		return nil, i.err
	}
	i.discovered, i.err = discovered, nil
	return discovered, nil
}

// oidcDiscovery is what Kokumi uses of an issuer's discovery document.
type oidcDiscovery struct {
	provider *oidc.Provider
	// endpoint holds the authorization and token endpoints, and the one way
	// the client authenticates to the provider.
	endpoint oauth2.Endpoint
	// endSessionEndpoint is where RP-initiated logout signs the user out of
	// the provider, or "" when it offers none.
	endSessionEndpoint string
	// revocationEndpoint is where RFC 7009 revokes a token, or "" when the
	// provider offers none.
	revocationEndpoint string
}

// discoverIssuer reads the discovery document of the issuer at issuerURL.
func discoverIssuer(ctx context.Context, issuerURL string) (*oidcDiscovery, error) {
	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, err
	}
	var metadata struct {
		EndSessionEndpoint       string   `json:"end_session_endpoint"`
		RevocationEndpoint       string   `json:"revocation_endpoint"`
		TokenEndpointAuthMethods []string `json:"token_endpoint_auth_methods_supported"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, fmt.Errorf("reading the provider metadata: %w", err)
	}
	endpoint := provider.Endpoint()
	endpoint.AuthStyle = clientAuthStyle(metadata.TokenEndpointAuthMethods)
	return &oidcDiscovery{
		provider:           provider,
		endpoint:           endpoint,
		endSessionEndpoint: metadata.EndSessionEndpoint,
		revocationEndpoint: metadata.RevocationEndpoint,
	}, nil
}

// clientAuthStyle is how the client sends its secret to the provider, from
// the methods the provider lists: client_secret_basic, which RFC 6749 section
// 2.3.1 requires every provider to support and OpenID Connect Discovery
// assumes when the list is missing, unless the list names client_secret_post
// and not client_secret_basic.
//
// The style is set, not detected: oauth2's detection sends a failed token
// request again in the other style, which redeems an authorization code or a
// refresh token twice, and returns the second answer.
func clientAuthStyle(methods []string) oauth2.AuthStyle {
	if slices.Contains(methods, "client_secret_post") && !slices.Contains(methods, "client_secret_basic") {
		return oauth2.AuthStyleInParams
	}
	return oauth2.AuthStyleInHeader
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
) (*oidcProvider, error) {
	if cfg == nil {
		return nil, nil
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
		issuer:            &oidcIssuer{url: cfg.IssuerURL},
		oauth:             oauth,
		usernameClaim:     cfg.UsernameClaim,
		groupsClaim:       defaultString(cfg.GroupsClaim, "groups"),
		signOutAtProvider: cfg.ProviderSignOut == v1alpha1.ProviderSignOutAlways,
	}, nil
}

// defaultString returns the value when non-empty, else the fallback.
func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// oauthConfig returns a copy of the OAuth2 config with the discovered endpoint
// and redirectURL, so requests never write to the shared struct.
func (p *oidcProvider) oauthConfig(d *oidcDiscovery, redirectURL string) oauth2.Config {
	oauth := p.oauth
	oauth.Endpoint = d.endpoint
	oauth.RedirectURL = redirectURL
	return oauth
}

// redirectSSOError sends the browser back to the login page with an sso_error
// code, which the page shows as a message.
func redirectSSOError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/?sso_error="+url.QueryEscape(code), http.StatusFound)
}

// redirectURI derives the callback URL from the request, honoring X-Forwarded-Proto.
func redirectURI(r *http.Request) string {
	return externalURL(r, "/api/v1/auth/oidc/callback")
}

// postLogoutRedirectURI is where the provider sends the browser back to after
// signing the user out: the UI's root.
func postLogoutRedirectURI(r *http.Request) string {
	return externalURL(r, "/")
}

// externalURL returns the URL of path as the browser reaches the server,
// derived from the request's host and X-Forwarded-Proto.
func externalURL(r *http.Request, path string) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s%s", scheme, r.Host, path)
}

// endSessionURL returns the URL that signs the user out at the provider and
// sends the browser back to postLogoutRedirect (OIDC RP-Initiated Logout
// 1.0), or "" when the provider advertises no end_session_endpoint. idToken,
// when not empty, is sent as id_token_hint.
func (p *oidcProvider) endSessionURL(ctx context.Context, postLogoutRedirect, idToken string) (string, error) {
	d, err := p.issuer.discover(ctx)
	if err != nil {
		return "", err
	}
	if d.endSessionEndpoint == "" {
		return "", nil
	}
	endpoint, err := url.Parse(d.endSessionEndpoint)
	if err != nil {
		return "", fmt.Errorf("parsing oidc end_session_endpoint: %w", err)
	}
	q := endpoint.Query()
	q.Set("client_id", p.oauth.ClientID)
	q.Set("post_logout_redirect_uri", postLogoutRedirect)
	if idToken != "" {
		q.Set("id_token_hint", idToken)
	}
	endpoint.RawQuery = q.Encode()
	return endpoint.String(), nil
}

// handleOIDCStart generates a CSRF state + PKCE verifier, stores them in a cookie,
// and redirects to the issuer's authorization endpoint. After a sign-out at
// the provider it sends max_age=0, which OIDC Core defines as equivalent to
// prompt=login; unlike prompt=login, a provider that does not support it
// ignores it instead of refusing the request. An issuer that cannot be
// reached sends the browser back to the login page.
func handleOIDCStart(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := m.getOIDC()
		if p == nil {
			respondError(w, http.StatusServiceUnavailable, "oidc is not configured")
			return
		}
		d, err := p.issuer.discover(r.Context())
		if err != nil {
			m.logger.Error(err, "Could not discover OIDC issuer")
			redirectSSOError(w, r, ssoErrorUnavailable)
			return
		}
		state, verifier := rand.Text(), oauth2.GenerateVerifier()
		// Store state + PKCE verifier in a cookie so the callback validates without server state.
		setCookie(w, r, oidcStateCookie, state+"."+verifier, oidcCookiePath, int(oidcStateTTL.Seconds()))
		oauth := p.oauthConfig(d, redirectURI(r))
		opts := []oauth2.AuthCodeOption{
			oauth2.S256ChallengeOption(verifier),
		}
		if _, err := r.Cookie(oidcReauthCookie); err == nil {
			opts = append(opts, oauth2.SetAuthURLParam("max_age", "0"))
		}
		http.Redirect(w, r, oauth.AuthCodeURL(state, opts...), http.StatusFound)
	}
}

// handleOIDCCallback validates the state cookie, exchanges the code, verifies the
// ID token, extracts the username claim, starts a session, and redirects to the UI
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
			// A refused request must not hold up every later sign-in.
			clearCookie(w, r, oidcReauthCookie, oidcCookiePath)
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
		clearCookie(w, r, oidcStateCookie, oidcCookiePath)

		d, err := p.issuer.discover(r.Context())
		if err != nil {
			m.logger.Error(err, "Could not discover OIDC issuer")
			redirectSSOError(w, r, ssoErrorUnavailable)
			return
		}
		oauth := p.oauthConfig(d, redirectURI(r))
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
		idToken, err := d.provider.Verifier(&oidc.Config{ClientID: oauth.ClientID}).Verify(r.Context(), rawID)
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
		id, err := identityFromClaims(claims, p.usernameClaim, p.groupsClaim)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		id.Issuer = idToken.Issuer
		if token.RefreshToken == "" {
			p.noRefreshToken.Do(func() {
				m.logger.Info("OIDC provider issued no refresh token; sessions are not checked at the provider",
					"hint", "if the provider issues refresh tokens only for it, add the scope offline_access to spec.auth.oidc.scopes")
			})
		}
		in := signIn{
			identity:             id,
			providerRefreshToken: token.RefreshToken,
			providerCheckAt:      nextProviderCheck(time.Now(), token, idToken),
		}
		// The provider's session, which a back-channel logout may name.
		in.providerSessionID, _ = claims["sid"].(string)
		// Only a sign-out at the provider sends the ID token back.
		if p.signOutAtProvider {
			in.idToken = rawID
		}
		res, err := m.startSession(in)
		if errors.Is(err, errSessionTooLarge) {
			redirectSSOError(w, r, ssoErrorTooLarge)
			return
		}
		if err != nil {
			respondSessionError(w, err)
			return
		}
		writeCookies(w, r, res.cookies...)
		// The user has signed in at the provider, so a sign-out before this one is done with.
		clearCookie(w, r, oidcReauthCookie, oidcCookiePath)
		// Redirect to the app root with the access token in the fragment (never sent to the server).
		target := "/#access_token=" + url.QueryEscape(res.tokens.accessToken)
		http.Redirect(w, r, target, http.StatusFound)
	}
}

// extractClaim reads a non-empty string claim by name or dotted path (see
// claimAtPath).
func extractClaim(claims map[string]any, name string) (string, error) {
	cur, ok := claimAtPath(claims, name)
	if !ok {
		return "", fmt.Errorf("oidc claim %q not found", name)
	}
	if s, ok := cur.(string); ok && s != "" {
		return s, nil
	}
	return "", fmt.Errorf("oidc claim %q is not a non-empty string", name)
}
