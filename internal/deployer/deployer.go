package deployer

import (
	"context"
	"errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// ErrOptInRequired is returned by VerifyOptIn when a pre-existing deployment
// exists but is not opted in for management by this Serving's Order. It is a
// terminal condition for the current generation: the reconciler surfaces it
// in the Serving status without requeueing.
var ErrOptInRequired = errors.New("existing deployment not opted in for this order")

// Phase describes the coarse-grained state of a deployment as reported by the
// deployer implementation.
type Phase string

const (
	// PhaseProgressing means the deployment exists but has not yet reached
	// the desired revision or is not healthy yet.
	PhaseProgressing Phase = "Progressing"

	// PhaseHealthy means the deployment is synced to the desired revision
	// and reports healthy.
	PhaseHealthy Phase = "Healthy"

	// PhaseDegraded means the deployment reports an unhealthy state.
	PhaseDegraded Phase = "Degraded"

	// PhaseMissing means no deployment exists for the Serving.
	PhaseMissing Phase = "Missing"
)

// DeploymentStatus is the deployer's observation of the deployment belonging
// to a Serving.
type DeploymentStatus struct {
	// Phase is the coarse-grained deployment state.
	Phase Phase

	// Revision is the revision (artifact digest) the deployment currently
	// has synced to. It is empty when unknown.
	Revision string

	// Message is a human-readable detail, e.g. the health message reported
	// by the deployment.
	Message string
}

// Deployer deploys the artifact of a Preparation for a Serving.
type Deployer interface {

	// VerifyOptIn checks whether a pre-existing deployment for the Serving
	// may be managed by this Serving's Order. It returns nil when there is
	// no deployment yet (the create path is always allowed) or when the
	// deployment is opted in. Otherwise it returns an error wrapping
	// ErrOptInRequired.
	VerifyOptIn(ctx context.Context, serving *deliveryv1alpha1.Serving) error

	// Deploy creates or updates the deployment for the Serving so that it
	// points at the Preparation's artifact.
	Deploy(ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation) error

	// Status reports the current state of the deployment belonging to the
	// Serving.
	Status(ctx context.Context, serving *deliveryv1alpha1.Serving) (DeploymentStatus, error)

	// Remove deletes the deployment for the given Serving.
	Remove(ctx context.Context, serving *deliveryv1alpha1.Serving) error

	// WatchObject returns an empty object of the resource type the deployer
	// manages for its deployments. Controllers watch this type to re-trigger
	// reconciliation when the deployment's state changes externally.
	WatchObject() client.Object

	// WatchPredicate filters events on the watched deployment object so that
	// reconciliation is only re-triggered when the deployment's health
	// changed. Create and delete events always pass through.
	WatchPredicate() predicate.Predicate

	// EnqueueRequests maps an event on the watched deployment object to the
	// reconcile requests of the owning Serving(s). It returns nil when the
	// object is not owned by any Serving.
	EnqueueRequests(ctx context.Context, obj client.Object) []reconcile.Request
}
