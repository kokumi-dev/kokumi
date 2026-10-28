package server

import (
	"context"
	"errors"
	"fmt"
	"slices"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// Capability names a UI action the authenticated user may perform, in the form
// "<area>:<action>". The UI shows, hides or disables features by capability
// and never sees the RBAC rules behind them. Capabilities are display hints
// only: every operation is still enforced by the API server through the
// impersonated ServiceAccounts.
type Capability string

const (
	CapSettingsGeneralUpdate  Capability = "settings.general:update"
	CapSettingsAuthUpdate     Capability = "settings.auth:update"
	CapSettingsSigningUpdate  Capability = "settings.signing:update"
	CapSettingsScanningUpdate Capability = "settings.scanning:update"

	CapOrdersCreate Capability = "orders:create"
	CapOrdersUpdate Capability = "orders:update"
	CapOrdersDelete Capability = "orders:delete"

	CapProductsCreate Capability = "products:create"
	CapProductsUpdate Capability = "products:update"
	CapProductsDelete Capability = "products:delete"

	CapRegistriesCreate Capability = "registries:create"
	CapRegistriesUpdate Capability = "registries:update"
	CapRegistriesDelete Capability = "registries:delete"

	CapRepositoriesCreate Capability = "repositories:create"
	CapRepositoriesUpdate Capability = "repositories:update"
	CapRepositoriesDelete Capability = "repositories:delete"

	CapEnvironmentsCreate Capability = "environments:create"
	CapEnvironmentsUpdate Capability = "environments:update"
	CapEnvironmentsDelete Capability = "environments:delete"

	CapClustersCreate Capability = "clusters:create"
	CapClustersUpdate Capability = "clusters:update"
	CapClustersDelete Capability = "clusters:delete"

	CapPreparationsPromote Capability = "preparations:promote"
	CapPreparationsApprove Capability = "preparations:approve"

	// CapRegistriesCredentialsUpdate and CapClustersCredentialsUpdate are
	// namespaced; see namespacedCapabilityCatalog.
	CapRegistriesCredentialsUpdate Capability = "registries.credentials:update"
	CapClustersCredentialsUpdate   Capability = "clusters.credentials:update"
)

// accessCheck is one SelfSubjectAccessReview on a resource the server
// accesses as the user. An empty namespace checks all namespaces; an empty
// name checks any object.
type accessCheck struct {
	verb      string
	resource  string
	namespace string
	name      string
}

// capabilityRule grants a capability when every check is allowed for at least
// one mapped ServiceAccount and, if requirePersonalLogin is set, the user is
// signed in with a personal OIDC identity.
type capabilityRule struct {
	capability           Capability
	checks               []accessCheck
	requirePersonalLogin bool
}

// allNamespaces returns one check per verb on resource in all namespaces.
// Namespaced capabilities are global: they are granted only when RBAC allows
// the action cluster-wide, matching the event stream, which only serves users
// who may list cluster-wide.
func allNamespaces(resource string, verbs ...string) []accessCheck {
	checks := make([]accessCheck, 0, len(verbs))
	for _, verb := range verbs {
		checks = append(checks, accessCheck{verb: verb, resource: resource})
	}
	return checks
}

// capabilityCatalog is the single source of truth mapping capabilities to the
// RBAC checks of the server operations behind them.
func capabilityCatalog(installNamespace string) []capabilityRule {
	kitchen := []accessCheck{{
		verb:      "update",
		resource:  "kitchens",
		namespace: installNamespace,
		name:      deliveryv1alpha1.DefaultKitchenName,
	}}
	return []capabilityRule{
		// General, auth, signing and scanning settings share the Kitchen
		// singleton, so RBAC cannot tell them apart yet. They are separate
		// capabilities so the UI is unaffected once they can be split.
		{capability: CapSettingsGeneralUpdate, checks: kitchen},
		{capability: CapSettingsAuthUpdate, checks: kitchen},
		{capability: CapSettingsSigningUpdate, checks: kitchen},
		{capability: CapSettingsScanningUpdate, checks: kitchen},

		{capability: CapOrdersCreate, checks: allNamespaces("orders", "create")},
		{capability: CapOrdersUpdate, checks: allNamespaces("orders", "update")},
		{capability: CapOrdersDelete, checks: allNamespaces("orders", "delete")},

		{capability: CapProductsCreate, checks: allNamespaces("products", "create")},
		{capability: CapProductsUpdate, checks: allNamespaces("products", "update")},
		{capability: CapProductsDelete, checks: allNamespaces("products", "delete")},

		// Registry and Repository edits are applied as a patch.
		{capability: CapRegistriesCreate, checks: allNamespaces(registriesResource, "create")},
		{capability: CapRegistriesUpdate, checks: allNamespaces(registriesResource, "patch")},
		{capability: CapRegistriesDelete, checks: allNamespaces(registriesResource, "delete")},

		{capability: CapRepositoriesCreate, checks: allNamespaces(repositoriesResource, "create")},
		{capability: CapRepositoriesUpdate, checks: allNamespaces(repositoriesResource, "patch")},
		{capability: CapRepositoriesDelete, checks: allNamespaces(repositoriesResource, "delete")},

		{capability: CapEnvironmentsCreate, checks: allNamespaces(environmentsResource, "create")},
		{capability: CapEnvironmentsUpdate, checks: allNamespaces(environmentsResource, "update")},
		{capability: CapEnvironmentsDelete, checks: allNamespaces(environmentsResource, "delete")},

		// Cluster edits are applied as a patch. Storing a kubeconfig also
		// needs access to Secrets in the Cluster's namespace, which RBAC
		// grants per namespace.
		{capability: CapClustersCreate, checks: allNamespaces(clustersResource, "create")},
		{capability: CapClustersUpdate, checks: allNamespaces(clustersResource, "patch")},
		{capability: CapClustersDelete, checks: allNamespaces(clustersResource, "delete")},

		// Promote updates the Order's Serving, or creates it on first promotion.
		{capability: CapPreparationsPromote, checks: allNamespaces("servings", "create", "update")},
		// Votes are recorded against the approver's OIDC identity.
		{
			capability:           CapPreparationsApprove,
			checks:               allNamespaces("preparations", "approve"),
			requirePersonalLogin: true,
		},
	}
}

// namespacedCapabilityCatalog maps the capabilities that hold per namespace
// to their RBAC checks in namespace. The built-in roles grant none of them
// cluster-wide.
func namespacedCapabilityCatalog(namespace string) []capabilityRule {
	secrets := []accessCheck{
		{verb: "get", resource: secretsResource, namespace: namespace},
		{verb: "create", resource: secretsResource, namespace: namespace},
		{verb: "patch", resource: secretsResource, namespace: namespace},
	}
	return []capabilityRule{
		// Registry credentials are Secrets in the Registry's namespace. The
		// UI writes them, and reads through Registries, with the user's
		// identity. Platform admins grant this per namespace by binding the
		// kokumi-registry-credentials ClusterRole.
		{capability: CapRegistriesCredentialsUpdate, checks: secrets},
		// Cluster kubeconfigs are Secrets in the Cluster's namespace, written
		// with the user's identity; the cluster-secret-access policy also
		// requires get. Platform admins grant this per namespace by binding
		// the kokumi-cluster-credentials ClusterRole, never in Kokumi's own.
		{capability: CapClustersCredentialsUpdate, checks: secrets},
	}
}

// capabilities returns the capabilities the rules grant to the user and the
// capabilities that could not be determined, both sorted. It fails closed: an
// undetermined capability is not granted, and the errors behind it are
// returned so the caller can report them.
func (u *userClient) capabilities(ctx context.Context, id *Identity, rules []capabilityRule) (granted, undetermined []Capability, err error) {
	granted, undetermined = []Capability{}, []Capability{}
	var errs []error
	for _, rule := range rules {
		ok, err := u.grants(ctx, id, rule)
		if err != nil {
			undetermined = append(undetermined, rule.capability)
			errs = append(errs, fmt.Errorf("evaluating %s: %w", rule.capability, err))
			continue
		}
		if ok {
			granted = append(granted, rule.capability)
		}
	}
	slices.Sort(granted)
	slices.Sort(undetermined)
	return granted, undetermined, errors.Join(errs...)
}

// grants reports whether the rule grants its capability to the user. Each
// check may be satisfied by a different mapped ServiceAccount, mirroring how
// writes pick the first authorized ServiceAccount per operation.
func (u *userClient) grants(ctx context.Context, id *Identity, rule capabilityRule) (bool, error) {
	if rule.requirePersonalLogin && !id.isPersonalLogin() {
		return false, nil
	}
	for _, c := range rule.checks {
		allowed, err := u.allowedAny(ctx, c)
		if err != nil || !allowed {
			return false, err
		}
	}
	return true, nil
}

// allowedAny reports whether any mapped ServiceAccount passes the check. It
// errs only when no ServiceAccount passes and at least one could not be
// checked. The result is cached for ssarCacheTTL, which is acceptable for
// display hints.
func (u *userClient) allowedAny(ctx context.Context, c accessCheck) (bool, error) {
	sa, err := firstAllowed(u.sas, func(saName string) (bool, error) {
		return u.imp.authorizedFor(ctx, saName, c.verb, c.resource, c.namespace, c.name)
	})
	return sa != "", err
}
