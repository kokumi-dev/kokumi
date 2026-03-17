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

// OrderUpdater updates the status of an Order object.
type OrderUpdater struct {
	client client.Client
}

// New returns an Updater backed by the given client.
func NewOrderUpdater(c client.Client) *OrderUpdater {
	return &OrderUpdater{client: c}
}

// Processing marks the Order as actively being processed.
func (u *OrderUpdater) Processing(ctx context.Context, r *deliveryv1alpha1.Order, configHash string) error {
	return u.set(ctx, r, deliveryv1alpha1.OrderPhaseProcessing, configHash, "Processing component configuration")
}

// Ready marks the Order as successfully reconciled.
func (u *OrderUpdater) Ready(ctx context.Context, r *deliveryv1alpha1.Order, configHash, msg string) error {
	return u.set(ctx, r, deliveryv1alpha1.OrderPhaseReady, configHash, msg)
}

// Failed marks the Order as failed with the supplied error as the message.
func (u *OrderUpdater) Failed(ctx context.Context, r *deliveryv1alpha1.Order, err error) error {
	r.Status.LatestConfigHash = ""
	return u.set(ctx, r, deliveryv1alpha1.OrderPhaseFailed, "", err.Error())
}

func (u *OrderUpdater) set(
	ctx context.Context,
	order *deliveryv1alpha1.Order,
	phase deliveryv1alpha1.OrderPhase,
	configHash string,
	msg string,
) error {
	order.Status.Phase = phase

	if phase == deliveryv1alpha1.OrderPhaseReady || phase == deliveryv1alpha1.OrderPhaseFailed {
		order.Status.ObservedGeneration = order.Generation
	}

	if configHash != "" {
		order.Status.LatestConfigHash = configHash
	}

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             string(phase),
		Message:            msg,
		ObservedGeneration: order.Generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}

	switch phase {
	case deliveryv1alpha1.OrderPhaseReady:
		condition.Status = metav1.ConditionTrue
	case deliveryv1alpha1.OrderPhaseFailed:
		condition.Type = "Degraded"
		condition.Reason = "ProcessingFailed"
	}

	meta.SetStatusCondition(&order.Status.Conditions, condition)

	if err := u.client.Status().Update(ctx, order); err != nil {
		if apierrors.IsConflict(err) {
			return nil
		}

		return fmt.Errorf("failed to update Order status: %w", err)
	}

	return nil
}
