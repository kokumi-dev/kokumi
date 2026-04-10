/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OCISource defines the OCI location of the base manifest artifact
type OCISource struct {
	// oci is the OCI registry URL for the source manifests
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^oci://.*`
	OCI string `json:"oci"`

	// version is the semantic version or tag of the artifact
	// The controller will resolve this to a digest
	// +kubebuilder:validation:Required
	Version string `json:"version"`
}

// HelmRender defines Helm-specific rendering options for the source artifact.
type HelmRender struct {
	// releaseName is the Helm release name passed to helm template.
	// Defaults to the Order's metadata.name when omitted.
	// +optional
	ReleaseName string `json:"releaseName,omitempty"`

	// namespace is the target namespace passed to helm template --namespace.
	// Defaults to the Order's metadata.namespace when omitted.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// includeCRDs controls whether CRDs are included in the rendered output.
	// Equivalent to helm template --include-crds.
	// +optional
	// +kubebuilder:default=false
	IncludeCRDs bool `json:"includeCRDs,omitempty"`

	// values holds inline Helm values merged last (highest priority).
	// Equivalent to a final -f values.yaml pass.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Values *apiextensionsv1.JSON `json:"values,omitempty"`
}

// Render defines optional rendering to apply to the source artifact.
// When absent, the source is treated as a pre-rendered manifest bundle.
type Render struct {
	// helm renders the source OCI artifact as a Helm chart.
	// When set, the source must be a Helm chart in OCI format.
	// +optional
	Helm *HelmRender `json:"helm,omitempty"`
}

// OCIDestination defines where the rendered, configured artifact
// (Preparation) will be pushed as an OCI artifact
type OCIDestination struct {
	// oci is the OCI registry URL where configured manifests will be pushed
	// +optional
	// +kubebuilder:validation:Pattern=`^oci://.*`
	OCI string `json:"oci,omitempty"`
}

// PatchTarget identifies which resource to patch
type PatchTarget struct {
	// kind specifies the Kubernetes resource kind to patch
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`

	// name specifies the name of the resource to patch
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// namespace specifies the namespace of the resource (optional, defaults to same namespace as Order)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// Patch defines a modification to apply to a resource
type Patch struct {
	// target identifies which resource to patch
	// +kubebuilder:validation:Required
	Target PatchTarget `json:"target"`

	// set contains JSONPath expressions and their values to set
	// Keys are JSONPath expressions (e.g., ".spec.replicas")
	// Values are the desired values for those paths
	// +kubebuilder:validation:Required
	Set map[string]string `json:"set"`
}

// MenuRef references a cluster-scoped Menu by name.
type MenuRef struct {
	// name is the name of the cluster-scoped Menu to use as a template.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// OrderSpec defines the desired state of Order.
// Exactly one of source or menuRef must be set.
type OrderSpec struct {
	// source defines the immutable base artifact to render from.
	// Must not be set when menuRef is used.
	// +optional
	Source *OCISource `json:"source,omitempty"`

	// menuRef references a cluster-scoped Menu that provides the source,
	// base configuration, and override constraints for this Order.
	// Must not be set when source is used.
	// +optional
	MenuRef *MenuRef `json:"menuRef,omitempty"`

	// render defines optional rendering configuration for the source artifact.
	// When absent the source OCI artifact is treated as a pre-rendered manifest bundle.
	// When render.helm is set the source must be a Helm chart in OCI format.
	// When menuRef is set, the render type is inherited from the Menu and this
	// field is used only for consumer value overrides (render.helm.values).
	// +optional
	Render *Render `json:"render,omitempty"`

	// destination defines where the rendered Preparation artifact will be pushed.
	// When omitted, the in-cluster registry is used automatically.
	// +optional
	Destination *OCIDestination `json:"destination,omitempty"`

	// patches defines deterministic transformations applied to the source artifact
	// before producing a Preparation.
	// When menuRef is set, these patches are subject to the Menu's override policy.
	// +optional
	Patches []Patch `json:"patches,omitempty"`

	// edits defines UI-driven field modifications applied after patches.
	// Unlike patches, edits are never inherited from a Menu and are intended
	// for interactive changes made through the web UI.
	// When menuRef is set, edits are still subject to the Menu's patch override policy.
	// +optional
	Edits []Patch `json:"edits,omitempty"`

	// autoDeploy controls whether a newly created Preparation
	// should automatically become the active Serving.
	// If false, activation must be performed explicitly.
	// +kubebuilder:default=false
	AutoDeploy bool `json:"autoDeploy"`
}

// OrderPhase represents the current phase of the Order
// +kubebuilder:validation:Enum=Pending;Processing;Ready;Failed
type OrderPhase string

const (
	// OrderPhasePending indicates the config is waiting to be processed
	OrderPhasePending OrderPhase = "Pending"
	// OrderPhaseProcessing indicates the config is being processed
	OrderPhaseProcessing OrderPhase = "Processing"
	// OrderPhaseReady indicates the latest Preparation was successfully rendered
	OrderPhaseReady OrderPhase = "Ready"
	// OrderPhaseFailed indicates the config processing failed
	OrderPhaseFailed OrderPhase = "Failed"
)

// OrderStatus defines the observed state of Order.
type OrderStatus struct {
	// observedGeneration is the most recent generation observed by the controller
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// latestRevision is the name of the most recent immutable Preparation
	// produced from this Order
	// +optional
	LatestRevision string `json:"latestRevision,omitempty"`

	// latestArtifactDigest is the SHA256 digest of the OCI artifact produced by the
	// most recent Preparation. It is used as the pointer for the parentDigest of
	// the next Preparation.
	// +optional
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	LatestArtifactDigest string `json:"latestArtifactDigest,omitempty"`

	// latestConfigHash is a SHA-256 hash of the spec inputs (source OCI reference,
	// version, and patches) that produced the current latestRevision.
	// +optional
	LatestConfigHash string `json:"latestConfigHash,omitempty"`

	// phase represents the current phase of the Order lifecycle
	// +optional
	Phase OrderPhase `json:"phase,omitempty"`

	// conditions represent the current state of the Order resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Latest Revision",type=string,JSONPath=`.status.latestRevision`
// +kubebuilder:printcolumn:name="Menu",type=string,JSONPath=`.spec.menuRef.name`,priority=1
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.source.oci`,priority=1
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.source.version`,priority=1
// +kubebuilder:printcolumn:name="Auto Deploy",type=boolean,JSONPath=`.spec.autoDeploy`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Order is the Schema for the orders API
type Order struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Order
	// +required
	Spec OrderSpec `json:"spec"`

	// status defines the observed state of Order
	// +optional
	Status OrderStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// OrderList contains a list of Order
type OrderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Order `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Order{}, &OrderList{})
}
