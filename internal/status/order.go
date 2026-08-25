package status

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// OrderUpdater updates the status of an Order object.
type OrderUpdater struct {
	client client.Client
}

// NewOrderUpdater returns an OrderUpdater backed by the given client.
func NewOrderUpdater(c client.Client) *OrderUpdater {
	return &OrderUpdater{client: c}
}

// Processing marks the Order as actively being processed.
func (u *OrderUpdater) Processing(ctx context.Context, order *deliveryv1alpha1.Order, configHash string) error {
	return u.set(ctx, order, metav1.ConditionUnknown, "Processing", configHash, "Processing component configuration")
}

// Ready marks the Order as successfully reconciled.
func (u *OrderUpdater) Ready(ctx context.Context, order *deliveryv1alpha1.Order, configHash, preparationName, artifactDigest, msg string) error {
	return SetCondition(ctx, u.client, order, func(latest *deliveryv1alpha1.Order) {
		latest.Status.ObservedGeneration = latest.Generation
		latest.Status.LatestConfigHash = configHash
		latest.Status.LatestPreparationName = preparationName
		latest.Status.LatestArtifactDigest = artifactDigest
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionTrue, "Ready", msg))
	})
}

// Failed marks the Order as failed with the supplied error as the message.
func (u *OrderUpdater) Failed(ctx context.Context, order *deliveryv1alpha1.Order, err error) error {
	return u.set(ctx, order, metav1.ConditionFalse, "ProcessingFailed", "", err.Error())
}

func (u *OrderUpdater) set(ctx context.Context, order *deliveryv1alpha1.Order, condStatus metav1.ConditionStatus, reason, configHash, msg string) error {
	return SetCondition(ctx, u.client, order, func(latest *deliveryv1alpha1.Order) {
		if condStatus != metav1.ConditionUnknown {
			latest.Status.ObservedGeneration = latest.Generation
		}
		latest.Status.LatestConfigHash = configHash
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, condStatus, reason, msg))
	})
}
