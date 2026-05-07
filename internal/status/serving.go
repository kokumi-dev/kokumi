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

// ServingUpdater updates the status of a Serving object.
type ServingUpdater struct {
	client client.Client
}

// NewServingUpdater returns a ServingUpdater backed by the given client.
func NewServingUpdater(c client.Client) *ServingUpdater {
	return &ServingUpdater{client: c}
}

// Deploying marks the Serving as actively being deployed.
func (u *ServingUpdater) Deploying(ctx context.Context, s *deliveryv1alpha1.Serving) error {
	return u.set(ctx, s, metav1.ConditionUnknown, "Deploying", "Creating Argo CD Application")
}

// Deployed marks the Serving as successfully deployed.
func (u *ServingUpdater) Deployed(ctx context.Context, s *deliveryv1alpha1.Serving, msg string) error {
	return u.set(ctx, s, metav1.ConditionTrue, "Deployed", msg)
}

// Pending marks the Serving as waiting for a prerequisite.
func (u *ServingUpdater) Pending(ctx context.Context, s *deliveryv1alpha1.Serving, msg string) error {
	return u.set(ctx, s, metav1.ConditionUnknown, "Pending", msg)
}

// Failed marks the Serving as failed with the supplied error as the message.
func (u *ServingUpdater) Failed(ctx context.Context, s *deliveryv1alpha1.Serving, err error) error {
	return u.set(ctx, s, metav1.ConditionFalse, "DeploymentFailed", err.Error())
}

func (u *ServingUpdater) set(
	ctx context.Context,
	serving *deliveryv1alpha1.Serving,
	condStatus metav1.ConditionStatus,
	reason string,
	msg string,
) error {
	serving.Status.ObservedGeneration = serving.Generation

	condition := metav1.Condition{
		Type:               deliveryv1alpha1.ConditionTypeReady,
		Status:             condStatus,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: serving.Generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}

	meta.SetStatusCondition(&serving.Status.Conditions, condition)

	if err := u.client.Status().Update(ctx, serving); err != nil {
		if apierrors.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("failed to update Serving status: %w", err)
	}

	return nil
}
