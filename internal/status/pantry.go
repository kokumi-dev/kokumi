package status

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// Ready marks the Pantry as reachable with valid credentials.
func (u *PantryUpdater) Ready(ctx context.Context, p *deliveryv1alpha1.Pantry, msg string) error {
	return u.set(ctx, p, metav1.ConditionTrue, "Ready", msg)
}

// Failed marks the Pantry as unreachable or as having invalid credentials.
func (u *PantryUpdater) Failed(ctx context.Context, p *deliveryv1alpha1.Pantry, err error) error {
	return u.set(ctx, p, metav1.ConditionFalse, "ConnectivityCheckFailed", err.Error())
}

func (u *PantryUpdater) set(
	ctx context.Context,
	pantry *deliveryv1alpha1.Pantry,
	condStatus metav1.ConditionStatus,
	reason string,
	msg string,
) error {
	pantry.Status.ObservedGeneration = pantry.Generation

	condition := metav1.Condition{
		Type:               deliveryv1alpha1.ConditionTypeReady,
		Status:             condStatus,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: pantry.Generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}

	meta.SetStatusCondition(&pantry.Status.Conditions, condition)

	if err := u.client.Status().Update(ctx, pantry); err != nil {
		if apierrors.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("failed to update Pantry status: %w", err)
	}

	return nil
}
