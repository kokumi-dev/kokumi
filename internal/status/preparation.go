package status

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
)

// PreparationUpdater updates the status of a Preparation object.
type PreparationUpdater struct {
	client client.Client
}

// NewPreparationUpdater returns a PreparationUpdater backed by the given client.
func NewPreparationUpdater(c client.Client) *PreparationUpdater {
	return &PreparationUpdater{client: c}
}

// Ready marks the Preparation as ready for serving.
func (u *PreparationUpdater) Ready(ctx context.Context, preparation *deliveryv1alpha1.Preparation, msg string) error {
	return u.set(ctx, preparation, func(latest *deliveryv1alpha1.Preparation) {
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionTrue, "Ready", msg))
	})
}

// Failed marks the Preparation as failed.
func (u *PreparationUpdater) Failed(ctx context.Context, preparation *deliveryv1alpha1.Preparation, err error) error {
	return u.set(ctx, preparation, func(latest *deliveryv1alpha1.Preparation) {
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionFalse, "ProcessingFailed", err.Error()))
	})
}

// Pending marks the Preparation as pending.
func (u *PreparationUpdater) Pending(ctx context.Context, preparation *deliveryv1alpha1.Preparation, msg string) error {
	return u.set(ctx, preparation, func(latest *deliveryv1alpha1.Preparation) {
		meta.SetStatusCondition(&latest.Status.Conditions, NewCondition(latest.Generation, metav1.ConditionUnknown, "Pending", msg))
	})
}

func (u *PreparationUpdater) set(ctx context.Context, preparation *deliveryv1alpha1.Preparation, mutate func(*deliveryv1alpha1.Preparation)) error {
	return setCondition(ctx, u.client, preparation, mutate)
}

// Approval writes the aggregated approval status and the Approved condition.
func (u *PreparationUpdater) Approval(ctx context.Context, preparation *deliveryv1alpha1.Preparation, res approval.Result) error {
	return u.commit(ctx, preparation, func(latest *deliveryv1alpha1.Preparation) {
		latest.Status.Approval = nil
		if res.Required {
			latest.Status.Approval = res.Status.DeepCopy()
		}
		condStatus := metav1.ConditionFalse
		if res.Approved {
			condStatus = metav1.ConditionTrue
		}
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeApproved, latest.Generation, condStatus, res.Reason, res.Message))
	})
}

// Seal locks the votes in res. The OCI attestation is recorded by Archived.
func (u *PreparationUpdater) Seal(ctx context.Context, preparation *deliveryv1alpha1.Preparation, res approval.Result, sealedTime metav1.Time) error {
	return u.commit(ctx, preparation, func(latest *deliveryv1alpha1.Preparation) {
		latest.Status.Approval = res.Status.DeepCopy()
		latest.Status.Approval.SealedTime = &sealedTime
		latest.Status.Approval.Attestation = nil
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeApprovalsSealed, latest.Generation,
			metav1.ConditionFalse, deliveryv1alpha1.ReasonArchiving, "Recording the sealed approvals in the OCI registry"))
	})
}

// Archived records the OCI attestation of the sealed votes.
func (u *PreparationUpdater) Archived(ctx context.Context, preparation *deliveryv1alpha1.Preparation, attestation deliveryv1alpha1.ApprovalAttestation) error {
	return u.commit(ctx, preparation, func(latest *deliveryv1alpha1.Preparation) {
		latest.Status.Approval.Attestation = &attestation
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeApprovalsSealed, latest.Generation,
			metav1.ConditionTrue, deliveryv1alpha1.ReasonSealed, "Approvals sealed and recorded at "+attestation.OCIRef))
	})
}

// ArchiveFailed records that the OCI attestation could not be pushed.
func (u *PreparationUpdater) ArchiveFailed(ctx context.Context, preparation *deliveryv1alpha1.Preparation, err error) error {
	return u.commit(ctx, preparation, func(latest *deliveryv1alpha1.Preparation) {
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeApprovalsSealed, latest.Generation,
			metav1.ConditionFalse, deliveryv1alpha1.ReasonArchiveFailed, err.Error()))
	})
}

// commit applies mutate to preparation itself and patches its status with an
// optimistic lock anchored to the generation the reconciler observed, so a
// stale cache read can never overwrite sealed votes.
func (u *PreparationUpdater) commit(ctx context.Context, preparation *deliveryv1alpha1.Preparation, mutate func(*deliveryv1alpha1.Preparation)) error {
	return commitCondition(ctx, u.client, preparation, func(latest *deliveryv1alpha1.Preparation) {
		latest.Status.ObservedGeneration = latest.Generation
		mutate(latest)
	})
}
