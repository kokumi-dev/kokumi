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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Decision expresses the verdict of an Approval.
// +kubebuilder:validation:Enum=Approve
type Decision string

const (
	// DecisionApprove records an approving verdict.
	DecisionApprove Decision = "Approve"
)

// Approver identifies the authenticated person submitting an Approval.
type Approver struct {
	// username is the authenticated users username.
	// +kubebuilder:validation:MinLength=1
	// +required
	Username string `json:"username"`

	// uid is the authenticated Kubernetes user ID when available.
	// +optional
	UID string `json:"uid,omitempty"`

	// email is an optional email address.
	// +optional
	Email string `json:"email,omitempty"`

	// groups contains the authenticated Kubernetes groups or verified OIDC groups.
	// +optional
	// +listType=set
	Groups []string `json:"groups,omitempty"`
}

// ApprovalSpec defines an approving verdict for a Preparation.
type ApprovalSpec struct {
	// preparationRef references a Preparation in the same namespace.
	// +required
	PreparationRef *corev1.LocalObjectReference `json:"preparationRef"`

	// approver is populated by admission from the authenticated request or by
	// the server.
	// +optional
	Approver *Approver `json:"approver,omitempty"`

	// decision is the verdict of this Approval.
	// +kubebuilder:default=Approve
	// +kubebuilder:validation:Enum=Approve
	Decision Decision `json:"decision,omitempty"`
}

// ApprovalStatus defines the observed state of Approval.
type ApprovalStatus struct {
	// conditions represent the current state of the Approval resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=appr
// +kubebuilder:printcolumn:name="Preparation",type=string,JSONPath=`.spec.preparationRef.name`
// +kubebuilder:printcolumn:name="Approver",type=string,JSONPath=`.spec.approver.username`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Approval is the Schema for the approvals API
type Approval struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Approval
	// +required
	Spec ApprovalSpec `json:"spec"`

	// status defines the observed state of Approval
	// +optional
	Status ApprovalStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ApprovalList contains a list of Approval
type ApprovalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Approval `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Approval{}, &ApprovalList{})
		return nil
	})
}
