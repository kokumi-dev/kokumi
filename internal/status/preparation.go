package status

import (
	"context"

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
func (u *PreparationUpdater) Ready(ctx context.Context, preparation *deliveryv1alpha1.Preparation, msg string) error {
	return u.set(ctx, preparation, metav1.ConditionTrue, "Ready", msg)
}

// Failed marks the Preparation as failed.
func (u *PreparationUpdater) Failed(ctx context.Context, preparation *deliveryv1alpha1.Preparation, err error) error {
	return u.set(ctx, preparation, metav1.ConditionFalse, "ProcessingFailed", err.Error())
}

// Pending marks the Preparation as pending.
func (u *PreparationUpdater) Pending(ctx context.Context, preparation *deliveryv1alpha1.Preparation, msg string) error {
	return u.set(ctx, preparation, metav1.ConditionUnknown, "Pending", msg)
}

func (u *PreparationUpdater) set(ctx context.Context, preparation *deliveryv1alpha1.Preparation, condStatus metav1.ConditionStatus, reason, msg string) error {
	return SetCondition(ctx, u.client, preparation, func(latest *deliveryv1alpha1.Preparation) {
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, condStatus, reason, msg))
	})
}
