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

	// ConditionTypeSigned reports on Preparations whether the signature of
	// the artifact was recorded in the OCI registry.
	ConditionTypeSigned = "Signed"

	// ConditionTypeVerified reports on Servings whether the signatures and
	// attestations of the target Preparation's artifact verified in the OCI
	// registry.
	ConditionTypeVerified = "Verified"

	// ConditionTypeSourceVerified reports on Products whether the signature
	// of the source verified.
	ConditionTypeSourceVerified = "SourceVerified"

	// ConditionTypeSigningKeyReady reports on the Kitchen whether the signing
	// key is loaded.
	ConditionTypeSigningKeyReady = "SigningKeyReady"

	// ConditionTypeCounted reports on Approvals whether the vote counts.
	ConditionTypeCounted = "Counted"
)

// Ready condition reasons of the objects Kokumi reconciles.
const (
	// ReasonReady reports that an Order, Preparation or the Kitchen is ready.
	ReasonReady = "Ready"
	// ReasonFailed reports on a Product or the Kitchen that it is
	// misconfigured, or that its source could not be resolved.
	ReasonFailed = "Failed"

	// ReasonProcessing reports on an Order that it is rendering its intent.
	ReasonProcessing = "Processing"
	// ReasonProcessingFailed reports on an Order that its intent could not
	// be rendered.
	ReasonProcessingFailed = "ProcessingFailed"

	// ReasonResolved reports on a Product that its source is resolved to a
	// digest.
	ReasonResolved = "Resolved"
	// ReasonVendored reports on a Product that its source is resolved and
	// copied to its vendor location.
	ReasonVendored = "Vendored"

	// ReasonPending reports on a Serving that it waits for its Order or for
	// a Preparation to serve.
	ReasonPending = "Pending"
	// ReasonDeploying reports on a Serving that its deployment has not
	// reached the target Preparation yet.
	ReasonDeploying = "Deploying"
	// ReasonDeployed reports on a Serving that its deployment runs the
	// target Preparation and is healthy.
	ReasonDeployed = "Deployed"
	// ReasonDeploymentFailed reports on a Serving that its deployment could
	// not be updated or is degraded.
	ReasonDeploymentFailed = "DeploymentFailed"
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

// Event reasons used by Orders.
const (
	// ReasonImageNotResolved reports in a Warning Event on an Order that
	// images of its rendered manifests could not be resolved to digests.
	ReasonImageNotResolved = "ImageNotResolved"
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

// Condition reasons used by signing and verification.
const (
	// ReasonSigned reports on a Preparation that its artifact is signed.
	ReasonSigned = "Signed"
	// ReasonSignFailed reports on a Preparation that its artifact could not
	// be signed.
	ReasonSignFailed = "SignFailed"
	// ReasonSigningKeyNotReady reports that the Kitchen signing key is not
	// loaded yet.
	ReasonSigningKeyNotReady = "SigningKeyNotReady"
	// ReasonAwaitingSignature reports on a Preparation that it is not ready
	// until its artifact is signed.
	ReasonAwaitingSignature = "AwaitingSignature"

	// ReasonSignaturesVerified reports on a Serving that the target
	// artifact's signature and attestations verified.
	ReasonSignaturesVerified = "SignaturesVerified"
	// ReasonSignatureMissing reports on a Serving that the target artifact
	// lacks a signature or attestation it must carry.
	ReasonSignatureMissing = "SignatureMissing"
	// ReasonSignatureInvalid reports on a Serving that a signature or
	// attestation of the target artifact did not verify against the trusted
	// keys, or does not attest what it must.
	ReasonSignatureInvalid = "SignatureInvalid"

	// ReasonKeyReady reports on the Kitchen that the signing key is loaded.
	ReasonKeyReady = "KeyReady"
	// ReasonKeyInvalid reports on the Kitchen that the signing key Secret
	// cannot be used.
	ReasonKeyInvalid = "KeyInvalid"
	// ReasonKeyMissing reports on the Kitchen that the signing key Secret
	// does not exist and could not be generated, for example because its
	// namespace is missing.
	ReasonKeyMissing = "KeyMissing"
	// ReasonTrustedKeyInvalid reports on the Kitchen that a key in
	// spec.signing.trustedKeys cannot be parsed. Nothing is signed or
	// verified until it is fixed.
	ReasonTrustedKeyInvalid = "TrustedKeyInvalid"

	// ReasonSucceeded reports on a Product that its source verified.
	ReasonSucceeded = "Succeeded"
	// ReasonVerificationError reports on a Product that its source did not
	// verify.
	ReasonVerificationError = "VerificationError"
	// ReasonVerificationRequired reports on a Product that the Kitchen
	// requires its source to verify, and it does not.
	ReasonVerificationRequired = "VerificationRequired"
)
