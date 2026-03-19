package status

import (
	"context"
	"fmt"
	"time"

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
	return u.set(ctx, s, deliveryv1alpha1.ServingPhaseDeploying, "Creating Argo CD Application")
}

// Deployed marks the Serving as successfully deployed.
func (u *ServingUpdater) Deployed(ctx context.Context, s *deliveryv1alpha1.Serving, msg string) error {
	return u.set(ctx, s, deliveryv1alpha1.ServingPhaseDeployed, msg)
}

// Pending marks the Serving as waiting for a prerequisite.
func (u *ServingUpdater) Pending(ctx context.Context, s *deliveryv1alpha1.Serving, msg string) error {
	return u.set(ctx, s, deliveryv1alpha1.ServingPhasePending, msg)
}

// Failed marks the Serving as failed with the supplied error as the message.
func (u *ServingUpdater) Failed(ctx context.Context, s *deliveryv1alpha1.Serving, err error) error {
	return u.set(ctx, s, deliveryv1alpha1.ServingPhaseFailed, err.Error())
}

func (u *ServingUpdater) set(
	ctx context.Context,
	serving *deliveryv1alpha1.Serving,
	phase deliveryv1alpha1.ServingPhase,
	msg string,
) error {
	serving.Status.Phase = phase

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             string(phase),
		Message:            msg,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}

	switch phase {
	case deliveryv1alpha1.ServingPhaseDeployed:
		condition.Status = metav1.ConditionTrue
	case deliveryv1alpha1.ServingPhaseFailed:
		condition.Type = conditionTypeDegraded
		condition.Reason = "DeploymentFailed"
	}

	meta.SetStatusCondition(&serving.Status.Conditions, condition)

	if err := u.client.Status().Update(ctx, serving); err != nil {
		return fmt.Errorf("failed to update Serving status: %w", err)
	}

	return nil
}
