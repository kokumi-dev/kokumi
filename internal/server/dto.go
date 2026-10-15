package server

import (
	"encoding/json"
	"time"
)

// OCISourceDTO is the data-transfer representation of an OCISource.
type OCISourceDTO struct {
	OCI       string        `json:"oci,omitempty"`
	PantryRef *PantryRefDTO `json:"pantryRef,omitempty"`
	Version   string        `json:"version"`
}

// OCIDestinationDTO is the data-transfer representation of an OCIDestination.
type OCIDestinationDTO struct {
	OCI       string        `json:"oci,omitempty"`
	PantryRef *PantryRefDTO `json:"pantryRef,omitempty"`
}

// PantryRefDTO is the data-transfer representation of a PantryRef
// embedded in an Order source or destination.
type PantryRefDTO struct {
	Name string `json:"name"`
}

// SourceLinkDTO is a clickable link back to the base artifact's source SCM
// (commit or tag), derived from the git provenance annotations.
type SourceLinkDTO struct {
	URL   string `json:"url"`
	Label string `json:"label"`
}

// GitSourceDTO is the data-transfer representation of the SCM provenance of the
// base artifact that produced a Preparation.
type GitSourceDTO struct {
	Repo       string         `json:"repo,omitempty"`
	Tag        string         `json:"tag,omitempty"`
	CommitHash string         `json:"commitHash,omitempty"`
	SourceLink *SourceLinkDTO `json:"sourceLink,omitempty"`
}

// PatchTargetDTO is the data-transfer representation of a PatchTarget.
type PatchTargetDTO struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// PatchDTO is the data-transfer representation of a Patch.
type PatchDTO struct {
	Target PatchTargetDTO    `json:"target"`
	Set    map[string]string `json:"set"`
}

// ConditionDTO is the data-transfer representation of a metav1.Condition.
type ConditionDTO struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// HelmRenderDTO is the data-transfer representation of a HelmRender.
type HelmRenderDTO struct {
	ReleaseName string          `json:"releaseName,omitempty"`
	Namespace   string          `json:"namespace,omitempty"`
	IncludeCRDs bool            `json:"includeCRDs,omitempty"`
	Values      json.RawMessage `json:"values,omitempty"`
}

// ManifestRenderDTO is the data-transfer representation of a ManifestRender.
type ManifestRenderDTO struct {
	Layout string `json:"layout,omitempty"`
}

// RenderDTO is the data-transfer representation of a Render.
type RenderDTO struct {
	Helm     *HelmRenderDTO     `json:"helm,omitempty"`
	Manifest *ManifestRenderDTO `json:"manifest,omitempty"`
}

// ProductRefDTO is the data-transfer representation of a ProductRef.
type ProductRefDTO struct {
	Name string `json:"name"`
}

// OrderDTO is the enriched view of a Order served to the UI.
// ActivePreparation is derived from the linked Serving's status.observedPreparation.
type OrderDTO struct {
	Name                 string             `json:"name"`
	Namespace            string             `json:"namespace"`
	Labels               map[string]string  `json:"labels,omitempty"`
	ProductRef           ProductRefDTO      `json:"productRef"`
	Values               json.RawMessage    `json:"values,omitempty"`
	Destination          OCIDestinationDTO  `json:"destination"`
	EffectiveDestination string             `json:"effectiveDestination,omitempty"`
	Patches              []PatchDTO         `json:"patches,omitempty"`
	Edits                []PatchDTO         `json:"edits,omitempty"`
	Mode                 string             `json:"mode"`
	Approvals            *ApprovalPolicyDTO `json:"approvals,omitempty"`
	State                string             `json:"state"`
	LatestRevision       string             `json:"latestRevision,omitempty"`
	ActivePreparation    string             `json:"activePreparation,omitempty"`
	Conditions           []ConditionDTO     `json:"conditions,omitempty"`
	CreatedAt            *time.Time         `json:"createdAt,omitempty"`
}

// ApprovalPolicyDTO is the data-transfer representation of an ApprovalPolicy.
type ApprovalPolicyDTO struct {
	RequiredApprovals int32    `json:"requiredApprovals"`
	AllowedGroups     []string `json:"allowedGroups"`
}

// ApprovalVoteDTO is the latest vote of one approver on a Preparation.
type ApprovalVoteDTO struct {
	ApprovalName string    `json:"approvalName"`
	Username     string    `json:"username,omitempty"`
	Subject      string    `json:"subject"`
	Decision     string    `json:"decision"`
	Result       string    `json:"result"`
	SubmittedAt  time.Time `json:"submittedAt"`
}

// PreparationApprovalDTO summarizes the approval gate of a Preparation.
type PreparationApprovalDTO struct {
	Policy            ApprovalPolicyDTO `json:"policy"`
	Approved          bool              `json:"approved"`
	State             string            `json:"state"`
	Message           string            `json:"message,omitempty"`
	RequiredApprovals int32             `json:"requiredApprovals"`
	ApprovedCount     int32             `json:"approvedCount"`
	RejectedCount     int32             `json:"rejectedCount"`
	IneligibleCount   int32             `json:"ineligibleCount"`
	SubmissionCount   int32             `json:"submissionCount"`
	Votes             []ApprovalVoteDTO `json:"votes,omitempty"`
	SealedAt          *time.Time        `json:"sealedAt,omitempty"`
	Attestation       string            `json:"attestation,omitempty"`
}

// ApproverDTO identifies who submitted an Approval.
type ApproverDTO struct {
	Issuer   string   `json:"issuer"`
	Subject  string   `json:"subject"`
	Username string   `json:"username,omitempty"`
	Email    string   `json:"email,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

// ApprovalDTO is the view of a single Approval (vote) served to the UI.
type ApprovalDTO struct {
	Name           string      `json:"name"`
	Namespace      string      `json:"namespace"`
	Order          string      `json:"order"`
	Preparation    string      `json:"preparation"`
	ArtifactDigest string      `json:"artifactDigest"`
	Approver       ApproverDTO `json:"approver"`
	Decision       string      `json:"decision"`
	Comment        string      `json:"comment,omitempty"`
	SubmittedAt    time.Time   `json:"submittedAt"`
	Counted        bool        `json:"counted"`
	Reason         string      `json:"reason,omitempty"`
	Message        string      `json:"message,omitempty"`
}

// SubmitApprovalRequest is the body for POST /api/v1/preparations/{namespace}/{name}/approvals.
type SubmitApprovalRequest struct {
	Decision string `json:"decision"`
	Comment  string `json:"comment,omitempty"`
}

// ArtifactDTO is the data-transfer representation of an Artifact.
type ArtifactDTO struct {
	OCIRef string `json:"ociRef"`
	Digest string `json:"digest"`
	Signed bool   `json:"signed"`
}

// PreparationDTO is the enriched view of a Preparation served to the UI.
// IsActive is true when this Preparation is the one currently deployed by the
// Order's linked Serving (status.observedPreparation).
type PreparationDTO struct {
	Name          string         `json:"name"`
	Namespace     string         `json:"namespace"`
	Order         string         `json:"order"`
	Artifact      ArtifactDTO    `json:"artifact"`
	ConfigHash    string         `json:"configHash"`
	State         string         `json:"state"`
	CreatedAt     *time.Time     `json:"createdAt,omitempty"`
	IsActive      bool           `json:"isActive"`
	CommitMessage string         `json:"commitMessage,omitempty"`
	ParentDigest  string         `json:"parentDigest,omitempty"`
	GitSource     GitSourceDTO   `json:"gitSource,omitempty"`
	Renderer      RendererDTO    `json:"renderer"`
	Conditions    []ConditionDTO `json:"conditions,omitempty"`
	// Approval is set when the Preparation has an approval policy.
	Approval *PreparationApprovalDTO `json:"approval,omitempty"`
}

// RendererDTO identifies what rendered a Preparation.
type RendererDTO struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	Builder string `json:"builder"`
}

// AttestationDTO is one OCI referrer of a Preparation's artifact, as listed
// live from the registry.
type AttestationDTO struct {
	// ArtifactType is the artifactType of the referrer.
	ArtifactType string `json:"artifactType"`
	Digest       string `json:"digest"`
	// CreatedAt is the referrer's org.opencontainers.image.created
	// annotation; empty when the referrer carries none.
	CreatedAt string `json:"createdAt,omitempty"`
	Size      int64  `json:"size"`
	// Recorded reports whether Kokumi recorded this referrer in the
	// Preparation's status. Anyone who can push to the repository can attach
	// a referrer; only recorded ones were written by the controller.
	Recorded bool `json:"recorded"`
	// MediaType and Payload are only set for recorded referrers, and only
	// when the payload was requested.
	MediaType string          `json:"mediaType,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// CreateOrderRequest is the body for POST /api/v1/orders.
type CreateOrderRequest struct {
	Namespace     string             `json:"namespace"`
	Name          string             `json:"name"`
	ProductRef    ProductRefDTO      `json:"productRef"`
	Values        json.RawMessage    `json:"values,omitempty"`
	Destination   *OCIDestinationDTO `json:"destination,omitempty"`
	Patches       []PatchDTO         `json:"patches,omitempty"`
	Edits         []PatchDTO         `json:"edits,omitempty"`
	Mode          string             `json:"mode"`
	Approvals     *ApprovalPolicyDTO `json:"approvals,omitempty"`
	CommitMessage *string            `json:"commitMessage,omitempty"`
}

// UpdateOrderRequest is the body for PUT /api/v1/orders/{namespace}/{name}.
// The Product of an Order cannot be changed.
type UpdateOrderRequest struct {
	Values        json.RawMessage    `json:"values,omitempty"`
	Destination   *OCIDestinationDTO `json:"destination,omitempty"`
	Patches       []PatchDTO         `json:"patches,omitempty"`
	Edits         []PatchDTO         `json:"edits,omitempty"`
	Mode          string             `json:"mode"`
	Approvals     *ApprovalPolicyDTO `json:"approvals,omitempty"`
	CommitMessage *string            `json:"commitMessage,omitempty"`
}

// PromoteRequest is the body for POST /api/v1/orders/{namespace}/{name}/promote.
type PromoteRequest struct {
	Preparation string `json:"preparation"`
}

// ServingDTO is the enriched view of a Serving served to the UI.
type ServingDTO struct {
	Name                string         `json:"name"`
	Namespace           string         `json:"namespace"`
	Order               string         `json:"order"`
	DesiredPreparation  string         `json:"desiredPreparation,omitempty"`
	TargetPreparation   string         `json:"targetPreparation,omitempty"`
	ObservedPreparation string         `json:"observedPreparation,omitempty"`
	DeployedDigest      string         `json:"deployedDigest,omitempty"`
	State               string         `json:"state"`
	Conditions          []ConditionDTO `json:"conditions,omitempty"`
	CreatedAt           *time.Time     `json:"createdAt,omitempty"`
}

// --- Product DTOs ---

// AllowedPatchTargetDTO is the data-transfer representation of an AllowedPatchTarget.
type AllowedPatchTargetDTO struct {
	Target PatchTargetDTO `json:"target"`
	Paths  []string       `json:"paths"`
}

// ValueOverridesDTO is the data-transfer representation of ValueOverrides.
type ValueOverridesDTO struct {
	Policy string   `json:"policy"`
	Paths  []string `json:"paths,omitempty"`
}

// PatchOverridesDTO is the data-transfer representation of PatchOverrides.
type PatchOverridesDTO struct {
	Policy  string                  `json:"policy"`
	Targets []AllowedPatchTargetDTO `json:"targets,omitempty"`
}

// OverridesDTO is the data-transfer representation of Overrides. An empty
// policy in a request defaults to None.
type OverridesDTO struct {
	Values  ValueOverridesDTO `json:"values"`
	Patches PatchOverridesDTO `json:"patches"`
}

// ProductSourceStatusDTO is the source a Product's Orders render from.
type ProductSourceStatusDTO struct {
	OCI       string        `json:"oci"`
	Version   string        `json:"version"`
	PantryRef *PantryRefDTO `json:"pantryRef,omitempty"`
	Digest    string        `json:"digest,omitempty"`
}

// ProductDTO is the view of a Product served to the UI.
type ProductDTO struct {
	Name      string             `json:"name"`
	Namespace string             `json:"namespace"`
	Source    OCISourceDTO       `json:"source"`
	Vendor    *OCIDestinationDTO `json:"vendor,omitempty"`
	Render    *RenderDTO         `json:"render,omitempty"`
	Patches   []PatchDTO         `json:"patches,omitempty"`
	Overrides OverridesDTO       `json:"overrides"`
	// ResolvedSource is status.source; absent until the Product is resolved.
	ResolvedSource *ProductSourceStatusDTO `json:"resolvedSource,omitempty"`
	State          string                  `json:"state,omitempty"`
	Conditions     []ConditionDTO          `json:"conditions,omitempty"`
	CreatedAt      *time.Time              `json:"createdAt,omitempty"`
	// DeletionTimestamp is set while a deleted Product waits for its Orders
	// to be deleted.
	DeletionTimestamp *time.Time `json:"deletionTimestamp,omitempty"`
}

// CreateProductRequest is the body for POST /api/v1/products.
type CreateProductRequest struct {
	Name      string             `json:"name"`
	Namespace string             `json:"namespace"`
	Source    OCISourceDTO       `json:"source"`
	Vendor    *OCIDestinationDTO `json:"vendor,omitempty"`
	Render    *RenderDTO         `json:"render,omitempty"`
	Patches   []PatchDTO         `json:"patches,omitempty"`
	Overrides OverridesDTO       `json:"overrides"`
}

// UpdateProductRequest is the body for PUT /api/v1/products/{namespace}/{name}.
type UpdateProductRequest struct {
	Source    OCISourceDTO       `json:"source"`
	Vendor    *OCIDestinationDTO `json:"vendor,omitempty"`
	Render    *RenderDTO         `json:"render,omitempty"`
	Patches   []PatchDTO         `json:"patches,omitempty"`
	Overrides OverridesDTO       `json:"overrides"`
}

// --- Pantry DTOs ---

// PantryDTO is the view of a Pantry served to the UI.
type PantryDTO struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	URL         string `json:"url"`
	SecretRef   string `json:"secretRef,omitempty"`
	Description string `json:"description,omitempty"`
	State       string `json:"state"`
	// ReferrersAPI is "Supported" or "Unsupported"; empty while unknown.
	ReferrersAPI string         `json:"referrersAPI,omitempty"`
	Conditions   []ConditionDTO `json:"conditions,omitempty"`
	CreatedAt    *time.Time     `json:"createdAt,omitempty"`
}

// CreatePantryRequest is the body for POST /api/v1/pantries.
type CreatePantryRequest struct {
	Namespace   string `json:"namespace,omitempty"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	SecretRef   string `json:"secretRef,omitempty"`
}

// UpdatePantryRequest is the body for PUT /api/v1/pantries/{name}.
type UpdatePantryRequest struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	SecretRef   string `json:"secretRef,omitempty"`
}

// ArtifactFileDTO is a single file of a raw manifest bundle artifact.
type ArtifactFileDTO struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ArtifactInfoDTO contains metadata about an OCI artifact at a specific tag.
type ArtifactInfoDTO struct {
	IsHelm     bool              `json:"isHelm"`
	IsManifest bool              `json:"isManifest"`
	Digest     string            `json:"digest,omitempty"`
	Manifest   string            `json:"manifest,omitempty"`
	Files      []ArtifactFileDTO `json:"files,omitempty"`
	ChartInfo  *ChartInfoDTO     `json:"chartInfo,omitempty"`
}

// ChartInfoDTO contains Helm chart metadata extracted from a chart artifact.
type ChartInfoDTO struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	AppVersion  string `json:"appVersion,omitempty"`
	Description string `json:"description,omitempty"`
	// DefaultValues is the verbatim contents of the chart's values.yaml,
	// preserving the original key order and comments as authored.
	DefaultValues string `json:"defaultValues,omitempty"`
	// Readme is the contents of README.md, empty when the chart has none.
	Readme string `json:"readme,omitempty"`
	// HasSchema reports whether the chart ships a values JSON schema.
	HasSchema bool `json:"hasSchema,omitempty"`
}
