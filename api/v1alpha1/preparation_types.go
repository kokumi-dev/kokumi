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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RecipeSource defines the immutable base artifact for a preparation
type RecipeSource struct {
	// oci is the OCI registry URL for the source manifests
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^oci://.*`
	OCI string `json:"oci"`

	// baseDigest is the SHA256 digest of the base source artifact
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	BaseDigest string `json:"baseDigest"`
}

// Renderer defines the tool and its version/digest used to render the source
type Renderer struct {
	// version is the semantic version of the renderer
	// +kubebuilder:validation:Required
	Version string `json:"version"`

	// digest is the SHA256 digest of the renderer binary/image
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`
}

// Artifact defines the final immutable output of the rendering process
type Artifact struct {
	// ociRef is the full OCI reference including digest
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^oci://.*@sha256:[a-f0-9]{64}$`
	OCIRef string `json:"ociRef"`

	// digest is the SHA256 digest of the artifact
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`

	// signed indicates whether the artifact has been cryptographically signed
	// +optional
	Signed bool `json:"signed,omitempty"`
}

// PreparationSpec defines the desired state of Preparation
type PreparationSpec struct {
	// recipe is the name of the recipe this preparation belongs to
	// +kubebuilder:validation:Required
	Recipe string `json:"recipe"`

	// source defines the source artifact information
	// +kubebuilder:validation:Required
	Source RecipeSource `json:"source"`

	// renderer defines the renderer used to process this preparation
	// +kubebuilder:validation:Required
	Renderer Renderer `json:"renderer"`

	// configHash is the SHA256 hash of the canonicalized recipe configuration
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]+$`
	ConfigHash string `json:"configHash"`

	// artifact defines the output artifact information
	// +kubebuilder:validation:Required
	Artifact Artifact `json:"artifact"`
}

// PreparationPhase represents the current phase of the Preparation
// +kubebuilder:validation:Enum=Pending;Ready;Failed
type PreparationPhase string

const (
	// PreparationPhasePending indicates the preparation is being created
	PreparationPhasePending PreparationPhase = "Pending"
	// PreparationPhaseReady indicates the preparation is ready for serving
	PreparationPhaseReady PreparationPhase = "Ready"
	// PreparationPhaseFailed indicates the preparation creation failed
	PreparationPhaseFailed PreparationPhase = "Failed"
)

// PreparationStatus defines the observed state of Preparation.
type PreparationStatus struct {
	// phase represents the current phase of the Preparation lifecycle
	// +optional
	Phase PreparationPhase `json:"phase,omitempty"`

	// createdAt is the timestamp when the preparation was created
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// conditions represent the current state of the Preparation resource.
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
// +kubebuilder:printcolumn:name="Recipe",type=string,JSONPath=`.spec.recipe`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Digest",type=string,JSONPath=`.spec.artifact.digest`,priority=1
// +kubebuilder:printcolumn:name="Signed",type=boolean,JSONPath=`.spec.artifact.signed`,priority=1
// +kubebuilder:printcolumn:name="Created",type=date,JSONPath=`.status.createdAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:resource:shortName=prep

// Preparation is the Schema for the preparations API
type Preparation struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Preparation
	// +required
	Spec PreparationSpec `json:"spec"`

	// status defines the observed state of Preparation
	// +optional
	Status PreparationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PreparationList contains a list of Preparation
type PreparationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Preparation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Preparation{}, &PreparationList{})
}
