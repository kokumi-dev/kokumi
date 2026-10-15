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

	// ConditionTypeProvenanceAttached reports on Preparations whether the
	// provenance attestation was recorded in the OCI registry.
	ConditionTypeProvenanceAttached = "ProvenanceAttached"

	// ConditionTypeCounted reports on Approvals whether the vote counts.
	ConditionTypeCounted = "Counted"
)

// Condition reasons used by Products and the Orders that reference them.
const (
	// ReasonProductNotFound reports on an Order that its Product does not exist.
	ReasonProductNotFound = "ProductNotFound"
	// ReasonProductNotReady reports on an Order that its Product has not
	// resolved its source yet.
	ReasonProductNotReady = "ProductNotReady"
	// ReasonProductDeleting reports on an Order that its Product is being
	// deleted.
	ReasonProductDeleting = "ProductDeleting"
	// ReasonInUse reports on a deleted Product that Orders still reference it.
	ReasonInUse = "InUse"
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

	ReasonProvenanceAttached     = "ProvenanceAttached"
	ReasonProvenanceAttachFailed = "ProvenanceAttachFailed"
	ReasonAwaitingProvenance     = "AwaitingProvenance"

	ReasonCounted             = "Counted"
	ReasonSuperseded          = "Superseded"
	ReasonNotEligible         = "NotEligible"
	ReasonDigestMismatch      = "DigestMismatch"
	ReasonPreparationNotFound = "PreparationNotFound"
	ReasonSubmittedAfterSeal  = "SubmittedAfterSeal"
)
