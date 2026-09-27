package approval

import (
	"encoding/json"
	"fmt"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// GateResult is the approval gate decision for serving a Preparation, shaped
// as the Serving's Approved condition.
type GateResult struct {
	// Blocked is true when the Preparation must not be deployed.
	Blocked bool
	Status  metav1.ConditionStatus
	Reason  string
	Message string
}

// Gate decides whether prep may be deployed: it must satisfy its approval
// policy and, when a policy applies, its votes must be sealed and archived.
func Gate(prep *deliveryv1alpha1.Preparation, approvals []deliveryv1alpha1.Approval) GateResult {
	res := Evaluate(prep, approvals)
	switch {
	case !res.Approved:
		return GateResult{Blocked: true, Status: metav1.ConditionFalse, Reason: res.Reason, Message: prep.Name + ": " + res.Message}
	case res.Required && !isArchived(prep):
		return GateResult{Blocked: true, Status: metav1.ConditionFalse, Reason: deliveryv1alpha1.ReasonSealingApprovals,
			Message: prep.Name + ": sealing approvals before deployment"}
	default:
		return GateResult{Status: metav1.ConditionTrue, Reason: res.Reason, Message: prep.Name + ": " + res.Message}
	}
}

// NewSealTime returns the seal time for now, truncated to the serialized
// precision so the attestation built from the stored status is reproducible.
func NewSealTime(now time.Time) metav1.Time {
	return metav1.NewTime(now.UTC().Truncate(time.Second))
}

// PolicyAnnotations returns the OCI manifest annotations recording policy on
// the rendered artifact, or nil when no policy applies.
func PolicyAnnotations(policy *deliveryv1alpha1.ApprovalPolicy) (map[string]string, error) {
	if policy == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return nil, fmt.Errorf("encoding approval policy: %w", err)
	}
	return map[string]string{oci.AnnotationApprovalPolicy: string(encoded)}, nil
}

// isArchived reports whether the sealed votes of prep are recorded in the OCI registry.
func isArchived(prep *deliveryv1alpha1.Preparation) bool {
	return IsSealed(prep) &&
		prep.Status.Approval.Attestation != nil &&
		apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeApprovalsSealed)
}
