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

// EvidenceAttached records the signature and provenance attestations of the
// artifact and marks the Preparation as ready for serving.
func (u *PreparationUpdater) EvidenceAttached(ctx context.Context, preparation *deliveryv1alpha1.Preparation, signature, provenance deliveryv1alpha1.AttestationReference) error {
	return u.commit(ctx, preparation, func(latest *deliveryv1alpha1.Preparation) {
		setAttestation(&latest.Status.Attestations, signature)
		setAttestation(&latest.Status.Attestations, provenance)
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeSigned, latest.Generation,
			metav1.ConditionTrue, deliveryv1alpha1.ReasonSigned, "Signature recorded at "+signature.OCIRef))
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeProvenanceAttached, latest.Generation,
			metav1.ConditionTrue, deliveryv1alpha1.ReasonProvenanceAttached, "Provenance recorded at "+provenance.OCIRef))
		setEvidenceReady(latest)
	})
}

// SignFailed records that the artifact could not be signed, for reason. The
// Preparation is not ready until it is.
func (u *PreparationUpdater) SignFailed(ctx context.Context, preparation *deliveryv1alpha1.Preparation, reason string, err error) error {
	return u.commit(ctx, preparation, func(latest *deliveryv1alpha1.Preparation) {
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeSigned, latest.Generation,
			metav1.ConditionFalse, reason, err.Error()))
		setEvidenceReady(latest)
	})
}

// ProvenanceFailed records that the provenance attestation could not be
// attached. The Preparation is not ready until it is.
func (u *PreparationUpdater) ProvenanceFailed(ctx context.Context, preparation *deliveryv1alpha1.Preparation, err error) error {
	return u.commit(ctx, preparation, func(latest *deliveryv1alpha1.Preparation) {
		meta.SetStatusCondition(&latest.Status.Conditions, newTypedCondition(deliveryv1alpha1.ConditionTypeProvenanceAttached, latest.Generation,
			metav1.ConditionFalse, deliveryv1alpha1.ReasonProvenanceAttachFailed, err.Error()))
		setEvidenceReady(latest)
	})
}

// setEvidenceReady sets Ready from the evidence conditions: a Preparation is
// ready for serving once its artifact is signed and its provenance attached.
func setEvidenceReady(latest *deliveryv1alpha1.Preparation) {
	var ready metav1.Condition
	switch {
	case !meta.IsStatusConditionTrue(latest.Status.Conditions, deliveryv1alpha1.ConditionTypeSigned):
		ready = NewCondition(latest.Generation, metav1.ConditionFalse, deliveryv1alpha1.ReasonAwaitingSignature, "Waiting for the artifact to be signed")
	case !meta.IsStatusConditionTrue(latest.Status.Conditions, deliveryv1alpha1.ConditionTypeProvenanceAttached):
		ready = NewCondition(latest.Generation, metav1.ConditionFalse, deliveryv1alpha1.ReasonAwaitingProvenance, "Waiting for the provenance attestation to be recorded")
	default:
		ready = NewCondition(latest.Generation, metav1.ConditionTrue, "Ready", "Preparation is ready for serving")
	}
	meta.SetStatusCondition(&latest.Status.Conditions, ready)
}

// setAttestation replaces the entry of the attestation's type, or appends it.
func setAttestation(attestations *[]deliveryv1alpha1.AttestationReference, attestation deliveryv1alpha1.AttestationReference) {
	for i := range *attestations {
		if (*attestations)[i].Type == attestation.Type {
			(*attestations)[i] = attestation
			return
		}
	}
	*attestations = append(*attestations, attestation)
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
