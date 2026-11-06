package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	providerAdmin = "admin"
	providerOIDC  = "oidc"
)

// authManager holds the active authenticator and OIDC provider behind a mutex
// so they can be swapped when the Kitchen config or Secrets change. Without a
// configured provider no one can sign in and the API stays closed. A Secret
// that cannot be read keeps the last state it gave.
type authManager struct {
	mu          sync.RWMutex
	auth        *authenticator // signing key, plus the admin account when it can sign in; nil until a signing key was read
	oidc        *oidcProvider  // OIDC provider; nil when not configured or not yet resolved
	adminLogin  bool           // true when admin credential login is available
	secret      string         // name of the admin Secret last resolved from
	oidcSecret  string         // name of the OIDC Secret last resolved from
	tokenSecret string         // name of the token signing-key Secret last resolved from
	reader      client.Reader  // informer cache, used for the Kitchen singleton
	apiReader   client.Reader  // direct API client, used for live Secret reads
	ns          string
	lifetimes   tokenLifetimes
	logger      logr.Logger
	ended       *endedSessions // record of ended sessions; nil records nothing
}

// endedSessions returns the record of ended sessions, or nil without one.
func (m *authManager) endedSessions() *endedSessions {
	if m == nil {
		return nil
	}
	return m.ended
}

// pruneEndedSessions drops the entries of the record of ended sessions that
// no session can match any more, every endedSessionsPruneInterval until ctx
// ends.
func (m *authManager) pruneEndedSessions(ctx context.Context) {
	ticker := time.NewTicker(endedSessionsPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a := m.get(); a != nil {
				if err := m.ended.prune(ctx, time.Now(), a.sessionTTL); err != nil {
					m.logger.Error(err, "Could not prune the record of ended sessions")
				}
			}
		}
	}
}

// newAuthManager builds an authManager and performs the initial resolution.
func newAuthManager(
	ctx context.Context,
	reader client.Reader,
	apiReader client.Reader,
	ns string,
	lifetimes tokenLifetimes,
	ended *endedSessions,
) *authManager {
	logger := log.FromContext(ctx)
	m := &authManager{
		reader:    reader,
		apiReader: apiReader,
		ns:        ns,
		lifetimes: lifetimes,
		logger:    logger,
		ended:     ended,
	}
	m.reload(ctx, nil)
	return m
}

// get returns the active authenticator (nil until a signing key was read).
func (m *authManager) get() *authenticator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.auth
}

// lastTokens returns the active authenticator without the admin account, or
// nil when there is none.
func (m *authManager) lastTokens() *authenticator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.auth == nil {
		return nil
	}
	return m.auth.withAdmin(nil)
}

// adminLoginEnabled reports whether password login is available (false
// without the admin account, where the authenticator is token-only).
func (m *authManager) adminLoginEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.adminLogin
}

// getOIDC returns the active OIDC provider (nil when unconfigured).
func (m *authManager) getOIDC() *oidcProvider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.oidc
}

// providers returns enabled provider names for the UI (subset of
// {"admin","oidc"}); "admin" only when password login is available.
func (m *authManager) providers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	if m.adminLogin {
		out = append(out, providerAdmin)
	}
	if m.oidc != nil {
		out = append(out, providerOIDC)
	}
	return out
}

// secretName returns the admin Secret last resolved, used to filter Secret events.
func (m *authManager) secretName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.secret
}

// oidcSecretName returns the OIDC Secret last resolved, used to filter Secret events.
func (m *authManager) oidcSecretName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.oidcSecret
}

// tokenSecretName returns the token signing-key Secret last resolved, used to
// filter Secret events.
func (m *authManager) tokenSecretName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tokenSecret
}

// tokenSigningKeySecretName returns the configured token signing-key Secret
// name (default when unset).
func tokenSigningKeySecretName(kitchen *deliveryv1alpha1.Kitchen) string {
	if kitchen != nil && kitchen.Spec.Auth != nil &&
		kitchen.Spec.Auth.TokenSigningKeySecretRef != nil &&
		kitchen.Spec.Auth.TokenSigningKeySecretRef.Name != "" {
		return kitchen.Spec.Auth.TokenSigningKeySecretRef.Name
	}
	return deliveryv1alpha1.DefaultTokenSigningKeySecretName
}

// reload resolves the admin-user and OIDC config from the Kitchen singleton and
// rebuilds both providers. The shared authenticator (signing key) is built from
// the dedicated token signing-key Secret, independent of adminUser, so OIDC-only
// setups work without an admin account. The admin account and OIDC are resolved
// independently, so a broken admin Secret never disables OIDC, and vice versa.
func (m *authManager) reload(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen) {
	var adminCfg *deliveryv1alpha1.AdminUserConfig
	var oidcCfg *deliveryv1alpha1.OIDCConfig
	if kitchen != nil && kitchen.Spec.Auth != nil {
		adminCfg = kitchen.Spec.Auth.AdminUser
		oidcCfg = kitchen.Spec.Auth.OIDC
	}
	// A disabled admin account is as good as none: no Secret is read for it.
	if adminCfg != nil && !adminCfg.Enabled {
		adminCfg = nil
	}
	tokenSecretName := tokenSigningKeySecretName(kitchen)

	// Shared trust root: signing key from the dedicated token Secret. While it
	// cannot be read, the last signing key keeps sessions verifiable.
	tokens, err := buildTokenAuthenticator(ctx, m.apiReader, m.ns, tokenSecretName, m.lifetimes)
	if err != nil {
		m.logger.Info("Token signing key not available", "reason", err.Error())
		tokens = m.lastTokens()
	}

	// No enabled adminUser, no password login: there is no default account.
	var admin *adminCredentials
	var adminErr error
	if adminCfg == nil {
		m.logger.Info("Admin authentication not configured", "reason", "adminUser not enabled")
	} else if admin, adminErr = readAdminCredentials(ctx, m.apiReader, m.ns, adminCfg); adminErr != nil {
		m.logger.Info("Admin authentication not available", "reason", adminErr.Error())
	}

	// OIDC reuses the shared authenticator; absent oidc block or ClientSecretRef
	// -> unconfigured (no default-name fallback). Errors keep last-known-good state.
	var oidc *oidcProvider
	var oidcErr error
	if oidcCfg == nil {
		m.logger.Info("OIDC authentication not configured", "reason", "oidc not set")
	} else if oidcCfg.ClientSecretRef == nil {
		m.logger.Info("OIDC authentication not configured", "reason", "oidc clientSecretRef not set")
	} else {
		oidc, oidcErr = buildOIDCProvider(ctx, m.apiReader, m.ns, oidcCfg, tokens)
		if oidcErr != nil {
			m.logger.Info("OIDC authentication not configured", "reason", oidcErr.Error())
		}
	}

	m.mu.Lock()
	issuer := m.apply(tokens, adminCfg, admin, adminErr, oidc, oidcCfg, oidcErr, tokenSecretName)
	auth, oidcOn := m.auth, m.oidc != nil
	adminLogin, secret := m.adminLogin, m.secret
	m.mu.Unlock()

	// Discover a new issuer now instead of at the first sign-in, so a wrong
	// issuerURL shows in the log and the first sign-in does not wait. It runs
	// in the background to keep informer handlers fast; a failure is retried
	// by the next sign-in.
	if issuer != nil {
		go func() {
			if _, err := issuer.discover(ctx); err != nil {
				m.logger.Error(err, "Could not discover OIDC issuer")
			}
		}()
	}

	switch {
	case !adminLogin && !oidcOn:
		m.logger.Info("No login method available")
	case adminLogin:
		m.logger.Info("Admin authentication enabled", "namespace", m.ns, "secret", secret, "username", auth.username)
	default:
		m.logger.Info("OIDC authentication enabled", "issuer", oidcCfg.IssuerURL, "secret", oidcCfg.ClientSecretRef.Name)
	}
}

// apply swaps the freshly built providers into the manager state. Callers must
// hold m.mu. Admin credentials whose Secret cannot be read are kept while they
// come from the same Secret; a Secret that is gone or holds no password hash
// ends admin login. An OIDC provider that fails to (re)build keeps its
// last-known-good state; an intentionally unset one (nil, nil) is applied
// as-is. It returns the OIDC issuer it introduced, or nil when the issuer is
// unchanged or OIDC is off.
func (m *authManager) apply(
	tokens *authenticator,
	adminCfg *deliveryv1alpha1.AdminUserConfig,
	admin *adminCredentials,
	adminErr error,
	oidc *oidcProvider,
	oidcCfg *deliveryv1alpha1.OIDCConfig,
	oidcErr error,
	tokenSecretName string,
) *oidcIssuer {
	var newIssuer *oidcIssuer
	if adminErr != nil && !errors.Is(adminErr, errNoAdminCredentials) &&
		m.adminLogin && adminCfg.SecretRef.Name == m.secret {
		admin = &adminCredentials{username: m.auth.username, passwordHash: m.auth.passwordHash}
	}
	if tokens != nil {
		m.auth = tokens.withAdmin(admin)
	}
	// Track the configured token Secret name even when the build failed, so
	// Secret events for the new name are not filtered out while it is missing.
	m.tokenSecret = tokenSecretName
	if adminCfg != nil && adminCfg.SecretRef != nil {
		m.secret = adminCfg.SecretRef.Name
	} else {
		m.secret = ""
	}
	if oidc != nil || oidcErr == nil {
		// The same issuer keeps what was discovered, so a reload neither asks
		// the issuer again nor loses it while the issuer is down.
		if oidc != nil && m.oidc != nil && m.oidc.issuer.url == oidc.issuer.url {
			oidc.issuer = m.oidc.issuer
		} else if oidc != nil {
			newIssuer = oidc.issuer
		}
		m.oidc = oidc
		if oidcCfg != nil && oidcCfg.ClientSecretRef != nil {
			m.oidcSecret = oidcCfg.ClientSecretRef.Name
		}
	}
	m.adminLogin = m.auth != nil && m.auth.passwordHash != nil
	return newIssuer
}

// refresh reads the latest Kitchen singleton and rebuilds the providers.
// It is safe to call from informer event handlers.
func (m *authManager) refresh(ctx context.Context) {
	kitchen := &deliveryv1alpha1.Kitchen{}
	if err := m.reader.Get(ctx, client.ObjectKey{Namespace: m.ns, Name: deliveryv1alpha1.DefaultKitchenName}, kitchen); err != nil {
		// If the singleton is gone, keep the last known state rather than
		// flipping auth off unexpectedly.
		m.logger.Info("Could not read Kitchen for auth reload", "error", err.Error())
		return
	}
	m.reload(ctx, kitchen)
}
