package server

import (
	"context"
	"sync"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// authManager holds the currently active authenticator behind a mutex so it can
// be swapped at runtime when the Kitchen adminUser config or the referenced
// Secret changes.
//
// Authentication is treated as "intentionally disabled" only when the resolved
// config sets enabled=false (or the credentials Secret is absent at startup).
// Any other failure (a transient API error, a temporarily incomplete Secret)
// is treated as an error and the last-known-good authenticator is retained so
// the API fails closed instead of silently opening up.
type authManager struct {
	mu        sync.RWMutex
	auth      *authenticator
	disabled  bool
	secret    string        // name of the Secret the config was last resolved from
	reader    client.Reader // informer cache, used for the Kitchen singleton
	apiReader client.Reader // direct API client, used for live Secret reads
	ns        string
	getenv    func(string) string
	logger    logr.Logger
}

// newAuthManager builds an authManager and performs the initial resolution.
func newAuthManager(
	ctx context.Context,
	reader client.Reader,
	apiReader client.Reader,
	ns string,
	getenv func(string) string,
	logger logr.Logger,
) *authManager {
	m := &authManager{
		reader:    reader,
		apiReader: apiReader,
		ns:        ns,
		getenv:    getenv,
		logger:    logger,
	}
	m.reload(ctx, nil)
	return m
}

// get returns the active authenticator (nil when auth is disabled or not yet
// resolved).
func (m *authManager) get() *authenticator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.auth
}

// getState returns the active authenticator together with the disabled flag
// under a single read lock. disabled is true only when auth is intentionally
// off (adminUser.enabled=false); a nil authenticator with disabled=false means
// authentication is configured but currently unresolvable (e.g. the credentials
// Secret is missing) and the API must fail closed.
func (m *authManager) getState() (*authenticator, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.auth, m.disabled
}

// secretName returns the name of the Secret the manager last resolved its
// config from. It is used by the watcher to filter Secret events so a custom
// spec.adminUser.secretRef is picked up without watching every Secret.
func (m *authManager) secretName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.secret
}

// reload resolves the admin-user config from the Kitchen singleton (when
// provided) and rebuilds the authenticator. A nil kitchen falls back to the
// legacy Secret-only behavior.
func (m *authManager) reload(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen) {
	cfg := resolveAdminUserConfig(kitchenAdminUser(kitchen))

	_, hadAuth := m.getState()
	auth, err := buildAuthenticator(ctx, m.apiReader, m.ns, cfg, m.getenv)
	if err != nil {
		// Any error other than an intentional disable (missing/incomplete
		// Secret, transient API failure): keep the last-known-good
		// authenticator so the API fails closed instead of opening up.
		// A fresh install with no Secret yet is expected, so log it at Info
		// level; only a degradation of a previously-working state is an Error.
		if hadAuth {
			m.logger.Error(nil, "Authentication not updated, keeping previous state", "reason", err.Error())
		} else {
			m.logger.Info("Authentication not yet configured", "reason", err.Error())
		}
		return
	}
	// auth == nil with no error means the admin account is intentionally
	// disabled (buildAuthenticator returns nil,nil when enabled=false).
	m.set(auth, auth == nil, cfg.SecretName)
	if auth == nil {
		m.logger.Info("Authentication disabled", "reason", "adminUser.enabled is false")
		return
	}
	m.logger.Info("Authentication enabled", "namespace", m.ns, "secret", cfg.SecretName, "username", cfg.Username)
}

// set stores the authenticator under the lock. disabled reports whether auth
// is intentionally off (nil auth + disabled=true) versus not yet resolved.
func (m *authManager) set(auth *authenticator, disabled bool, secret string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auth = auth
	m.disabled = disabled
	m.secret = secret
}

// refresh reads the latest Kitchen singleton and rebuilds the authenticator.
// It is safe to call from informer event handlers.
func (m *authManager) refresh(ctx context.Context) {
	kitchen := &deliveryv1alpha1.Kitchen{}
	if err := m.reader.Get(ctx, client.ObjectKey{Namespace: m.ns, Name: "default"}, kitchen); err != nil {
		// If the singleton is gone, keep the last known state rather than
		// flipping auth off unexpectedly.
		m.logger.Info("Could not read Kitchen for auth reload", "error", err.Error())
		return
	}
	m.reload(ctx, kitchen)
}
