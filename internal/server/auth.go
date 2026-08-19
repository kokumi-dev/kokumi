package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// defaultAuthSecretName is the name of the Secret holding admin credentials
	// when AUTH_SECRET_NAME is not set.
	defaultAuthSecretName = "kokumi-server-auth"
	// defaultNamespace is used when the running namespace cannot be determined.
	defaultNamespace = "kokumi"
	// defaultAccessTokenTTL is the lifetime of an issued access token.
	defaultAccessTokenTTL = time.Hour
	// defaultRefreshTokenTTL is the lifetime of a refresh token cookie.
	defaultRefreshTokenTTL = 7 * 24 * time.Hour
	// tokenIssuer is the JWT "iss" claim value for tokens minted by this server.
	tokenIssuer      = "kokumi"
	accessTokenType  = "access"
	refreshTokenType = "refresh"

	// Secret data keys.
	secretKeyUsername     = "username"
	secretKeyPasswordHash = "password-hash"
	secretKeySigningKey   = "signing-key"

	// signingMethod is the only JWT signing algorithm accepted by this server.
	signingMethod = "HS256"

	bearerPrefix      = "Bearer "
	refreshCookieName = "kokumi.refresh"
	// refreshCookiePath scopes the cookie to the auth endpoints only.
	refreshCookiePath = "/api/v1/auth"
)

// authenticator validates username/password credentials against a bcrypt hash
// and issues/verifies short-lived HMAC-signed JWTs. It is immutable after
// construction and safe for concurrent use.
type authenticator struct {
	username     string
	passwordHash []byte
	signingKey   []byte
	accessTTL    time.Duration
	refreshTTL   time.Duration
}

// publicAPIPaths are API paths reachable without a valid access token.
var publicAPIPaths = map[string]struct{}{
	"/api/v1/auth/login":   {},
	"/api/v1/auth/refresh": {},
	"/api/v1/auth/logout":  {},
	"/api/v1/info":         {},
}

// loadAuthenticator reads the credentials Secret and builds an authenticator.
// It returns an error when the Secret is absent or missing required keys, in
// which case the caller should treat authentication as disabled.
func loadAuthenticator(ctx context.Context, reader client.Reader, namespace, name string) (*authenticator, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: namespace, Name: name}
	if err := reader.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("reading auth secret %s/%s: %w", namespace, name, err)
	}

	username := strings.TrimSpace(string(secret.Data[secretKeyUsername]))
	passwordHash := secret.Data[secretKeyPasswordHash]
	signingKey := secret.Data[secretKeySigningKey]

	if username == "" {
		return nil, fmt.Errorf("auth secret %s/%s missing %q", namespace, name, secretKeyUsername)
	}
	if len(passwordHash) == 0 {
		return nil, fmt.Errorf("auth secret %s/%s missing %q", namespace, name, secretKeyPasswordHash)
	}
	if len(signingKey) == 0 {
		return nil, fmt.Errorf("auth secret %s/%s missing %q", namespace, name, secretKeySigningKey)
	}

	return newAuthenticator(username, passwordHash, signingKey), nil
}

// newAuthenticator builds an authenticator with the default token lifetimes
// and a Secure cookie.
func newAuthenticator(username string, passwordHash, signingKey []byte) *authenticator {
	return &authenticator{
		username:     username,
		passwordHash: passwordHash,
		signingKey:   signingKey,
		accessTTL:    defaultAccessTokenTTL,
		refreshTTL:   defaultRefreshTokenTTL,
	}
}

// verifyCredentials reports whether username and password match the configured
// admin account. The bcrypt comparison runs even when the username does not
// match so the cost is constant regardless of which field is wrong, avoiding
// username enumeration via timing.
func (a *authenticator) verifyCredentials(username, password string) bool {
	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) == 1
	passMatch := bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password)) == nil
	return userMatch && passMatch
}

// issueAccessToken mints a signed access JWT valid until now+accessTTL and returns
// the token string together with its expiry time.
func (a *authenticator) issueAccessToken(now time.Time) (string, time.Time, error) {
	return a.issueTypedToken(now, a.accessTTL, accessTokenType)
}

// issueRefreshToken mints a signed refresh JWT valid until now+refreshTTLand returns
// the token string together with its expiry time.
func (a *authenticator) issueRefreshToken(now time.Time) (string, time.Time, error) {
	return a.issueTypedToken(now, a.refreshTTL, refreshTokenType)
}

// issueTypedToken mints a signed JWT of the given type and lifetime.
func (a *authenticator) issueTypedToken(now time.Time, ttl time.Duration, typ string) (string, time.Time, error) {
	expires := now.Add(ttl)
	claims := jwt.RegisteredClaims{
		Subject:   a.username,
		Issuer:    tokenIssuer,
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expires),
		ID:        randomTokenID(),
	}
	// Embed the token type in a private claim so parseTypedToken can reject a
	// token used in the wrong role (e.g. a refresh token presented as a bearer).
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, typedClaims{RegisteredClaims: claims, TokenType: typ})
	signed, err := token.SignedString(a.signingKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing token: %w", err)
	}
	return signed, expires, nil
}

// typedClaims extends RegisteredClaims with a private "typ" claim.
type typedClaims struct {
	jwt.RegisteredClaims
	TokenType string `json:"typ"`
}

// parseToken verifies an access token and returns its claims.
func (a *authenticator) parseToken(tokenString string) (*jwt.RegisteredClaims, error) {
	return a.parseTypedToken(tokenString, accessTokenType)
}

// parseRefresh verifies a refresh token and returns its claims.
func (a *authenticator) parseRefresh(tokenString string) (*jwt.RegisteredClaims, error) {
	return a.parseTypedToken(tokenString, refreshTokenType)
}

// parseTypedToken verifies the signature, algorithm, issuer, expiry, and token
// type of a token and returns its claims.
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

// setRefreshCookie writes the refresh token as an HttpOnly, SameSite=Lax cookie
// scoped to the auth endpoints.
func (a *authenticator) setRefreshCookie(w http.ResponseWriter, refreshToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    refreshToken,
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(a.refreshTTL.Seconds()),
	})
}

// clearRefreshCookie expires the refresh cookie so subsequent refresh attempts
// fail and the client is forced back to login.
func (a *authenticator) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// refreshTokenFromCookie extracts the refresh token from the request cookie.
func (a *authenticator) refreshTokenFromCookie(r *http.Request) string {
	c, err := r.Cookie(refreshCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

// middleware wraps next, rejecting requests to protected API paths that do not
// carry a valid bearer token. Static assets, health checks, and the public API
// paths pass through untouched.
func (a *authenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
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

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. As a fallback it accepts an "access_token" query parameter, which is
// required for the SSE endpoint because the browser EventSource API cannot set
// request headers. Returns "" when no token is present.
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

// loginRequest is the JSON body for POST /api/v1/auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse is the JSON body returned on a successful login.
type loginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// handleLogin validates credentials and returns a freshly issued access token
// plus sets a refresh token as an HttpOnly cookie.
func handleLogin(a *authenticator) http.HandlerFunc {
	provider := newAdminProvider(a)
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := provider.Login(r)
		if err != nil {
			status, msg := mapLoginError(err)
			respondError(w, status, msg)
			return
		}
		writeSession(w, a, session)
	}
}

// handleRefresh exchanges a valid refresh cookie for a new access token and a
// rotated refresh cookie.
func handleRefresh(a *authenticator) http.HandlerFunc {
	provider := newAdminProvider(a)
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := provider.Refresh(r)
		if err != nil {
			a.clearRefreshCookie(w)
			respondError(w, http.StatusUnauthorized, mapRefreshError(err))
			return
		}
		writeSession(w, a, session)
	}
}

// handleLogout expires the refresh cookie.
func handleLogout(a *authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.clearRefreshCookie(w)
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeSession sets the refresh cookie (when issued) and returns the access
// token to the client.
func writeSession(w http.ResponseWriter, a *authenticator, s *Session) {
	if s.SetRefreshCookie {
		a.setRefreshCookie(w, s.RefreshToken)
	}
	respondJSON(w, http.StatusOK, loginResponse{Token: s.AccessToken, ExpiresAt: s.AccessExpiresAt})
}

// mapLoginError translates provider errors to HTTP status + message.
func mapLoginError(err error) (int, string) {
	if err == errInvalidCredentials {
		return http.StatusUnauthorized, "invalid username or password"
	}
	return http.StatusBadRequest, err.Error()
}

// mapRefreshError translates provider refresh errors to an HTTP message.
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
		// crypto/rand failures are catastrophic and effectively never happen;
		// fall back to a time-based value so a token is still produced.
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}

// currentNamespace determines the namespace the server is running in, trying
// the POD_NAMESPACE env var, then the in-cluster service account file, then a
// default.
func currentNamespace(getenv func(string) string) string {
	if ns := strings.TrimSpace(getenv("POD_NAMESPACE")); ns != "" {
		return ns
	}
	const saNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	if data, err := os.ReadFile(saNamespaceFile); err == nil {
		if ns := strings.TrimSpace(string(data)); ns != "" {
			return ns
		}
	}
	return defaultNamespace
}

// errInvalidCredentials is returned by the admin provider when the supplied
// username/password do not match.
var errInvalidCredentials = fmt.Errorf("invalid username or password")

// errMissingRefresh is returned by the admin provider when no refresh cookie is
// present on a refresh request.
var errMissingRefresh = fmt.Errorf("missing refresh token")

// decodeJSONBody decodes a JSON request body into v, returning a 400-style
// error when the body is malformed.
func decodeJSONBody(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("invalid request body")
	}
	return nil
}

// authSecretName returns the configured auth Secret name or the default.
func authSecretName(getenv func(string) string) string {
	if name := strings.TrimSpace(getenv("AUTH_SECRET_NAME")); name != "" {
		return name
	}
	return defaultAuthSecretName
}

// applyAuthConfig overrides authenticator defaults from environment variables.
// KOKUMI_TOKEN_TTL accepts a Go duration string (e.g. "30m", "2h") and tunes the
// access-token lifetime.
func applyAuthConfig(a *authenticator, getenv func(string) string) {
	if v := strings.TrimSpace(getenv("KOKUMI_TOKEN_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			a.accessTTL = d
		}
	}
}
