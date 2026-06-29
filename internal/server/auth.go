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
	// defaultTokenTTL is the lifetime of an issued login token.
	defaultTokenTTL = time.Hour
	// tokenIssuer is the JWT "iss" claim value for tokens minted by this server.
	tokenIssuer = "kokumi"

	// Secret data keys.
	secretKeyUsername     = "username"
	secretKeyPasswordHash = "password-hash"
	secretKeySigningKey   = "signing-key"

	// signingMethod is the only JWT signing algorithm accepted by this server.
	signingMethod = "HS256"

	bearerPrefix = "Bearer "
)

// authenticator validates username/password credentials against a bcrypt hash
// and issues/verifies short-lived HMAC-signed JWTs. It is immutable after
// construction and safe for concurrent use.
type authenticator struct {
	username     string
	passwordHash []byte
	signingKey   []byte
	tokenTTL     time.Duration
}

// publicAPIPaths are API paths reachable without a valid token.
var publicAPIPaths = map[string]struct{}{
	"/api/v1/auth/login": {},
	"/api/v1/info":       {},
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

	return &authenticator{
		username:     username,
		passwordHash: passwordHash,
		signingKey:   signingKey,
		tokenTTL:     defaultTokenTTL,
	}, nil
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

// issueToken mints a signed JWT valid until now+tokenTTL and returns the token
// string together with its expiry time.
func (a *authenticator) issueToken(now time.Time) (string, time.Time, error) {
	expires := now.Add(a.tokenTTL)
	claims := jwt.RegisteredClaims{
		Subject:   a.username,
		Issuer:    tokenIssuer,
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expires),
		ID:        randomTokenID(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(a.signingKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing token: %w", err)
	}
	return signed, expires, nil
}

// parseToken verifies the signature, algorithm, issuer, and expiry of a token
// and returns its claims. Any failure yields a non-nil error.
func (a *authenticator) parseToken(tokenString string) (*jwt.RegisteredClaims, error) {
	claims := &jwt.RegisteredClaims{}
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
	return claims, nil
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

// handleLogin validates credentials and returns a freshly issued token.
func handleLogin(a *authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if !a.verifyCredentials(req.Username, req.Password) {
			respondError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		token, expires, err := a.issueToken(time.Now())
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to issue token")
			return
		}
		respondJSON(w, http.StatusOK, loginResponse{Token: token, ExpiresAt: expires})
	}
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

// authSecretName returns the configured auth Secret name or the default.
func authSecretName(getenv func(string) string) string {
	if name := strings.TrimSpace(getenv("AUTH_SECRET_NAME")); name != "" {
		return name
	}
	return defaultAuthSecretName
}
