package v1alpha1

const (
	// ConditionTypeReady is the standard condition type used for all resource types.
	ConditionTypeReady = "Ready"

	// ConditionTypeApproved reports on Preparations and Servings whether the
	// (target) Preparation satisfies its approval policy.
	ConditionTypeApproved = "Approved"

	// ConditionTypeApprovalsSealed reports on Preparations whether the votes
	// were locked and recorded as an OCI attestation.
	ConditionTypeApprovalsSealed = "ApprovalsSealed"

	// ConditionTypeCounted reports on Approvals whether the vote counts.
	ConditionTypeCounted = "Counted"
)

// Condition reasons used by the approval gate.
const (
	ReasonApproved            = "Approved"
	ReasonApprovalNotRequired = "ApprovalNotRequired"
	ReasonAwaitingApprovals   = "AwaitingApprovals"
	ReasonChangesRequested    = "ChangesRequested"
	ReasonSealingApprovals    = "SealingApprovals"

	ReasonSealed        = "Sealed"
	ReasonArchiving     = "Archiving"
	ReasonArchiveFailed = "ArchiveFailed"

	ReasonCounted             = "Counted"
	ReasonSuperseded          = "Superseded"
	ReasonNotEligible         = "NotEligible"
	ReasonDigestMismatch      = "DigestMismatch"
	ReasonPreparationNotFound = "PreparationNotFound"
	ReasonSubmittedAfterSeal  = "SubmittedAfterSeal"
)
