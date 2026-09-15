package server

import (
	"context"
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
// so they can be swapped when the Kitchen config or Secrets change. Auth is
// intentionally off only when no provider is configured; any other failure
// keeps last-known-good state so the API fails closed.
type authManager struct {
	mu          sync.RWMutex
	auth        *authenticator // admin account; nil when disabled or not yet resolved
	oidc        *oidcProvider  // OIDC provider; nil when not configured or not yet resolved
	disabled    bool           // true only when auth is intentionally off (no providers)
	adminLogin  bool           // true when admin credential login is available
	secret      string         // name of the admin Secret last resolved from
	oidcSecret  string         // name of the OIDC Secret last resolved from
	tokenSecret string         // name of the token signing-key Secret last resolved from
	reader      client.Reader  // informer cache, used for the Kitchen singleton
	apiReader   client.Reader  // direct API client, used for live Secret reads
	ns          string
	tokenTTL    time.Duration
	logger      logr.Logger
}

// newAuthManager builds an authManager and performs the initial resolution.
func newAuthManager(
	ctx context.Context,
	reader client.Reader,
	apiReader client.Reader,
	ns string,
	tokenTTL time.Duration,
) *authManager {
	logger := log.FromContext(ctx)
	m := &authManager{
		reader:    reader,
		apiReader: apiReader,
		ns:        ns,
		tokenTTL:  tokenTTL,
		logger:    logger,
	}
	m.reload(ctx, nil)
	return m
}

// get returns the active admin authenticator (nil when unconfigured).
func (m *authManager) get() *authenticator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.auth
}

// getState returns the active authenticator and disabled flag under one lock.
func (m *authManager) getState() (*authenticator, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.auth, m.disabled
}

// adminLoginEnabled reports whether password login is available (false in
// OIDC-only or disabled mode, where the authenticator is token-only).
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
// setups work without an admin account. The two providers are resolved
// independently so a broken OIDC config never disables admin login, and vice versa.
func (m *authManager) reload(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen) {
	var adminCfg *deliveryv1alpha1.AdminUserConfig
	var oidcCfg *deliveryv1alpha1.OIDCConfig
	if kitchen != nil && kitchen.Spec.Auth != nil {
		adminCfg = kitchen.Spec.Auth.AdminUser
		oidcCfg = kitchen.Spec.Auth.OIDC
	}
	tokenSecretName := tokenSigningKeySecretName(kitchen)

	// Shared trust root: signing key from the dedicated token Secret. Errors
	// keep last-known-good state.
	auth, authErr := buildTokenAuthenticator(ctx, m.apiReader, m.ns, tokenSecretName, m.tokenTTL)
	if authErr != nil {
		m.logger.Info("Token signing key not available", "reason", authErr.Error())
	}

	// Admin credentials layer on top of the shared authenticator: absent
	// adminUser or unset SecretRef -> no password login (fail closed, no
	// default-name fallback). Errors keep last-known-good state.
	var adminErr error
	if adminCfg == nil {
		m.logger.Info("Admin authentication not configured", "reason", "adminUser not set")
	} else if adminCfg.SecretRef == nil {
		m.logger.Info("Admin authentication not configured", "reason", "admin secretRef not set")
	} else if auth != nil {
		auth, adminErr = applyAdminCredentials(ctx, m.apiReader, m.ns, adminCfg, auth)
		if adminErr != nil {
			m.logger.Info("Admin authentication not configured", "reason", adminErr.Error())
		}
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
		oidc, oidcErr = buildOIDCProvider(ctx, m.apiReader, m.ns, oidcCfg, auth)
		if oidcErr != nil {
			m.logger.Info("OIDC authentication not configured", "reason", oidcErr.Error())
		}
	}

	// A provider that fails to (re)build keeps last-known-good state so the API
	// fails closed; an intentionally disabled provider (nil, nil) is applied as-is.
	m.mu.Lock()
	m.apply(auth, adminCfg, adminErr, oidc, oidcCfg, oidcErr, tokenSecretName)
	m.mu.Unlock()

	switch {
	case m.disabled:
		m.logger.Info("Authentication disabled", "reason", "no identity providers configured")
	case m.adminLogin && adminCfg != nil:
		m.logger.Info("Admin authentication enabled", "namespace", m.ns, "secret", adminCfg.SecretRef.Name, "username", adminCfg.Username)
	case m.adminLogin:
		m.logger.Info("Admin authentication enabled (last known good)", "namespace", m.ns)
	case oidc != nil:
		m.logger.Info("OIDC authentication enabled", "issuer", oidcCfg.IssuerURL, "secret", oidcCfg.ClientSecretRef.Name)
	}
}

// apply swaps the freshly built providers into the manager state. Callers must
// hold m.mu. A provider that fails to (re)build keeps last-known-good state so
// the API fails closed; an intentionally disabled provider (nil, nil) is
// applied as-is.
func (m *authManager) apply(
	auth *authenticator,
	adminCfg *deliveryv1alpha1.AdminUserConfig,
	adminErr error,
	oidc *oidcProvider,
	oidcCfg *deliveryv1alpha1.OIDCConfig,
	oidcErr error,
	tokenSecretName string,
) {
	prevAuth := m.auth
	if auth != nil {
		// A transient admin-credentials error must not wipe the last-known-good
		// credentials, but only when the credentials source is unchanged and
		// the admin is still enabled; a changed SecretRef or a disabled admin
		// must take effect immediately, not resurrect stale credentials.
		if adminErr != nil && prevAuth != nil && prevAuth.passwordHash != nil &&
			adminCfg != nil && adminCfg.SecretRef != nil &&
			adminCfg.SecretRef.Name == m.secret && adminCfg.IsEnabled() {
			auth.username = prevAuth.username
			auth.passwordHash = prevAuth.passwordHash
		}
		m.auth = auth
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
		m.oidc = oidc
		if oidcCfg != nil && oidcCfg.ClientSecretRef != nil {
			m.oidcSecret = oidcCfg.ClientSecretRef.Name
		}
	}
	// adminLogin reflects the retained authenticator (may be last-known-good),
	// not the freshly-built one that may have failed.
	m.adminLogin = m.auth != nil && m.auth.passwordHash != nil
	// Disabled only when neither admin login nor OIDC is available; a token-only
	// or last-known-good authenticator keeps auth enabled so a transient error
	// fails closed rather than opening the API.
	m.disabled = !m.adminLogin && m.oidc == nil
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
