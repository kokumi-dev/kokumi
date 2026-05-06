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

// MenuUpdater updates the status of a Menu object.
type MenuUpdater struct {
	client client.Client
}

// NewMenuUpdater returns a MenuUpdater backed by the given client.
func NewMenuUpdater(c client.Client) *MenuUpdater {
	return &MenuUpdater{client: c}
}

// Ready marks the Menu as valid and available for use.
func (u *MenuUpdater) Ready(ctx context.Context, m *deliveryv1alpha1.Menu, msg string) error {
	return u.set(ctx, m, metav1.ConditionTrue, "Ready", msg)
}

// Failed marks the Menu as having a configuration error.
func (u *MenuUpdater) Failed(ctx context.Context, m *deliveryv1alpha1.Menu, err error) error {
	return u.set(ctx, m, metav1.ConditionFalse, "Failed", err.Error())
}

func (u *MenuUpdater) set(
	ctx context.Context,
	menu *deliveryv1alpha1.Menu,
	condStatus metav1.ConditionStatus,
	reason string,
	msg string,
) error {
	menu.Status.ObservedGeneration = menu.Generation

	condition := metav1.Condition{
		Type:               deliveryv1alpha1.ConditionTypeReady,
		Status:             condStatus,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: menu.Generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}

	meta.SetStatusCondition(&menu.Status.Conditions, condition)

	if err := u.client.Status().Update(ctx, menu); err != nil {
		if apierrors.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("failed to update Menu status: %w", err)
	}

	return nil
}
