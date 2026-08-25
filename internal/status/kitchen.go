package status

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// KitchenUpdater updates the status of a Kitchen object.
type KitchenUpdater struct {
	client client.Client
}

// NewKitchenUpdater returns a KitchenUpdater backed by the given client.
func NewKitchenUpdater(c client.Client) *KitchenUpdater {
	return &KitchenUpdater{client: c}
}

// Ready marks the Kitchen as valid and available for use.
func (u *KitchenUpdater) Ready(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen, msg string) error {
	return u.set(ctx, kitchen, metav1.ConditionTrue, "Ready", msg)
}

// Failed marks the Kitchen as having a configuration error.
func (u *KitchenUpdater) Failed(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen, err error) error {
	return u.set(ctx, kitchen, metav1.ConditionFalse, "Failed", err.Error())
}

func (u *KitchenUpdater) set(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen, condStatus metav1.ConditionStatus, reason, msg string) error {
	return SetCondition(ctx, u.client, kitchen, func(latest *deliveryv1alpha1.Kitchen) {
		latest.Status.ObservedGeneration = latest.Generation
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, condStatus, reason, msg))
	})
}
