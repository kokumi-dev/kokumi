package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	// defaultAccessTokenTTL is the default access-token lifetime.
	defaultAccessTokenTTL = time.Hour
	// defaultRefreshTokenTTL is the default refresh-token lifetime.
	defaultRefreshTokenTTL = 7 * 24 * time.Hour
	// tokenIssuer is the JWT issuer claim for minted tokens.
	tokenIssuer      = "kokumi"
	accessTokenType  = "access"
	refreshTokenType = "refresh"

	// Secret data keys.
	secretKeyUsername     = "username"
	secretKeyPasswordHash = "password-hash"
	secretKeySigningKey   = "signing-key"

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
	refreshTTL   time.Duration
}

// publicAPIPaths are API paths reachable without a valid access token.
var publicAPIPaths = map[string]struct{}{
	"/api/v1/auth/login":         {},
	"/api/v1/auth/refresh":       {},
	"/api/v1/auth/logout":        {},
	"/api/v1/auth/oidc/start":    {},
	"/api/v1/auth/oidc/callback": {},
	"/api/v1/info":               {},
}

// newAuthenticator builds an authenticator with default token lifetimes.
func newAuthenticator(username string, passwordHash, signingKey []byte) *authenticator {
	return &authenticator{
		username:     username,
		passwordHash: passwordHash,
		signingKey:   signingKey,
		accessTTL:    defaultAccessTokenTTL,
		refreshTTL:   defaultRefreshTokenTTL,
	}
}

// buildAuthenticator reads the credentials Secret to build an authenticator. A
// disabled admin still builds a token-only authenticator (signing key only) so
// OIDC sessions share the trust root; a missing/incomplete Secret fails closed.
func buildAuthenticator(
	ctx context.Context,
	reader client.Reader,
	namespace string,
	cfg *v1alpha1.AdminUserConfig,
	getenv func(string) string,
) (*authenticator, error) {

	if cfg == nil || cfg.SecretRef == nil {
		return nil, fmt.Errorf("admin secretRef not set")
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: namespace, Name: cfg.SecretRef.Name}
	if err := reader.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("reading auth secret %s/%s: %w", namespace, cfg.SecretRef.Name, err)
	}

	passwordHash := secret.Data[secretKeyPasswordHash]
	signingKey := secret.Data[secretKeySigningKey]

	// The signing key is the trust root for all session tokens; required regardless of mode.
	if len(signingKey) == 0 {
		return nil, fmt.Errorf("auth secret %s/%s missing %q", namespace, cfg.SecretRef.Name, secretKeySigningKey)
	}

	// Disabled admin: only the signing key is needed; credential keys are ignored so login is impossible.
	if !cfg.IsEnabled() {
		auth := newAuthenticator("", nil, signingKey)
		if v := strings.TrimSpace(getenv("KOKUMI_TOKEN_TTL")); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				auth.accessTTL = d
			}
		}
		return auth, nil
	}

	// Enabled admin: password hash is mandatory so credentials can be verified.
	if len(passwordHash) == 0 {
		return nil, fmt.Errorf("auth secret %s/%s missing %q", namespace, cfg.SecretRef.Name, secretKeyPasswordHash)
	}

	// Username from config, else Secret key, else "admin".
	username := strings.TrimSpace(cfg.Username)
	if username == "" {
		username = strings.TrimSpace(string(secret.Data[secretKeyUsername]))
	}
	if username == "" {
		username = "admin"
	}

	auth := newAuthenticator(username, passwordHash, signingKey)
	// KOKUMI_TOKEN_TTL overrides the access-token lifetime when valid.
	if v := strings.TrimSpace(getenv("KOKUMI_TOKEN_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			auth.accessTTL = d
		}
	}
	return auth, nil
}

// verifyCredentials checks username/password; bcrypt runs even on username
// mismatch to keep timing constant and avoid enumeration.
func (a *authenticator) verifyCredentials(username, password string) bool {
	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) == 1
	passMatch := bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password)) == nil
	return userMatch && passMatch
}

// issueAccessToken mints a signed access JWT.
func (a *authenticator) issueAccessToken(now time.Time) (string, time.Time, error) {
	return a.issueTypedToken(now, a.accessTTL, accessTokenType, a.username)
}

// issueAccessTokenFor mints an access JWT for an explicit subject (OIDC ID-token username).
func (a *authenticator) issueAccessTokenFor(now time.Time, username string) (string, time.Time, error) {
	expires := now.Add(a.accessTTL)
	claims := jwt.RegisteredClaims{
		Subject:   username,
		Issuer:    tokenIssuer,
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expires),
		ID:        randomTokenID(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, typedClaims{RegisteredClaims: claims, TokenType: accessTokenType})
	signed, err := token.SignedString(a.signingKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing token: %w", err)
	}
	return signed, expires, nil
}

// issueRefreshToken mints a refresh JWT; subject is carried so refresh preserves
// identity (an OIDC login must not flip to the admin username).
func (a *authenticator) issueRefreshToken(now time.Time, subject string) (string, time.Time, error) {
	return a.issueTypedToken(now, a.refreshTTL, refreshTokenType, subject)
}

// issueTypedToken mints a typed JWT; sub defaults to the admin username when empty.
func (a *authenticator) issueTypedToken(now time.Time, ttl time.Duration, typ, sub string) (string, time.Time, error) {
	if sub == "" {
		sub = a.username
	}
	expires := now.Add(ttl)
	claims := jwt.RegisteredClaims{
		Subject:   sub,
		Issuer:    tokenIssuer,
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expires),
		ID:        randomTokenID(),
	}
	// Embed token type so parseTypedToken rejects cross-role use (e.g. refresh as bearer).
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, typedClaims{RegisteredClaims: claims, TokenType: typ})
	signed, err := token.SignedString(a.signingKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing token: %w", err)
	}
	return signed, expires, nil
}

// typedClaims adds a private "typ" claim to RegisteredClaims.
type typedClaims struct {
	jwt.RegisteredClaims
	TokenType string `json:"typ"`
}

// parseToken verifies an access token.
func (a *authenticator) parseToken(tokenString string) (*jwt.RegisteredClaims, error) {
	return a.parseTypedToken(tokenString, accessTokenType)
}

// parseRefresh verifies a refresh token.
func (a *authenticator) parseRefresh(tokenString string) (*jwt.RegisteredClaims, error) {
	return a.parseTypedToken(tokenString, refreshTokenType)
}

// parseTypedToken verifies signature, algorithm, issuer, expiry, and type.
func (a *authenticator) parseTypedToken(tokenString, wantType string) (*jwt.RegisteredClaims, error) {
	claims := &typedClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
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
	if claims.TokenType != wantType {
		return nil, fmt.Errorf("unexpected token type %q, want %q", claims.TokenType, wantType)
	}
	return &claims.RegisteredClaims, nil
}

// isHTTPS reports TLS (direct or via X-Forwarded-Proto). The refresh cookie is
// only Secure then, so plain-HTTP UIs (e.g. Safari on localhost) still allow silent refresh.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// setRefreshCookie writes an HttpOnly, SameSite=Lax refresh cookie; Secure only over HTTPS.
func (a *authenticator) setRefreshCookie(w http.ResponseWriter, r *http.Request, refreshToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    refreshToken,
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(a.refreshTTL.Seconds()),
	})
}

// clearRefreshCookie expires the refresh cookie, forcing re-login.
func (a *authenticator) clearRefreshCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// refreshTokenFromCookie returns the refresh token from the cookie.
func (a *authenticator) refreshTokenFromCookie(r *http.Request) string {
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
		a, disabled := m.getState()
		if a == nil {
			if disabled {
				// Auth intentionally disabled: protected paths are open.
				next.ServeHTTP(w, r)
				return
			}
			// Auth is configured but currently unresolvable (e.g. the
			// credentials Secret is missing). Fail closed rather than
			// silently exposing protected endpoints.
			respondError(w, http.StatusServiceUnavailable, "authentication is not available")
			return
		}
		token := bearerToken(r)
		if token == "" {
			respondError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
			return
		}
		if _, err := a.parseToken(token); err != nil {
			respondError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		next.ServeHTTP(w, r)
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
		a, disabled := m.getState()
		if a == nil {
			if disabled {
				// Intentionally off: no login endpoint at all.
				respondError(w, http.StatusServiceUnavailable, "authentication is disabled")
			} else {
				// Configured but unresolvable (e.g. Secret missing).
				respondError(w, http.StatusServiceUnavailable, "authentication is not available")
			}
			return
		}
		// Token-only (OIDC-only) mode: server healthy but rejects password logins.
		if !m.adminLoginEnabled() {
			respondError(w, http.StatusForbidden, "admin login is disabled")
			return
		}
		provider := newAdminProvider(a)
		session, err := provider.Login(r)
		if err != nil {
			status, msg := mapLoginError(err)
			respondError(w, status, msg)
			return
		}
		writeSession(w, a, r, session)
	}
}

// handleRefresh exchanges a valid refresh cookie for a new access token + rotated cookie.
func handleRefresh(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, disabled := m.getState()
		if a == nil {
			if disabled {
				respondError(w, http.StatusServiceUnavailable, "authentication is disabled")
			} else {
				respondError(w, http.StatusServiceUnavailable, "authentication is not available")
			}
			return
		}
		provider := newAdminProvider(a)
		session, err := provider.Refresh(r)
		if err != nil {
			a.clearRefreshCookie(w, r)
			respondError(w, http.StatusUnauthorized, mapRefreshError(err))
			return
		}
		writeSession(w, a, r, session)
	}
}

// handleLogout expires the refresh cookie.
func handleLogout(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a := m.get(); a != nil {
			a.clearRefreshCookie(w, r)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeSession sets the refresh cookie (when issued) and returns the access token.
func writeSession(w http.ResponseWriter, a *authenticator, r *http.Request, s *Session) {
	if s.SetRefreshCookie {
		a.setRefreshCookie(w, r, s.RefreshToken)
	}
	respondJSON(w, http.StatusOK, loginResponse{Token: s.AccessToken, ExpiresAt: s.AccessExpiresAt})
}

// mapLoginError maps provider errors to HTTP status + message.
func mapLoginError(err error) (int, string) {
	if err == errInvalidCredentials {
		return http.StatusUnauthorized, "invalid username or password"
	}
	return http.StatusBadRequest, err.Error()
}

// mapRefreshError maps provider refresh errors to an HTTP message.
func mapRefreshError(err error) string {
	if err == errMissingRefresh {
		return "missing refresh token"
	}
	return "invalid or expired refresh token"
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

// errInvalidCredentials is returned when username/password do not match.
var errInvalidCredentials = fmt.Errorf("invalid username or password")

// errMissingRefresh is returned when no refresh cookie is present.
var errMissingRefresh = fmt.Errorf("missing refresh token")

// decodeJSONBody decodes a JSON request body into v, returning a 400-style
// error when the body is malformed.
func decodeJSONBody(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("invalid request body")
	}
	return nil
}
