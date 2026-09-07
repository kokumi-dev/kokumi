package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// impersonator builds and caches per-ServiceAccount clients that impersonate
// "system:serviceaccount:<ns>:<sa>". All user-facing API operations are
// executed through these clients so plain Kubernetes RBAC on the mapped
// ServiceAccounts is the single source of truth for authorization.
type impersonator struct {
	// cfg is the server's own rest config (the kokumi-server SA); it is copied
	// per ServiceAccount with impersonation added.
	cfg *rest.Config
	// scheme used for the impersonated clients.
	scheme *runtime.Scheme
	// ns is the namespace the mapped ServiceAccounts live in.
	ns string

	mu      sync.Mutex
	clients map[string]client.Client // keyed by SA name
	ssar    map[string]ssarEntry     // keyed by sa|verb|resource|namespace
}

// ssarEntry is a cached SelfSubjectAccessReview result. RBAC grants would
// otherwise be sticky for the process lifetime; the TTL bounds how long a
// stale "allowed" answer can influence SA selection (the actual operation is
// always enforced by the API server regardless of this cache).
type ssarEntry struct {
	allowed   bool
	checkedAt time.Time
}

// ssarCacheTTL bounds the lifetime of cached SelfSubjectAccessReview results.
const ssarCacheTTL = 90 * time.Second

// newImpersonator creates the client cache backing the impersonation layer.
func newImpersonator(cfg *rest.Config, scheme *runtime.Scheme, ns string) *impersonator {
	return &impersonator{
		cfg:     cfg,
		scheme:  scheme,
		ns:      ns,
		clients: map[string]client.Client{},
		ssar:    map[string]ssarEntry{},
	}
}

// clientFor returns a cached client impersonating the named ServiceAccount.
func (imp *impersonator) clientFor(saName string) (client.Client, error) {
	imp.mu.Lock()
	defer imp.mu.Unlock()
	if c, ok := imp.clients[saName]; ok {
		return c, nil
	}
	cfg := rest.CopyConfig(imp.cfg)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: fmt.Sprintf("system:serviceaccount:%s:%s", imp.ns, saName),
	}
	c, err := client.New(cfg, client.Options{Scheme: imp.scheme})
	if err != nil {
		return nil, fmt.Errorf("building impersonated client for %s: %w", saName, err)
	}
	imp.clients[saName] = c
	return c, nil
}

// authorizedFor reports whether the ServiceAccount may perform verb on the
// delivery.kokumi.dev resource in the namespace (empty = cluster-scoped). The
// check is a SelfSubjectAccessReview executed as the impersonated SA, so the
// API server itself answers. Results are cached for ssarCacheTTL; the actual
// operation is always enforced by the API server regardless of this cache.
func (imp *impersonator) authorizedFor(ctx context.Context, saName, verb, resource, namespace string) (bool, error) {
	key := saName + "|" + verb + "|" + resource + "|" + namespace
	imp.mu.Lock()
	if e, ok := imp.ssar[key]; ok && time.Since(e.checkedAt) < ssarCacheTTL {
		imp.mu.Unlock()
		return e.allowed, nil
	}
	imp.mu.Unlock()

	c, err := imp.clientFor(saName)
	if err != nil {
		return false, err
	}
	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Group:     "delivery.kokumi.dev",
				Resource:  resource,
				Verb:      verb,
				Namespace: namespace,
			},
		},
	}
	if err := c.Create(ctx, review); err != nil {
		return false, fmt.Errorf("SelfSubjectAccessReview as %s: %w", saName, err)
	}
	allowed := review.Status.Allowed

	imp.mu.Lock()
	imp.ssar[key] = ssarEntry{allowed: allowed, checkedAt: time.Now()}
	imp.mu.Unlock()
	return allowed, nil
}

// errNotAuthorized is returned when no mapped ServiceAccount is permitted the
// operation; mapped to HTTP 403.
var errNotAuthorized = fmt.Errorf("not authorized")

// pickWriteSA returns the name of the first mapped ServiceAccount authorized
// for verb on resource in the namespace. Returns errNotAuthorized when no
// mapped SA is allowed (mapped to HTTP 403).
func (imp *impersonator) pickWriteSA(ctx context.Context, sas []corev1.ServiceAccount, verb, resource, namespace string) (string, error) {
	for _, sa := range sas {
		allowed, err := imp.authorizedFor(ctx, sa.Name, verb, resource, namespace)
		if err != nil {
			return "", err
		}
		if allowed {
			return sa.Name, nil
		}
	}
	return "", errNotAuthorized
}
