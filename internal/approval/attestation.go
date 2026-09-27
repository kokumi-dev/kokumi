package approval

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/types"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

const (
	// AttestationArtifactType is the OCI artifact type of the sealed approvals
	// attestation, pushed as a referrer of the Preparation artifact.
	AttestationArtifactType = "application/vnd.kokumi.approvals.v1+json"

	// AttestationPredicateType identifies the kokumi approval predicate.
	AttestationPredicateType = "https://kokumi.dev/attestations/approval/v1"
)

// Predicate records the approval policy, the result, and every vote that
// existed when the approvals were sealed.
type Predicate struct {
	Order       string                          `json:"order"`
	Preparation PreparationInfo                 `json:"preparation"`
	Policy      deliveryv1alpha1.ApprovalPolicy `json:"policy"`
	Result      string                          `json:"result"`
	SealedTime  string                          `json:"sealedTime"`
	Submissions []Submission                    `json:"submissions"`
}

// PreparationInfo identifies the Preparation object.
type PreparationInfo struct {
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	UID       types.UID `json:"uid"`
}

// Submission is a single Approval as recorded in the attestation.
type Submission struct {
	Name          string                            `json:"name"`
	UID           types.UID                         `json:"uid"`
	Approver      deliveryv1alpha1.Approver         `json:"approver"`
	Decision      deliveryv1alpha1.ApprovalDecision `json:"decision"`
	Comment       string                            `json:"comment,omitempty"`
	SubmittedTime string                            `json:"submittedTime"`
	CreationTime  string                            `json:"creationTime"`
	Counted       bool                              `json:"counted"`
	Reason        string                            `json:"reason"`
}

// Archive records the sealed approvals of prep as an OCI referrer of its
// artifact and returns the attestation reference. Retries are idempotent.
func Archive(ctx context.Context, c oci.Client, prep *deliveryv1alpha1.Preparation, approvals []deliveryv1alpha1.Approval) (deliveryv1alpha1.ApprovalAttestation, error) {
	statement, err := buildAttestation(prep, approvals)
	if err != nil {
		return deliveryv1alpha1.ApprovalAttestation{}, err
	}
	subject, err := oci.Parse(prep.Spec.Artifact.OCIRef)
	if err != nil {
		return deliveryv1alpha1.ApprovalAttestation{}, fmt.Errorf("parsing Preparation artifact reference: %w", err)
	}
	digest, err := attestation.Push(ctx, c, subject, AttestationArtifactType, statement, prep.Status.Approval.SealedTime.Time)
	if err != nil {
		return deliveryv1alpha1.ApprovalAttestation{}, err
	}
	return deliveryv1alpha1.ApprovalAttestation{
		OCIRef: fmt.Sprintf("oci://%s@%s", subject.RepositoryReference(), digest),
		Digest: digest,
	}, nil
}

// buildAttestation returns the deterministic in-toto statement for the sealed
// approvals of prep, so retries produce the same OCI digest. Votes submitted
// after the seal are excluded.
func buildAttestation(prep *deliveryv1alpha1.Preparation, approvals []deliveryv1alpha1.Approval) ([]byte, error) {
	if prep.Spec.ApprovalPolicy == nil {
		return nil, errors.New("preparation has no approval policy")
	}
	if !IsSealed(prep) {
		return nil, errors.New("preparation approvals are not sealed")
	}

	res := Evaluate(prep, approvals)

	sorted := make([]*deliveryv1alpha1.Approval, 0, len(approvals))
	for i := range approvals {
		sorted = append(sorted, &approvals[i])
	}
	slices.SortFunc(sorted, compareApprovals)

	submissions := make([]Submission, 0, len(sorted))
	for _, a := range sorted {
		v := res.Verdicts[a.Name]
		if v.Reason == deliveryv1alpha1.ReasonSubmittedAfterSeal || v.Reason == deliveryv1alpha1.ReasonDigestMismatch {
			continue
		}
		submissions = append(submissions, Submission{
			Name:          a.Name,
			UID:           a.UID,
			Approver:      a.Spec.Approver,
			Decision:      a.Spec.Decision,
			Comment:       a.Spec.Comment,
			SubmittedTime: a.Spec.SubmittedTime.UTC().Format(time.RFC3339Nano),
			CreationTime:  a.CreationTimestamp.UTC().Format(time.RFC3339),
			Counted:       v.Counted,
			Reason:        v.Reason,
		})
	}

	return attestation.NewStatement(prep.Spec.Artifact.OCIRef, prep.Spec.Artifact.Digest, AttestationPredicateType, Predicate{
		Order: prep.Spec.OrderName,
		Preparation: PreparationInfo{
			Namespace: prep.Namespace,
			Name:      prep.Name,
			UID:       prep.UID,
		},
		Policy:      *prep.Spec.ApprovalPolicy,
		Result:      res.Reason,
		SealedTime:  prep.Status.Approval.SealedTime.UTC().Format(time.RFC3339),
		Submissions: submissions,
	})
}
