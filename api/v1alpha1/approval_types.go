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
	"k8s.io/apimachinery/pkg/types"
)

// ApprovalDecision expresses the verdict of an Approval.
// +kubebuilder:validation:Enum=Approve;Reject
type ApprovalDecision string

const (
	// ApprovalDecisionApprove records an approving verdict.
	ApprovalDecisionApprove ApprovalDecision = "Approve"
	// ApprovalDecisionReject records a rejecting verdict. A Reject from an
	// eligible approver blocks the Preparation until that approver approves.
	ApprovalDecisionReject ApprovalDecision = "Reject"
)

// Approver identifies the authenticated person who submitted an Approval.
// It is populated by the kokumi server from the verified OIDC session.
type Approver struct {
	// issuer is the OIDC issuer URL that authenticated the approver.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	Issuer string `json:"issuer"`

	// subject is the OIDC subject (sub claim). Together with issuer it is the
	// stable identity key: only the latest vote per issuer and subject counts.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Subject string `json:"subject"`

	// username is the human-readable name of the approver.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Username string `json:"username,omitempty"`

	// email is the verified email address of the approver.
	// +optional
	// +kubebuilder:validation:MaxLength=254
	Email string `json:"email,omitempty"`

	// groups are the verified OIDC groups of the approver at submission time.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=253
	Groups []string `json:"groups,omitempty"`
}

// ApprovalPreparationReference binds an Approval to the exact Preparation
// object and artifact content that was reviewed.
type ApprovalPreparationReference struct {
	// name is the name of the Preparation in the same namespace.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// uid is the UID of the Preparation. A recreated Preparation with the
	// same name does not inherit votes.
	// +required
	UID types.UID `json:"uid"`

	// artifactDigest is the digest of the reviewed Preparation artifact.
	// +required
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	ArtifactDigest string `json:"artifactDigest"`
}

// ApprovalSpec is an immutable vote on a Preparation. Approvals can only be
// created by the kokumi server and are never updated or deleted.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Approval spec is immutable"
type ApprovalSpec struct {
	// orderName is the name of the Order the reviewed Preparation belongs to.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	OrderName string `json:"orderName"`

	// preparationRef references the reviewed Preparation.
	// +required
	PreparationRef ApprovalPreparationReference `json:"preparationRef"`

	// approver identifies who submitted this vote.
	// +required
	Approver Approver `json:"approver"`

	// decision is the verdict of this vote.
	// +required
	Decision ApprovalDecision `json:"decision"`

	// comment is an optional review comment.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Comment string `json:"comment,omitempty"`

	// submittedTime is when the kokumi server accepted the vote. It orders
	// votes of the same approver submitted within the same second.
	// +required
	SubmittedTime metav1.MicroTime `json:"submittedTime"`
}

// ApprovalStatus defines the observed state of Approval.
type ApprovalStatus struct {
	// observedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the Approval resource.
	// The Counted condition reports whether this vote contributes to the
	// approval gate of its Preparation.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=appr
// +kubebuilder:selectablefield:JSONPath=`.spec.orderName`
// +kubebuilder:selectablefield:JSONPath=`.spec.preparationRef.name`
// +kubebuilder:printcolumn:name="Order",type=string,JSONPath=`.spec.orderName`,priority=1
// +kubebuilder:printcolumn:name="Preparation",type=string,JSONPath=`.spec.preparationRef.name`
// +kubebuilder:printcolumn:name="Approver",type=string,JSONPath=`.spec.approver.username`
// +kubebuilder:printcolumn:name="Decision",type=string,JSONPath=`.spec.decision`
// +kubebuilder:printcolumn:name="Counted",type=string,JSONPath=`.status.conditions[?(@.type=='Counted')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Counted')].reason`,priority=1
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
