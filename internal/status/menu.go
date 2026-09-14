package status

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// MenuUpdater updates the status of a Menu object.
type MenuUpdater struct {
	client client.Client
}

// NewMenuUpdater returns a MenuUpdater backed by the given client.
func NewMenuUpdater(c client.Client) *MenuUpdater {
	return &MenuUpdater{client: c}
}

// Ready marks the Menu as valid and available for use.
// configHash records the spec inputs that produced the published artifact;
// pass an empty string when the source is resolved without rendering.
func (u *MenuUpdater) Ready(ctx context.Context, menu *deliveryv1alpha1.Menu, configHash string, source *deliveryv1alpha1.MenuSourceStatus) error {
	return SetCondition(ctx, u.client, menu, func(latest *deliveryv1alpha1.Menu) {
		latest.Status.ObservedGeneration = latest.Generation
		latest.Status.Source = source
		latest.Status.ConfigHash = configHash
		reason := "Resolved"
		if latest.Spec.Vendor != nil {
			reason = "Vendored"
			if source.PreRendered {
				reason = "Rendered"
			}
		}
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionTrue, reason, "Source published"))
	})
}

// Failed marks the Menu as having a configuration error.
func (u *MenuUpdater) Failed(ctx context.Context, menu *deliveryv1alpha1.Menu, err error) error {
	return u.set(ctx, menu, metav1.ConditionFalse, "Failed", err.Error())
}

func (u *MenuUpdater) set(ctx context.Context, menu *deliveryv1alpha1.Menu, condStatus metav1.ConditionStatus, reason, msg string) error {
	return SetCondition(ctx, u.client, menu, func(latest *deliveryv1alpha1.Menu) {
		latest.Status.ObservedGeneration = latest.Generation
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, condStatus, reason, msg))
	})
}
