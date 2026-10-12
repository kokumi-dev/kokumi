package status

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// PantryUpdater updates the status of a Pantry object.
type PantryUpdater struct {
	client client.Client
}

// NewPantryUpdater returns a PantryUpdater backed by the given client.
func NewPantryUpdater(c client.Client) *PantryUpdater {
	return &PantryUpdater{client: c}
}

// Ready marks the Pantry as reachable with valid credentials and records the
// probed referrers API support (empty when unknown).
func (u *PantryUpdater) Ready(ctx context.Context, pantry *deliveryv1alpha1.Pantry, msg string, referrersAPI deliveryv1alpha1.ReferrersAPISupport) error {
	return u.set(ctx, pantry, func(latest *deliveryv1alpha1.Pantry) {
		latest.Status.ObservedGeneration = latest.Generation
		latest.Status.ReferrersAPI = referrersAPI
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionTrue, "Ready", msg))
	})
}

// Failed marks the Pantry as unreachable or as having invalid credentials.
func (u *PantryUpdater) Failed(ctx context.Context, pantry *deliveryv1alpha1.Pantry, err error) error {
	return u.set(ctx, pantry, func(latest *deliveryv1alpha1.Pantry) {
		latest.Status.ObservedGeneration = latest.Generation
		latest.Status.ReferrersAPI = ""
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionFalse, "ConnectivityCheckFailed", err.Error()))
	})
}

func (u *PantryUpdater) set(ctx context.Context, pantry *deliveryv1alpha1.Pantry, mutate func(*deliveryv1alpha1.Pantry)) error {
	return setCondition(ctx, u.client, pantry, mutate)
}
