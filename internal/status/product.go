package status

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// ProductUpdater updates the status of a Product object.
type ProductUpdater struct {
	client client.Client
}

// NewProductUpdater returns a ProductUpdater backed by the given client.
func NewProductUpdater(c client.Client) *ProductUpdater {
	return &ProductUpdater{client: c}
}

// Ready marks the Product as resolved, so that its Orders can render from
// source. configHash records the inputs source was resolved from. The
// SourceVerified condition reports source.verified, and is removed when the
// source was not verified.
//
// The status records the generation of product, the object that was
// resolved, not of the object it re-fetches: Orders render only from a
// Product that is Ready for its current generation, so a spec change during
// resolution must leave the Product not ready until it is resolved again.
func (u *ProductUpdater) Ready(ctx context.Context, product *deliveryv1alpha1.Product, configHash string, source *deliveryv1alpha1.ProductSourceStatus) error {
	generation := product.Generation
	return u.set(ctx, product, func(latest *deliveryv1alpha1.Product) {
		latest.Status.ObservedGeneration = generation
		latest.Status.Source = source
		latest.Status.ConfigHash = configHash
		reason, msg := "Resolved", "Source resolved"
		if product.Spec.Vendor != nil {
			reason, msg = "Vendored", "Source vendored"
		}
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(generation, metav1.ConditionTrue, reason, msg))
		if source.Verified == nil {
			meta.RemoveStatusCondition(&latest.Status.Conditions, deliveryv1alpha1.ConditionTypeSourceVerified)
			return
		}
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeSourceVerified, generation,
			metav1.ConditionTrue, deliveryv1alpha1.ReasonSucceeded, "Verified signature of "+VerifiedSigner(source.Verified)))
	})
}

// VerificationFailed marks the Product as not ready because its source did
// not verify, for reason. Orders render nothing until it does. The published
// source is kept, but no longer recorded as verified.
func (u *ProductUpdater) VerificationFailed(ctx context.Context, product *deliveryv1alpha1.Product, reason string, err error) error {
	generation := product.Generation
	return u.set(ctx, product, func(latest *deliveryv1alpha1.Product) {
		latest.Status.ObservedGeneration = generation
		if latest.Status.Source != nil {
			latest.Status.Source.Verified = nil
		}
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeSourceVerified, generation,
			metav1.ConditionFalse, reason, err.Error()))
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(generation, metav1.ConditionFalse, reason, err.Error()))
	})
}

// VerifiedSigner describes the signer verification recorded.
func VerifiedSigner(verified *deliveryv1alpha1.SourceVerification) string {
	if verified.KeyID != "" {
		return "key " + verified.KeyID
	}
	return verified.Subject + " (issuer " + verified.Issuer + ")"
}

// Failed marks the Product as having a configuration error. Like Ready, it
// records the generation of product.
func (u *ProductUpdater) Failed(ctx context.Context, product *deliveryv1alpha1.Product, err error) error {
	generation := product.Generation
	return u.set(ctx, product, func(latest *deliveryv1alpha1.Product) {
		latest.Status.ObservedGeneration = generation
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(generation, metav1.ConditionFalse, "Failed", err.Error()))
	})
}

// NotReady marks the Product as waiting for a prerequisite, for reason. Like
// Ready, it records the generation of product.
func (u *ProductUpdater) NotReady(ctx context.Context, product *deliveryv1alpha1.Product, reason, msg string) error {
	generation := product.Generation
	return u.set(ctx, product, func(latest *deliveryv1alpha1.Product) {
		latest.Status.ObservedGeneration = generation
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(generation, metav1.ConditionFalse, reason, msg))
	})
}

// InUse marks a deleted Product as waiting for the Orders that still
// reference it.
func (u *ProductUpdater) InUse(ctx context.Context, product *deliveryv1alpha1.Product, msg string) error {
	return u.set(ctx, product, func(latest *deliveryv1alpha1.Product) {
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionFalse, deliveryv1alpha1.ReasonInUse, msg))
	})
}

func (u *ProductUpdater) set(ctx context.Context, product *deliveryv1alpha1.Product, mutate func(*deliveryv1alpha1.Product)) error {
	return setCondition(ctx, u.client, product, mutate)
}
