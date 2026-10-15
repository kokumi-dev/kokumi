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
	return u.set(ctx, kitchen, func(latest *deliveryv1alpha1.Kitchen) {
		latest.Status.ObservedGeneration = latest.Generation
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionTrue, "Ready", msg))
	})
}

// Failed marks the Kitchen as having a configuration error.
func (u *KitchenUpdater) Failed(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen, err error) error {
	return u.set(ctx, kitchen, func(latest *deliveryv1alpha1.Kitchen) {
		latest.Status.ObservedGeneration = latest.Generation
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionFalse, "Failed", err.Error()))
	})
}

// SigningKeyReady publishes the public half of the signing key and marks it
// ready.
func (u *KitchenUpdater) SigningKeyReady(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen, signing deliveryv1alpha1.KitchenSigningStatus) error {
	return u.set(ctx, kitchen, func(latest *deliveryv1alpha1.Kitchen) {
		latest.Status.Signing = &signing
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeSigningKeyReady, latest.Generation,
			metav1.ConditionTrue, deliveryv1alpha1.ReasonKeyReady, "Signing key "+signing.KeyID+" loaded"))
	})
}

// SigningKeyFailed records that the signing key cannot be used, for reason.
// The published public key is kept, since artifacts it signed still verify.
func (u *KitchenUpdater) SigningKeyFailed(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen, reason string, err error) error {
	return u.set(ctx, kitchen, func(latest *deliveryv1alpha1.Kitchen) {
		latest.Status.ObservedGeneration = latest.Generation
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeSigningKeyReady, latest.Generation,
			metav1.ConditionFalse, reason, err.Error()))
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionFalse, reason, err.Error()))
	})
}

func (u *KitchenUpdater) set(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen, mutate func(*deliveryv1alpha1.Kitchen)) error {
	return setCondition(ctx, u.client, kitchen, mutate)
}
