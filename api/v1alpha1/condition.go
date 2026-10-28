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

	// ConditionTypeScanned reports on Products whether the images they
	// vendor were scanned for vulnerabilities recently enough.
	ConditionTypeScanned = "Scanned"

	// ConditionTypeChecksPassed reports on Servings whether the target
	// Preparation passes the promotion checks it was rendered under.
	ConditionTypeChecksPassed = "ChecksPassed"

	// ConditionTypeUpstreamDeployed reports on Servings whether the upstream
	// stages of the Preparation to serve deployed its Product revision
	// healthily for their soak time.
	ConditionTypeUpstreamDeployed = "UpstreamDeployed"
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

// Condition reasons used by vulnerability scanning.
const (
	// ReasonScanned reports on a Product that every vendored image in use
	// was scanned within the Kitchen's interval.
	ReasonScanned = "Scanned"
	// ReasonScanFailed reports on a Product that images could not be
	// scanned, or the results not be recorded.
	ReasonScanFailed = "ScanFailed"
	// ReasonStale reports on a Product that the scan of an image in use is
	// older than twice the Kitchen's interval.
	ReasonStale = "Stale"
	// ReasonDatabaseStale reports on a Product that its images were scanned
	// with a vulnerability database older than the Kitchen allows.
	ReasonDatabaseStale = "DatabaseStale"
	// ReasonScannerUnavailable reports on a Product that the scanner could
	// not be reached.
	ReasonScannerUnavailable = "ScannerUnavailable"

	// ReasonPassed reports on a Serving that the target Preparation passes
	// its promotion checks.
	ReasonPassed = "Passed"
	// ReasonVulnerabilitiesFound reports on a Serving that an image of the
	// target Preparation has findings at or above the allowed severity.
	ReasonVulnerabilitiesFound = "VulnerabilitiesFound"
	// ReasonScanMissing reports on a Serving that an image of the target
	// Preparation has no verified scan: it is not vendored, or not scanned
	// yet.
	ReasonScanMissing = "ScanMissing"
	// ReasonScanStale reports on a Serving that the scan of an image of the
	// target Preparation is older than the check allows.
	ReasonScanStale = "ScanStale"

	// ReasonVulnerabilitiesIncreased reports in a Warning Event on a Serving
	// that the deployed Preparation has more findings at or above the
	// severity its check allows than before.
	ReasonVulnerabilitiesIncreased = "VulnerabilitiesIncreased"
)

// Condition reasons used by promotion pipelines.
const (
	// ReasonSatisfied reports on a Serving that every upstream stage
	// deployed the Product revision of the Preparation to serve for its
	// soak time, or that the Serving promoted the Preparation before.
	ReasonSatisfied = "Satisfied"
	// ReasonNotDeployedUpstream reports on a Serving that an upstream stage
	// has not deployed the Product revision of the Preparation to serve
	// healthily, or does not exist.
	ReasonNotDeployedUpstream = "NotDeployedUpstream"
	// ReasonSoaking reports on a Serving that an upstream stage runs the
	// Product revision of the Preparation to serve, but not for its soak
	// time yet.
	ReasonSoaking = "Soaking"
	// ReasonInvalidPipeline reports on an Order or Serving that an upstream
	// stage is a variant of another Product, or that the stages form a
	// cycle.
	ReasonInvalidPipeline = "InvalidPipeline"
)

// Condition reasons used by Servings for where they deploy.
const (
	// ReasonArgoCDNotInstalled reports on a Serving or an Environment that
	// Argo CD's CRDs are not installed, so that nothing can be deployed
	// through Argo CD.
	ReasonArgoCDNotInstalled = "ArgoCDNotInstalled"
)

// Condition reasons used by Registries.
const (
	// ReasonRegistryConnected reports on a Registry that the registry
	// accepted its credentials and TLS settings.
	ReasonRegistryConnected = "Connected"
	// ReasonSecretNotFound reports on a Registry or a Cluster that a Secret
	// it names does not exist.
	ReasonSecretNotFound = "SecretNotFound"
	// ReasonSecretInvalid reports on a Registry that its credentials Secret
	// is not a kubernetes.io/dockerconfigjson Secret with an entry for its
	// url, and on a Cluster that its Secret holds no self-contained
	// kubeconfig under its key.
	ReasonSecretInvalid = "SecretInvalid"
	// ReasonCertificateInvalid reports on a Registry that its TLS Secret
	// holds no usable certificate.
	ReasonCertificateInvalid = "CertificateInvalid"
	// ReasonUnauthorized reports on a Registry that the registry rejected
	// its credentials, or that it requires credentials the Registry lacks,
	// and on a Cluster that its API server rejected the credential.
	ReasonUnauthorized = "Unauthorized"
	// ReasonUnreachable reports on a Registry or a Cluster that the
	// registry or API server could not be reached, for example because of
	// DNS, TLS or a timeout, or that the API server is not ready.
	ReasonUnreachable = "Unreachable"
	// ReasonCredentialsRequired reports on a Product or an Order that
	// content read with credentials would be copied to a location Kokumi
	// accesses without credentials, where anyone could read it.
	ReasonCredentialsRequired = "CredentialsRequired"
)

// Condition reasons used by Repositories, and by Products and Orders for
// the Registries they list.
const (
	// ReasonRegistryNotFound reports that a Registry named in registryRef
	// or registryRefs does not exist.
	ReasonRegistryNotFound = "RegistryNotFound"
	// ReasonRegistryNotCovered reports on a Repository that its registryRef
	// names a Registry whose url does not cover the repository.
	ReasonRegistryNotCovered = "NotCovered"
	// ReasonRepositoryListed reports on a Repository that it is ready to be
	// browsed.
	ReasonRepositoryListed = "Listed"
)

// Condition reasons used by Environments.
const (
	// ReasonNoClusters reports on an Environment that it selects no Cluster.
	ReasonNoClusters = "NoClusters"
	// ReasonProjectNotFound reports on an Environment that its Argo CD
	// AppProject does not exist.
	ReasonProjectNotFound = "ProjectNotFound"
	// ReasonProjectNotPermitted reports on an Environment outside Kokumi's
	// namespace that its Argo CD AppProject does not admit the
	// Environment's namespace in its sourceNamespaces.
	ReasonProjectNotPermitted = "ProjectNotPermitted"
	// ReasonInvalidSelector reports on an Environment that its
	// clusterSelector is not a valid label selector.
	ReasonInvalidSelector = "InvalidSelector"
)
