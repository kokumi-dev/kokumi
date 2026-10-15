package server

import (
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

// apiDeps groups the runtime dependencies used by HTTP handlers.
// All fields may be nil when no Kubernetes configuration was found; handlers
// return 503 Service Unavailable in that case.
type apiDeps struct {
	ociClient oci.Client
	store     *artifact.Store
	pipeline  *artifact.Pipeline
	logger    logr.Logger
	authMgr   *authManager

	// impersonator builds per-ServiceAccount clients used to execute all
	// user-facing operations as the mapped identity (Kubernetes RBAC is the
	// single source of truth for authorization).
	impersonator *impersonator
	// saList returns the ServiceAccounts in the install namespace (from the
	// informer cache) used to resolve identity -> ServiceAccount mappings.
	saList func() []*corev1.ServiceAccount

	// approvalWriter uses the server's own ServiceAccount. It is used only to
	// submit Approvals (the admission policy accepts them from no other
	// identity) and to read Approvals fresh from the API server.
	approvalWriter client.Client

	// kitchen returns the Kitchen singleton from the informer cache.
	kitchen func() (*deliveryv1alpha1.Kitchen, error)
}

// trustSet returns the keys the Kitchen trusts: its published public key and
// its trusted keys. Attestations are verified against them.
func (d *apiDeps) trustSet() (*signing.TrustSet, error) {
	kitchen, err := d.kitchen()
	if err != nil {
		return nil, fmt.Errorf("reading Kitchen: %w", err)
	}
	return signing.TrustSetForKitchen(kitchen)
}
