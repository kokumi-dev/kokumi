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

// PreparationPolicyType defines how updates to the Preparations are handled
// +kubebuilder:validation:Enum=Automatic;Manual
type PreparationPolicyType string

const (
	// PreparationPolicyAutomatic automatically deploys new preparations
	PreparationPolicyAutomatic PreparationPolicyType = "Automatic"
	// PreparationPolicyManual requires manual approval for preparation updates
	PreparationPolicyManual PreparationPolicyType = "Manual"
)

// PreparationPolicy defines how updates to the Preparation artifact are handled
// when a new version is available for deployment. It determines whether updates
// are automatically deployed or require manual approval. By default, updates are manual.
type PreparationPolicy struct {
	// type specifies whether preparation updates are automatic or manual
	// +kubebuilder:validation:Required
	// +kubebuilder:default=Manual
	Type PreparationPolicyType `json:"type"`
}

// ServingSpec defines the desired state of Serving
type ServingSpec struct {
	// order is the name of the order to serve
	// +kubebuilder:validation:Required
	Order string `json:"order"`

	// preparation is the desired Preparation to serve
	// +kubebuilder:validation:Required
	Preparation string `json:"preparation"`

	// preparationPolicy defines how preparation updates are handled
	// +optional
	PreparationPolicy PreparationPolicy `json:"preparationPolicy,omitempty"`
}

// ServingPhase represents the current phase of the Serving
// +kubebuilder:validation:Enum=Pending;Deploying;Deployed;Failed
type ServingPhase string

const (
	// ServingPhasePending indicates the serving is pending
	ServingPhasePending ServingPhase = "Pending"
	// ServingPhaseDeploying indicates the serving is in progress
	ServingPhaseDeploying ServingPhase = "Deploying"
	// ServingPhaseDeployed indicates the serving is complete and active
	ServingPhaseDeployed ServingPhase = "Deployed"
	// ServingPhaseFailed indicates the serving failed
	ServingPhaseFailed ServingPhase = "Failed"
)

// ServingStatus defines the observed state of Serving.
type ServingStatus struct {
	// observedPreparation is the preparation that was last observed by the controller
	// +optional
	ObservedPreparation string `json:"observedPreparation,omitempty"`

	// deployedDigest is the SHA256 digest of the currently deployed artifact
	// +optional
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	DeployedDigest string `json:"deployedDigest,omitempty"`

	// phase represents the current phase of the Serving lifecycle
	// +optional
	Phase ServingPhase `json:"phase,omitempty"`

	// conditions represent the current state of the Serving resource.
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
// +kubebuilder:printcolumn:name="Order",type=string,JSONPath=`.spec.order`
// +kubebuilder:printcolumn:name="Preparation",type=string,JSONPath=`.spec.preparation`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Policy",type=string,JSONPath=`.spec.preparationPolicy.type`,priority=1
// +kubebuilder:printcolumn:name="Observed",type=string,JSONPath=`.status.observedPreparation`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Serving is the Schema for the servings API
type Serving struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Serving
	// +required
	Spec ServingSpec `json:"spec"`

	// status defines the observed state of Serving
	// +optional
	Status ServingStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ServingList contains a list of Serving
type ServingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Serving `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Serving{}, &ServingList{})
}
