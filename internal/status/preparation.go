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

// PreparationUpdater updates the status of a Preparation object.
type PreparationUpdater struct {
	client client.Client
}

// NewPreparationUpdater returns a PreparationUpdater backed by the given client.
func NewPreparationUpdater(c client.Client) *PreparationUpdater {
	return &PreparationUpdater{client: c}
}

// Ready marks the Preparation as ready for serving.
func (u *PreparationUpdater) Ready(ctx context.Context, p *deliveryv1alpha1.Preparation, msg string) error {
	return u.set(ctx, p, metav1.ConditionTrue, "Ready", msg)
}

// Failed marks the Preparation as failed.
func (u *PreparationUpdater) Failed(ctx context.Context, p *deliveryv1alpha1.Preparation, err error) error {
	return u.set(ctx, p, metav1.ConditionFalse, "ProcessingFailed", err.Error())
}

// Pending marks the Preparation as pending.
func (u *PreparationUpdater) Pending(ctx context.Context, p *deliveryv1alpha1.Preparation, msg string) error {
	return u.set(ctx, p, metav1.ConditionUnknown, "Pending", msg)
}

func (u *PreparationUpdater) set(
	ctx context.Context,
	preparation *deliveryv1alpha1.Preparation,
	condStatus metav1.ConditionStatus,
	reason string,
	msg string,
) error {
	condition := metav1.Condition{
		Type:               deliveryv1alpha1.ConditionTypeReady,
		Status:             condStatus,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: preparation.Generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}

	meta.SetStatusCondition(&preparation.Status.Conditions, condition)

	if err := u.client.Status().Update(ctx, preparation); err != nil {
		if apierrors.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("failed to update Preparation status: %w", err)
	}

	return nil
}
