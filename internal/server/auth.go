package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	// defaultAccessTokenTTL is the default access-token lifetime. It is short
	// because a sign-out cannot revoke an access token: a copied token works
	// until it expires.
	defaultAccessTokenTTL = 5 * time.Minute
	// defaultSessionTTL is how long a session lasts from sign-in by default.
	// Refreshing never extends it.
	defaultSessionTTL = 24 * time.Hour
	// tokenIssuer is the JWT issuer claim for minted tokens.
	tokenIssuer      = "kokumi"
	accessTokenType  = "access"
	refreshTokenType = "refresh"

	// Secret data keys.
	secretKeyPasswordHash = "password-hash"
	secretKeySigningKey   = "signing-key"

	// defaultAdminUsername is the admin's username when the Kitchen sets none.
	defaultAdminUsername = "admin"

	// minSigningKeyLen is the minimum accepted HMAC signing-key length (bytes).
	minSigningKeyLen = 32

	// signingMethod is the only accepted JWT signing algorithm.
	signingMethod = "HS256"

	bearerPrefix      = "Bearer "
	refreshCookieName = "kokumi.refresh"
	// refreshCookiePath scopes the cookie to the auth endpoints.
	refreshCookiePath = "/api/v1/auth"
)

// authenticator validates admin credentials and issues/verifies HMAC-signed
// JWTs; immutable and concurrency-safe.
type authenticator struct {
	username     string
	passwordHash []byte
	signingKey   []byte
	accessTTL    time.Duration
	sessionTTL   time.Duration
}

// tokenLifetimes overrides the default token lifetimes; a zero field keeps
// the default.
type tokenLifetimes struct {
	// access is the access-token lifetime (KOKUMI_TOKEN_TTL).
	access time.Duration
	// session is how long a session lasts from sign-in (KOKUMI_SESSION_TTL).
	session time.Duration
}

// publicAPIPaths are API paths reachable without a valid access token.
var publicAPIPaths = map[string]struct{}{
	"/api/v1/auth/login":         {},
	"/api/v1/auth/refresh":       {},
	"/api/v1/auth/logout":        {},
	"/api/v1/auth/oidc/start":    {},
	"/api/v1/auth/oidc/callback": {},
	"/api/v1/info":               {},
	"/api/v1/signing":            {},
}

// newAuthenticator builds an authenticator with default token lifetimes.
func newAuthenticator(username string, passwordHash, signingKey []byte) *authenticator {
	return &authenticator{
		username:     username,
		passwordHash: passwordHash,
		signingKey:   signingKey,
		accessTTL:    defaultAccessTokenTTL,
		sessionTTL:   defaultSessionTTL,
	}
}

// buildTokenAuthenticator reads the token signing-key Secret and builds a
// token-only authenticator (no credentials). The signing key is the trust root
// for all session tokens, admin and OIDC alike; a missing/incomplete Secret
// fails closed.
func buildTokenAuthenticator(
	ctx context.Context,
	reader client.Reader,
	namespace string,
	secretName string,
	lifetimes tokenLifetimes,
) (*authenticator, error) {
	if secretName == "" {
		return nil, fmt.Errorf("token signing-key secretRef not set")
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: namespace, Name: secretName}
	if err := reader.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("reading token signing-key secret %s/%s: %w", namespace, secretName, err)
	}
	signingKey := secret.Data[secretKeySigningKey]
	if len(signingKey) < minSigningKeyLen {
		return nil, fmt.Errorf("token signing-key secret %s/%s must contain a %q of at least %d bytes", namespace, secretName, secretKeySigningKey, minSigningKeyLen)
	}
	auth := newAuthenticator("", nil, signingKey)
	if lifetimes.access > 0 {
		auth.accessTTL = lifetimes.access
	}
	if lifetimes.session > 0 {
		auth.sessionTTL = lifetimes.session
	}
	return auth, nil
}

// adminCredentials are the admin account's username and bcrypt password hash.
type adminCredentials struct {
	username     string
	passwordHash []byte
}

// readAdminCredentials reads the admin account of cfg from its Secret. A
// Secret that does not exist or holds no password hash is
// errNoAdminCredentials; any other error means it could not be read.
func readAdminCredentials(
	ctx context.Context,
	reader client.Reader,
	namespace string,
	cfg *v1alpha1.AdminUserConfig,
) (*adminCredentials, error) {
	if cfg.SecretRef == nil {
		return nil, fmt.Errorf("%w: admin secretRef not set", errNoAdminCredentials)
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: namespace, Name: cfg.SecretRef.Name}
	if err := reader.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: auth secret %s/%s not found", errNoAdminCredentials, namespace, cfg.SecretRef.Name)
		}
		return nil, fmt.Errorf("reading auth secret %s/%s: %w", namespace, cfg.SecretRef.Name, err)
	}
	passwordHash := secret.Data[secretKeyPasswordHash]
	if len(passwordHash) == 0 {
		return nil, fmt.Errorf("%w: auth secret %s/%s missing %q", errNoAdminCredentials, namespace, cfg.SecretRef.Name, secretKeyPasswordHash)
	}
	username := strings.TrimSpace(cfg.Username)
	if username == "" {
		username = defaultAdminUsername
	}
	return &adminCredentials{username: username, passwordHash: passwordHash}, nil
}

// withAdmin returns a copy of a that signs admin in, or no one when admin is
// nil. a is never mutated: authenticators are immutable once published.
func (a *authenticator) withAdmin(admin *adminCredentials) *authenticator {
	out := *a
	out.username, out.passwordHash = "", nil
	if admin != nil {
		out.username, out.passwordHash = admin.username, admin.passwordHash
	}
	return &out
}

// verifyCredentials checks username/password; bcrypt runs even on username
// mismatch to keep timing constant and avoid enumeration.
func (a *authenticator) verifyCredentials(username, password string) bool {
	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) == 1
	passMatch := bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password)) == nil
	return userMatch && passMatch
}

// Session is a signed-in user's pair of tokens. The access token is
// short-lived and presented as a Bearer header (or the access_token query
// parameter for SSE); the refresh token travels in an HttpOnly cookie.
type Session struct {
	AccessToken     string
	AccessExpiresAt time.Time
	RefreshToken    string
	// ExpiresAt is when the session ends: the refresh token and its cookie
	// expire then, and refreshing does not move it.
	ExpiresAt time.Time
}

// startSession mints the tokens of a new session for id, which lasts
// sessionTTL from now.
func (a *authenticator) startSession(id *Identity) (*Session, error) {
	now := time.Now()
	return a.issueSession(now, id, now.Add(a.sessionTTL))
}

// issueSession mints the access and refresh tokens of a session for id that
// ends at expires. A refresh passes the end of the session it renews, so a
// session lasts sessionTTL from sign-in however often it is refreshed. The
// access token expires accessTTL from now, or with the session if that is
// sooner. Both tokens carry the whole identity, so a refreshed OIDC session
// stays an OIDC session.
func (a *authenticator) issueSession(now time.Time, id *Identity, expires time.Time) (*Session, error) {
	if id.Subject == "" {
		return nil, errors.New("identity has no subject")
	}
	accessExpires := now.Add(a.accessTTL)
	if expires.Before(accessExpires) {
		accessExpires = expires
	}
	access, err := a.signToken(now, accessTokenType, id, accessExpires)
	if err != nil {
		return nil, fmt.Errorf("issuing access token: %w", err)
	}
	refresh, err := a.signToken(now, refreshTokenType, id, expires)
	if err != nil {
		return nil, fmt.Errorf("issuing refresh token: %w", err)
	}
	return &Session{
		AccessToken:     access,
		AccessExpiresAt: accessExpires,
		RefreshToken:    refresh,
		ExpiresAt:       expires,
	}, nil
}

// signToken mints a session JWT of tokenType for id that expires at expires.
// Email, groups and provider are embedded for ServiceAccount mapping.
func (a *authenticator) signToken(now time.Time, tokenType string, id *Identity, expires time.Time) (string, error) {
	registered := jwt.RegisteredClaims{
		Subject:   id.Subject,
		Issuer:    tokenIssuer,
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expires),
		ID:        randomTokenID(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, newIdentityClaims(registered, tokenType, id))
	signed, err := token.SignedString(a.signingKey)
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}
	return signed, nil
}

// parseToken verifies a session token and returns the identity it carries
// and when it expires. It accepts only an unexpired HS256 token signed with
// the signing key, issued by tokenIssuer, of tokenType, with a subject. The
// auth middleware checks access tokens with it, and a refresh checks refresh
// tokens.
func (a *authenticator) parseToken(tokenString, tokenType string) (*Identity, time.Time, error) {
	claims := &identityClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.signingKey, nil
	},
		jwt.WithValidMethods([]string{signingMethod}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, time.Time{}, err
	}
	if claims.TokenType != tokenType {
		return nil, time.Time{}, fmt.Errorf("unexpected token type %q, want %q", claims.TokenType, tokenType)
	}
	if claims.Subject == "" {
		return nil, time.Time{}, errors.New("token has no subject")
	}
	return claims.identity(), claims.ExpiresAt.Time, nil
}

// isHTTPS reports TLS (direct or via X-Forwarded-Proto). Cookies are only
// Secure then, so plain-HTTP UIs (e.g. Safari on localhost) still allow silent refresh.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// setCookie writes an HttpOnly, SameSite=Lax cookie that lasts maxAge
// seconds; Secure only over HTTPS.
func setCookie(w http.ResponseWriter, r *http.Request, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
}

// clearCookie expires a cookie written by setCookie.
func clearCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	setCookie(w, r, name, "", path, -1)
}

// maxAgeUntil returns the Max-Age, in seconds, of a cookie that lasts until t.
func maxAgeUntil(t time.Time) int {
	return max(1, int(time.Until(t).Seconds()))
}

// setRefreshCookie writes the refresh cookie, which lasts as long as its session.
func setRefreshCookie(w http.ResponseWriter, r *http.Request, refreshToken string, sessionExpires time.Time) {
	setCookie(w, r, refreshCookieName, refreshToken, refreshCookiePath, maxAgeUntil(sessionExpires))
}

// refreshTokenFromCookie returns the refresh token from the cookie, trimmed.
func refreshTokenFromCookie(r *http.Request) string {
	c, err := r.Cookie(refreshCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

// middleware rejects protected API paths without a valid bearer token; the
// authenticator is resolved per request so config changes apply without restart.
func (m *authManager) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		a := m.get()
		if a == nil {
			// No token signing key yet, so no token can be valid: no login
			// method is configured, or its Secrets cannot be read.
			respondError(w, http.StatusServiceUnavailable, "authentication is not available")
			return
		}
		token := bearerToken(r)
		if token == "" {
			respondError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
			return
		}
		id, expires, err := a.parseToken(token, accessTokenType)
		if err != nil {
			respondError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		next.ServeHTTP(w, r.WithContext(withTokenExpiry(withIdentity(r.Context(), id), expires)))
	})
}

// requiresAuth reports whether a request path must carry a valid token.
func requiresAuth(path string) bool {
	if !strings.HasPrefix(path, "/api/v1/") {
		return false
	}
	_, public := publicAPIPaths[path]
	return !public
}

// bearerToken extracts the bearer token from the Authorization header, falling
// back to the access_token query param (needed for SSE, whose EventSource can't set headers).
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > len(bearerPrefix) && strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
		return strings.TrimSpace(h[len(bearerPrefix):])
	}
	if token := strings.TrimSpace(r.URL.Query().Get("access_token")); token != "" {
		return token
	}
	return ""
}

// loginRequest is the POST /api/v1/auth/login body.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse is returned on a successful login.
type loginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// handleLogin validates credentials and issues an access token + refresh cookie.
func handleLogin(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := m.get()
		if a == nil {
			respondError(w, http.StatusServiceUnavailable, "authentication is not available")
			return
		}
		// Without the admin account the server rejects password logins.
		if !m.adminLoginEnabled() {
			respondError(w, http.StatusForbidden, "admin login is disabled")
			return
		}
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if !a.verifyCredentials(req.Username, req.Password) {
			respondError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		session, err := a.startSession(&Identity{Subject: a.username, Provider: providerAdmin})
		if err != nil {
			// Token minting failed (signing key problem); not the client's fault.
			respondError(w, http.StatusInternalServerError, "failed to issue session")
			return
		}
		// An ID token left from an earlier OIDC session must not turn this
		// admin session's sign-out into a sign-out at the provider.
		clearCookie(w, r, oidcIDTokenCookie, oidcIDTokenCookiePath)
		writeSession(w, r, session)
	}
}

// handleRefresh exchanges a valid refresh cookie for a new access token + rotated cookie.
func handleRefresh(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := m.get()
		if a == nil {
			respondError(w, http.StatusServiceUnavailable, "authentication is not available")
			return
		}
		token := refreshTokenFromCookie(r)
		if token == "" {
			clearCookie(w, r, refreshCookieName, refreshCookiePath)
			respondError(w, http.StatusUnauthorized, "missing refresh token")
			return
		}
		id, expires, err := a.parseToken(token, refreshTokenType)
		if err != nil {
			clearCookie(w, r, refreshCookieName, refreshCookiePath)
			respondError(w, http.StatusUnauthorized, "invalid or expired refresh token")
			return
		}
		// The session keeps its identity and its end.
		session, err := a.issueSession(time.Now(), id, expires)
		if err != nil {
			// Token minting failed (signing key problem); not the client's fault.
			respondError(w, http.StatusInternalServerError, "failed to refresh session")
			return
		}
		writeSession(w, r, session)
	}
}

// logoutResponse is returned by a sign-out that continues at the OIDC
// provider: the UI sends the browser to EndSessionURL.
type logoutResponse struct {
	EndSessionURL string `json:"endSessionURL"`
}

// handleLogout ends the session in this browser by expiring its cookies,
// also while authentication cannot be resolved. Tokens are not revoked: a
// copied access token works until it expires. With providerSignOut Always,
// an OIDC session also ends at the provider when it offers RP-initiated
// logout: the response carries the provider's end-session URL. The next SSO
// sign-in then asks the provider to authenticate the user again, in case its
// session outlives this, as it does at a provider without RP-initiated logout.
func handleLogout(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var idToken string
		if c, err := r.Cookie(oidcIDTokenCookie); err == nil {
			idToken = c.Value
		}
		oidcSession := idToken != ""
		if a := m.get(); a != nil && !oidcSession {
			id, _, err := a.parseToken(refreshTokenFromCookie(r), refreshTokenType)
			oidcSession = err == nil && id.Provider == providerOIDC
		}
		clearCookie(w, r, refreshCookieName, refreshCookiePath)
		clearCookie(w, r, oidcIDTokenCookie, oidcIDTokenCookiePath)

		p := m.getOIDC()
		if !oidcSession || p == nil || !p.signOutAtProvider {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		setCookie(w, r, oidcReauthCookie, "1", oidcCookiePath, oidcReauthCookieMaxAge)
		endSessionURL, err := p.endSessionURL(r, idToken)
		if err != nil {
			m.logger.Error(err, "Could not build the OIDC end-session URL")
		}
		if endSessionURL == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		respondJSON(w, http.StatusOK, logoutResponse{EndSessionURL: endSessionURL})
	}
}

// writeSession sets the refresh cookie and returns the access token.
func writeSession(w http.ResponseWriter, r *http.Request, s *Session) {
	setRefreshCookie(w, r, s.RefreshToken, s.ExpiresAt)
	respondJSON(w, http.StatusOK, loginResponse{Token: s.AccessToken, ExpiresAt: s.AccessExpiresAt})
}

// randomTokenID returns a random 128-bit hex string for use as a JWT ID.
func randomTokenID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is effectively impossible; fall back to a time-based value.
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}

// errNoAdminCredentials is returned when the admin Secret does not exist or
// holds no password hash: admin login is off until it has one.
var errNoAdminCredentials = errors.New("no admin credentials")
