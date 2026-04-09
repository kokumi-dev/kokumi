package server

import (
	"encoding/json"
	"time"
)

// OCISourceDTO is the data-transfer representation of an OCISource.
type OCISourceDTO struct {
	OCI     string `json:"oci"`
	Version string `json:"version"`
}

// OCIDestinationDTO is the data-transfer representation of an OCIDestination.
type OCIDestinationDTO struct {
	OCI string `json:"oci"`
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

// RenderDTO is the data-transfer representation of a Render.
type RenderDTO struct {
	Helm *HelmRenderDTO `json:"helm,omitempty"`
}

// MenuRefDTO is the data-transfer representation of a MenuRef.
type MenuRefDTO struct {
	Name string `json:"name"`
}

// OrderDTO is the enriched view of a Order served to the UI.
// ActivePreparation is derived from the linked Serving's status.observedPreparation.
type OrderDTO struct {
	Name                 string            `json:"name"`
	Namespace            string            `json:"namespace"`
	Labels               map[string]string `json:"labels,omitempty"`
	Source               *OCISourceDTO     `json:"source,omitempty"`
	MenuRef              *MenuRefDTO       `json:"menuRef,omitempty"`
	Destination          OCIDestinationDTO `json:"destination"`
	EffectiveDestination string            `json:"effectiveDestination,omitempty"`
	Render               *RenderDTO        `json:"render,omitempty"`
	Patches              []PatchDTO        `json:"patches,omitempty"`
	Edits                []PatchDTO        `json:"edits,omitempty"`
	AutoDeploy           bool              `json:"autoDeploy"`
	Phase                string            `json:"phase"`
	LatestRevision       string            `json:"latestRevision,omitempty"`
	ActivePreparation    string            `json:"activePreparation,omitempty"`
	Conditions           []ConditionDTO    `json:"conditions,omitempty"`
	CreatedAt            *time.Time        `json:"createdAt,omitempty"`
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
	Phase         string         `json:"phase"`
	CreatedAt     *time.Time     `json:"createdAt,omitempty"`
	IsActive      bool           `json:"isActive"`
	CommitMessage string         `json:"commitMessage,omitempty"`
	Conditions    []ConditionDTO `json:"conditions,omitempty"`
}

// CreateOrderRequest is the body for POST /api/v1/orders.
type CreateOrderRequest struct {
	Namespace     string             `json:"namespace"`
	Name          string             `json:"name"`
	Source        OCISourceDTO       `json:"source"`
	MenuRef       *MenuRefDTO        `json:"menuRef,omitempty"`
	Destination   *OCIDestinationDTO `json:"destination,omitempty"`
	Render        *RenderDTO         `json:"render,omitempty"`
	Patches       []PatchDTO         `json:"patches,omitempty"`
	Edits         []PatchDTO         `json:"edits,omitempty"`
	AutoDeploy    bool               `json:"autoDeploy"`
	CommitMessage string             `json:"commitMessage,omitempty"`
}

// UpdateOrderRequest is the body for PUT /api/v1/orders/{namespace}/{name}.
type UpdateOrderRequest struct {
	Source        OCISourceDTO       `json:"source"`
	MenuRef       *MenuRefDTO        `json:"menuRef,omitempty"`
	Destination   *OCIDestinationDTO `json:"destination,omitempty"`
	Render        *RenderDTO         `json:"render,omitempty"`
	Patches       []PatchDTO         `json:"patches,omitempty"`
	Edits         []PatchDTO         `json:"edits,omitempty"`
	AutoDeploy    bool               `json:"autoDeploy"`
	CommitMessage string             `json:"commitMessage,omitempty"`
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
	DesiredPreparation  string         `json:"desiredPreparation"`
	ObservedPreparation string         `json:"observedPreparation,omitempty"`
	DeployedDigest      string         `json:"deployedDigest,omitempty"`
	PreparationPolicy   string         `json:"preparationPolicy"`
	Phase               string         `json:"phase"`
	Conditions          []ConditionDTO `json:"conditions,omitempty"`
	CreatedAt           *time.Time     `json:"createdAt,omitempty"`
}

// --- Menu DTOs ---

// ValueOverridePolicyDTO is the data-transfer representation of a ValueOverridePolicy.
type ValueOverridePolicyDTO struct {
	Policy  string   `json:"policy"`
	Allowed []string `json:"allowed,omitempty"`
}

// AllowedPatchTargetDTO is the data-transfer representation of an AllowedPatchTarget.
type AllowedPatchTargetDTO struct {
	Target PatchTargetDTO `json:"target"`
	Paths  []string       `json:"paths"`
}

// PatchOverridePolicyDTO is the data-transfer representation of a PatchOverridePolicy.
type PatchOverridePolicyDTO struct {
	Policy  string                  `json:"policy"`
	Allowed []AllowedPatchTargetDTO `json:"allowed,omitempty"`
}

// OverridePolicyDTO is the data-transfer representation of an OverridePolicy.
type OverridePolicyDTO struct {
	Values  ValueOverridePolicyDTO `json:"values"`
	Patches PatchOverridePolicyDTO `json:"patches"`
}

// MenuDefaultsDTO is the data-transfer representation of MenuDefaults.
type MenuDefaultsDTO struct {
	AutoDeploy bool `json:"autoDeploy"`
}

// MenuDTO is the view of a Menu served to the UI.
type MenuDTO struct {
	Name       string            `json:"name"`
	Source     OCISourceDTO      `json:"source"`
	Render     *RenderDTO        `json:"render,omitempty"`
	Patches    []PatchDTO        `json:"patches,omitempty"`
	Overrides  OverridePolicyDTO `json:"overrides"`
	Defaults   MenuDefaultsDTO   `json:"defaults"`
	Phase      string            `json:"phase,omitempty"`
	Conditions []ConditionDTO    `json:"conditions,omitempty"`
	CreatedAt  *time.Time        `json:"createdAt,omitempty"`
}

// CreateMenuRequest is the body for POST /api/v1/menus.
type CreateMenuRequest struct {
	Name      string            `json:"name"`
	Source    OCISourceDTO      `json:"source"`
	Render    *RenderDTO        `json:"render,omitempty"`
	Patches   []PatchDTO        `json:"patches,omitempty"`
	Overrides OverridePolicyDTO `json:"overrides"`
	Defaults  MenuDefaultsDTO   `json:"defaults"`
}

// UpdateMenuRequest is the body for PUT /api/v1/menus/{name}.
type UpdateMenuRequest struct {
	Source    OCISourceDTO      `json:"source"`
	Render    *RenderDTO        `json:"render,omitempty"`
	Patches   []PatchDTO        `json:"patches,omitempty"`
	Overrides OverridePolicyDTO `json:"overrides"`
	Defaults  MenuDefaultsDTO   `json:"defaults"`
}
