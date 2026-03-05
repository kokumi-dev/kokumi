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

// RecipeDTO is the enriched view of a Recipe served to the UI.
// ActivePreparation is derived from the linked Serving's status.observedPreparation.
type RecipeDTO struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	Labels            map[string]string `json:"labels,omitempty"`
	Source            OCISourceDTO      `json:"source"`
	Destination       OCIDestinationDTO `json:"destination"`
	Render            *RenderDTO        `json:"render,omitempty"`
	Patches           []PatchDTO        `json:"patches,omitempty"`
	AutoDeploy        bool              `json:"autoDeploy"`
	Phase             string            `json:"phase"`
	LatestRevision    string            `json:"latestRevision,omitempty"`
	ActivePreparation string            `json:"activePreparation,omitempty"`
	Conditions        []ConditionDTO    `json:"conditions,omitempty"`
	CreatedAt         *time.Time        `json:"createdAt,omitempty"`
}

// ArtifactDTO is the data-transfer representation of an Artifact.
type ArtifactDTO struct {
	OCIRef string `json:"ociRef"`
	Digest string `json:"digest"`
	Signed bool   `json:"signed"`
}

// PreparationDTO is the enriched view of a Preparation served to the UI.
// IsActive is true when this Preparation is the one currently deployed by the
// Recipe's linked Serving (status.observedPreparation).
type PreparationDTO struct {
	Name       string         `json:"name"`
	Namespace  string         `json:"namespace"`
	Recipe     string         `json:"recipe"`
	Artifact   ArtifactDTO    `json:"artifact"`
	ConfigHash string         `json:"configHash"`
	Phase      string         `json:"phase"`
	CreatedAt  *time.Time     `json:"createdAt,omitempty"`
	IsActive   bool           `json:"isActive"`
	Conditions []ConditionDTO `json:"conditions,omitempty"`
}

// CreateRecipeRequest is the body for POST /api/v1/recipes.
type CreateRecipeRequest struct {
	Namespace   string            `json:"namespace"`
	Name        string            `json:"name"`
	Source      OCISourceDTO      `json:"source"`
	Destination OCIDestinationDTO `json:"destination"`
	Render      *RenderDTO        `json:"render,omitempty"`
	Patches     []PatchDTO        `json:"patches,omitempty"`
	AutoDeploy  bool              `json:"autoDeploy"`
}

// UpdateRecipeRequest is the body for PUT /api/v1/recipes/{namespace}/{name}.
type UpdateRecipeRequest struct {
	Source      OCISourceDTO      `json:"source"`
	Destination OCIDestinationDTO `json:"destination"`
	Render      *RenderDTO        `json:"render,omitempty"`
	Patches     []PatchDTO        `json:"patches,omitempty"`
	AutoDeploy  bool              `json:"autoDeploy"`
}

// PromoteRequest is the body for POST /api/v1/recipes/{namespace}/{name}/promote.
type PromoteRequest struct {
	Preparation string `json:"preparation"`
}

// ServingDTO is the enriched view of a Serving served to the UI.
type ServingDTO struct {
	Name                string         `json:"name"`
	Namespace           string         `json:"namespace"`
	Recipe              string         `json:"recipe"`
	DesiredPreparation  string         `json:"desiredPreparation"`
	ObservedPreparation string         `json:"observedPreparation,omitempty"`
	DeployedDigest      string         `json:"deployedDigest,omitempty"`
	PreparationPolicy   string         `json:"preparationPolicy"`
	Phase               string         `json:"phase"`
	Conditions          []ConditionDTO `json:"conditions,omitempty"`
	CreatedAt           *time.Time     `json:"createdAt,omitempty"`
}
