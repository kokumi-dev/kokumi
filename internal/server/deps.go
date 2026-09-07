package server

import (
	"github.com/go-logr/logr"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/spf13/afero"
	corev1 "k8s.io/api/core/v1"
)

// apiDeps groups the runtime dependencies used by HTTP handlers.
// All fields may be nil when no Kubernetes configuration was found; handlers
// return 503 Service Unavailable in that case.
type apiDeps struct {
	ociClient oci.Client
	fs        afero.Fs
	logger    logr.Logger
	authMgr   *authManager

	// impersonator builds per-ServiceAccount clients used to execute all
	// user-facing operations as the mapped identity (Kubernetes RBAC is the
	// single source of truth for authorization).
	impersonator *impersonator
	// saList returns the ServiceAccounts in the install namespace (from the
	// informer cache) used to resolve identity -> ServiceAccount mappings.
	saList func() []*corev1.ServiceAccount
}
