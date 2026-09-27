package status

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
)

// ApprovalUpdater updates the status of an Approval object.
type ApprovalUpdater struct {
	client client.Client
}

// NewApprovalUpdater returns an ApprovalUpdater backed by the given client.
func NewApprovalUpdater(c client.Client) *ApprovalUpdater {
	return &ApprovalUpdater{client: c}
}

// Counted records the verdict of the approval gate for the Approval. It is a
// no-op when the status is already up to date.
func (u *ApprovalUpdater) Counted(ctx context.Context, a *deliveryv1alpha1.Approval, v approval.Verdict) error {
	condStatus := metav1.ConditionFalse
	if v.Counted {
		condStatus = metav1.ConditionTrue
	}
	return u.set(ctx, a, func(latest *deliveryv1alpha1.Approval) {
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeCounted, latest.Generation, condStatus, v.Reason, v.Message))
	})
}

// set applies mutate to a itself and patches its status with an optimistic
// lock anchored to the resource version the reconciler observed, so a stale
// cache read can never overwrite a counted verdict.
func (u *ApprovalUpdater) set(ctx context.Context, a *deliveryv1alpha1.Approval, mutate func(*deliveryv1alpha1.Approval)) error {
	return commitCondition(ctx, u.client, a, func(latest *deliveryv1alpha1.Approval) {
		latest.Status.ObservedGeneration = latest.Generation
		mutate(latest)
	})
}
