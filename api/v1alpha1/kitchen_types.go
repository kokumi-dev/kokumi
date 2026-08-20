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
	"k8s.io/apimachinery/pkg/runtime"
)

// KitchenSpec defines the desired state of Kitchen
type KitchenSpec struct {
	// ArgoCDURL is the base URL of the Argo CD instance used to build deep
	// links on the Servings page. Empty means no links.
	// +kubebuilder:validation:XValidation:rule="self == '' || (isURL(self) && url(self).getScheme() in ['http', 'https'])",message="must be a valid http or https URL"
	// +optional
	ArgoCDURL string `json:"argoCDURL,omitempty"`
}

// KitchenStatus defines the observed state of Kitchen.
type KitchenStatus struct {
	// observedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the Kitchen resource.
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
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].reason`
// +kubebuilder:printcolumn:name="Argo CD URL",type=string,JSONPath=`.spec.argoCDURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Kitchen is the Schema for the kitchens API
type Kitchen struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Kitchen
	// +required
	Spec KitchenSpec `json:"spec"`

	// status defines the observed state of Kitchen
	// +optional
	Status KitchenStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// KitchenList contains a list of Kitchen
type KitchenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Kitchen `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Kitchen{}, &KitchenList{})
		return nil
	})
}
