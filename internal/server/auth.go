package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// defaultAccessTokenTTL is the default access-token lifetime. Access
	// tokens are checked locally on every request, so it bounds how long a
	// copied token works when the record of ended sessions cannot be written.
	defaultAccessTokenTTL = 5 * time.Minute
	// defaultSessionTTL is how long a session lasts from sign-in by default.
	// Refreshing never extends it.
	defaultSessionTTL = 24 * time.Hour
	// defaultSessionIdleTTL is how long a session lasts without user activity
	// by default, the longest inactivity NIST SP 800-63B allows at AAL2. An
	// idle TTL of at least the session TTL turns the limit off.
	defaultSessionIdleTTL = time.Hour
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
	// providerTokenKey seals the provider token cookie; it is derived from
	// signingKey.
	providerTokenKey []byte
	accessTTL        time.Duration
	sessionTTL       time.Duration
	idleTTL          time.Duration
}

// tokenLifetimes overrides the default token lifetimes; a zero field keeps
// the default.
type tokenLifetimes struct {
	// access is the access-token lifetime (KOKUMI_TOKEN_TTL).
	access time.Duration
	// session is how long a session lasts from sign-in (KOKUMI_SESSION_TTL).
	session time.Duration
	// idle is how long a session lasts without activity
	// (KOKUMI_SESSION_IDLE_TTL).
	idle time.Duration
}

// publicAPIPaths are API paths reachable without a valid access token.
var publicAPIPaths = map[string]struct{}{
	"/api/v1/auth/login":                   {},
	"/api/v1/auth/refresh":                 {},
	"/api/v1/auth/logout":                  {},
	"/api/v1/auth/oidc/start":              {},
	"/api/v1/auth/oidc/callback":           {},
	"/api/v1/auth/oidc/backchannel-logout": {},
	"/api/v1/info":                         {},
	"/api/v1/signing":                      {},
}

// newAuthenticator builds an authenticator with default token lifetimes.
func newAuthenticator(username string, passwordHash, signingKey []byte) *authenticator {
	return &authenticator{
		username:         username,
		passwordHash:     passwordHash,
		signingKey:       signingKey,
		providerTokenKey: deriveProviderTokenKey(signingKey),
		accessTTL:        defaultAccessTokenTTL,
		sessionTTL:       defaultSessionTTL,
		idleTTL:          defaultSessionIdleTTL,
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
	if lifetimes.idle > 0 {
		auth.idleTTL = lifetimes.idle
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

// sessionTokens are the pair of tokens a sign-in or a refresh issues. The
// access token is short-lived and presented as a Bearer header (or the
// access_token query parameter for SSE); the refresh token travels in an
// HttpOnly cookie.
type sessionTokens struct {
	accessToken     string
	accessExpiresAt time.Time
	refreshToken    string
	// refreshExpiresAt is when the refresh token and its cookie expire: the
	// idle deadline, never after the end of the session.
	refreshExpiresAt time.Time
}

// sessionState is what every token of a session carries besides the
// identity. A refresh keeps it, except that a check at the OIDC provider
// moves ProviderCheckAt.
type sessionState struct {
	// ID (claim sid) names the session. It is random, set at sign-in, and
	// binds the provider token cookie to its session.
	ID string
	// AuthTime (claim auth_time) is when the user signed in. The session
	// ends sessionTTL later.
	AuthTime time.Time
	// ProviderCheckAt (claim idp_check) is when a refresh asks the OIDC
	// provider again whether the user stays signed in. It is zero for a
	// session the provider is not asked about.
	ProviderCheckAt time.Time
	// ProviderSessionID (claim idp_sid) is the sid of the OIDC provider's
	// session the user signed in with, when the provider names it. A
	// back-channel logout of that session ends this one.
	ProviderSessionID string
}

// newSessionState returns the state of a session the user signs in to at now.
func newSessionState(now time.Time) sessionState {
	return sessionState{ID: rand.Text(), AuthTime: now}
}

// sessionEnd is when session s ends: sessionTTL after the sign-in, so a
// changed sessionTTL applies to running sessions too.
func (a *authenticator) sessionEnd(s sessionState) time.Time {
	return s.AuthTime.Add(a.sessionTTL)
}

// idleDeadline returns when session s ends without activity: idleTTL from
// now when the user was active, else the previous deadline. It is never later
// than idleTTL from now or than the end of the session.
func (a *authenticator) idleDeadline(now time.Time, s sessionState, previous time.Time, active bool) time.Time {
	deadline := now.Add(a.idleTTL)
	if !active && previous.Before(deadline) {
		deadline = previous
	}
	if end := a.sessionEnd(s); end.Before(deadline) {
		deadline = end
	}
	return deadline
}

// signInTokens mints the first tokens of session s for id, at its sign-in.
func (a *authenticator) signInTokens(id *Identity, s sessionState) (*sessionTokens, error) {
	return a.issueTokens(s.AuthTime, id, s, a.idleDeadline(s.AuthTime, s, time.Time{}, true))
}

// issueTokens mints the access and refresh tokens of session s for id. The
// refresh token expires at idleUntil, the idle deadline; the access token
// accessTTL from now, or at idleUntil if that is sooner. Both tokens carry
// the whole identity and the session state, so a refreshed OIDC session
// stays an OIDC session.
func (a *authenticator) issueTokens(now time.Time, id *Identity, s sessionState, idleUntil time.Time) (*sessionTokens, error) {
	if id.Subject == "" {
		return nil, errors.New("identity has no subject")
	}
	if s.ID == "" || s.AuthTime.IsZero() {
		return nil, errors.New("session has no ID or sign-in time")
	}
	accessExpires := now.Add(a.accessTTL)
	if idleUntil.Before(accessExpires) {
		accessExpires = idleUntil
	}
	access, err := a.signToken(now, accessTokenType, id, s, accessExpires)
	if err != nil {
		return nil, fmt.Errorf("issuing access token: %w", err)
	}
	refresh, err := a.signToken(now, refreshTokenType, id, s, idleUntil)
	if err != nil {
		return nil, fmt.Errorf("issuing refresh token: %w", err)
	}
	return &sessionTokens{
		accessToken:      access,
		accessExpiresAt:  accessExpires,
		refreshToken:     refresh,
		refreshExpiresAt: idleUntil,
	}, nil
}

// signToken mints a session JWT of tokenType for id in session s that
// expires at expires. Email, groups and provider are embedded for
// ServiceAccount mapping.
func (a *authenticator) signToken(now time.Time, tokenType string, id *Identity, s sessionState, expires time.Time) (string, error) {
	registered := jwt.RegisteredClaims{
		Subject:   id.Subject,
		Issuer:    tokenIssuer,
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expires),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, newIdentityClaims(registered, tokenType, id, s))
	signed, err := token.SignedString(a.signingKey)
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}
	return signed, nil
}

// verifiedToken is a session token that parseToken verified.
type verifiedToken struct {
	identity *Identity
	session  sessionState
	// expires is the token's exp; for a refresh token, the idle deadline.
	expires time.Time
}

// parseToken verifies a session token. It accepts only an unexpired HS256
// token signed with the signing key, issued by tokenIssuer, of tokenType,
// with a subject, a session ID and a sign-in time. The auth middleware
// checks access tokens with it, and a refresh checks refresh tokens.
func (a *authenticator) parseToken(tokenString, tokenType string) (*verifiedToken, error) {
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
		return nil, err
	}
	if claims.TokenType != tokenType {
		return nil, fmt.Errorf("unexpected token type %q, want %q", claims.TokenType, tokenType)
	}
	if claims.Subject == "" {
		return nil, errors.New("token has no subject")
	}
	if claims.SessionID == "" || claims.AuthTime == nil {
		return nil, errors.New("token has no session ID or sign-in time")
	}
	return &verifiedToken{
		identity: claims.identity(),
		session:  claims.session(),
		expires:  claims.ExpiresAt.Time,
	}, nil
}

// isHTTPS reports TLS (direct or via X-Forwarded-Proto). Cookies are only
// Secure then, so plain-HTTP UIs (e.g. Safari on localhost) still allow silent refresh.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// newCookie returns an HttpOnly, SameSite=Lax cookie that lasts maxAge
// seconds; a negative maxAge clears it.
func newCookie(name, value, path string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

// expiredCookie returns the cookie that clears the cookie name of path.
func expiredCookie(name, path string) *http.Cookie {
	return newCookie(name, "", path, -1)
}

// writeCookies sets cookies, Secure only over HTTPS.
func writeCookies(w http.ResponseWriter, r *http.Request, cookies ...*http.Cookie) {
	for _, c := range cookies {
		c.Secure = isHTTPS(r)
		http.SetCookie(w, c)
	}
}

// setCookie writes a cookie of newCookie.
func setCookie(w http.ResponseWriter, r *http.Request, name, value, path string, maxAge int) {
	writeCookies(w, r, newCookie(name, value, path, maxAge))
}

// clearCookie expires a cookie written by setCookie.
func clearCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	writeCookies(w, r, expiredCookie(name, path))
}

// maxAgeUntil returns the Max-Age, in seconds, of a cookie that lasts until t.
func maxAgeUntil(t time.Time) int {
	return max(1, int(time.Until(t).Seconds()))
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
		t, err := a.parseToken(token, accessTokenType)
		if err != nil {
			respondError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		if m.ended.ended(t) {
			respondError(w, http.StatusUnauthorized, "the session has ended")
			return
		}
		next.ServeHTTP(w, r.WithContext(withVerifiedToken(r.Context(), t)))
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

// handleLogin signs in to the admin account: it returns an access token
// and sets the refresh cookie.
func handleLogin(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		res, err := m.signInAdmin(req.Username, req.Password)
		writeSession(w, r, res, err)
	}
}

// refreshRequest is the POST /api/v1/auth/refresh body.
type refreshRequest struct {
	// Active reports user input since the last refresh, which moves the idle
	// deadline. A background renewal sends false and keeps it.
	Active bool `json:"active"`
}

// handleRefresh exchanges a valid refresh cookie for a new access token and
// a rotated cookie (see refreshSession). A refusal that ends the session
// answers 401 and clears its cookies; a provider that cannot be asked
// answers 503, which the UI retries until its access token expires.
func handleRefresh(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			respondError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		res, err := m.refreshSession(r.Context(), browserSessionOf(r), req.Active)
		writeSession(w, r, res, err)
	}
}

// logoutResponse is returned by a sign-out that continues at the OIDC
// provider: the UI sends the browser to EndSessionURL.
type logoutResponse struct {
	EndSessionURL string `json:"endSessionURL"`
}

// handleLogout ends the session in this browser (see endSession). It
// answers 204, or 200 with the URL of the sign-out at the OIDC provider.
func handleLogout(m *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res := m.endSession(r.Context(), browserSessionOf(r), postLogoutRedirectURI(r))
		writeCookies(w, r, res.cookies...)
		if res.endSessionURL == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		respondJSON(w, http.StatusOK, logoutResponse{EndSessionURL: res.endSessionURL})
	}
}

// writeSession answers a sign-in or a refresh: it sets the cookies of res,
// then returns the access token, or answers the refusal err.
func writeSession(w http.ResponseWriter, r *http.Request, res *sessionResult, err error) {
	writeCookies(w, r, res.cookies...)
	if err != nil {
		respondSessionError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, loginResponse{Token: res.tokens.accessToken, ExpiresAt: res.tokens.accessExpiresAt})
}

// errNoAdminCredentials is returned when the admin Secret does not exist or
// holds no password hash: admin login is off until it has one.
var errNoAdminCredentials = errors.New("no admin credentials")
