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
	// access, when set, is recorded with every write.
	access *[]deliveryv1alpha1.RegistryAccess
}

// NewOrderUpdater returns an OrderUpdater backed by the given client.
func NewOrderUpdater(c client.Client) *OrderUpdater {
	return &OrderUpdater{client: c}
}

// Processing marks the Order as actively being processed.
func (u *OrderUpdater) Processing(ctx context.Context, order *deliveryv1alpha1.Order, configHash string) error {
	return u.set(ctx, order, func(latest *deliveryv1alpha1.Order) {
		latest.Status.LatestConfigHash = configHash
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionUnknown, "Processing", "Processing component configuration"))
	})
}

// WithRegistryAccess returns an updater that records access, the registry
// locations the Order needs, with every write, so that a reconcile writes
// its status once.
func (u *OrderUpdater) WithRegistryAccess(access []deliveryv1alpha1.RegistryAccess) *OrderUpdater {
	return &OrderUpdater{client: u.client, access: &access}
}

// Ready marks the Order as successfully reconciled.
func (u *OrderUpdater) Ready(ctx context.Context, order *deliveryv1alpha1.Order, configHash, preparationName, artifactDigest, msg string) error {
	return u.set(ctx, order, func(latest *deliveryv1alpha1.Order) {
		latest.Status.ObservedGeneration = latest.Generation
		latest.Status.LatestConfigHash = configHash
		latest.Status.LatestPreparationName = preparationName
		latest.Status.LatestArtifactDigest = artifactDigest
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionTrue, "Ready", msg))
	})
}

// Waiting marks the Order as blocked on its Product, with reason
// ReasonProductNotFound, ReasonProductNotReady, or ReasonProductDeleting.
// The latest config hash is
// kept, so an unchanged intent does not render again once the Product is
// back.
func (u *OrderUpdater) Waiting(ctx context.Context, order *deliveryv1alpha1.Order, reason, msg string) error {
	return u.set(ctx, order, func(latest *deliveryv1alpha1.Order) {
		latest.Status.ObservedGeneration = latest.Generation
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionFalse, reason, msg))
	})
}

// Failed marks the Order as failed with the supplied error as the message.
func (u *OrderUpdater) Failed(ctx context.Context, order *deliveryv1alpha1.Order, err error) error {
	return u.set(ctx, order, func(latest *deliveryv1alpha1.Order) {
		latest.Status.LatestConfigHash = ""
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionFalse, "ProcessingFailed", err.Error()))
	})
}

// Blocked marks the Order as not ready because of its registry setup, for
// reason, such as RegistryNotFound or CredentialsRequired. Like Failed it
// clears the latest config hash, so the Order renders again once the setup
// is fixed.
func (u *OrderUpdater) Blocked(ctx context.Context, order *deliveryv1alpha1.Order, reason, msg string) error {
	return u.set(ctx, order, func(latest *deliveryv1alpha1.Order) {
		latest.Status.ObservedGeneration = latest.Generation
		latest.Status.LatestConfigHash = ""
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionFalse, reason, msg))
	})
}

// Observed records that the Order's generation was reconciled without
// rendering, together with the registry locations of WithRegistryAccess.
func (u *OrderUpdater) Observed(ctx context.Context, order *deliveryv1alpha1.Order) error {
	return u.set(ctx, order, func(latest *deliveryv1alpha1.Order) {
		latest.Status.ObservedGeneration = latest.Generation
	})
}

func (u *OrderUpdater) set(ctx context.Context, order *deliveryv1alpha1.Order, mutate func(*deliveryv1alpha1.Order)) error {
	return setCondition(ctx, u.client, order, func(latest *deliveryv1alpha1.Order) {
		mutate(latest)
		if u.access != nil {
			latest.Status.RegistryAccess = *u.access
		}
	})
}
