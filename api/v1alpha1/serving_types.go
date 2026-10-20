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

// ServingSpec defines the desired state of Serving. The promotion mode is
// taken from the Order's spec.promotion.mode.
type ServingSpec struct {
	// orderName is the name of the Order to serve
	// +required
	OrderName string `json:"orderName"`

	// preparationName is the Preparation promoted for Manual promotion.
	// It is ignored when the Order uses Automatic promotion.
	// +optional
	PreparationName string `json:"preparationName,omitempty"`
}

// ServingStatus defines the observed state of Serving.
type ServingStatus struct {
	// observedGeneration is the most recent generation observed by the controller
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// targetPreparationName is the Preparation the Serving is converging to
	// once it is approved and its evidence verifies: spec.preparationName
	// for Manual promotion, or the newest Ready Preparation of the Order for
	// Automatic promotion, after it passed its upstream stages and checks.
	// Its approvals are sealed when it becomes the target.
	// +optional
	TargetPreparationName string `json:"targetPreparationName,omitempty"`

	// history lists the most recent deployments, newest first. The deployed
	// Preparation is the newest entry with a promotionRecord; an entry
	// without one is a promotion that is being recorded.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=16
	History []DeploymentEntry `json:"history,omitempty"`

	// deployedVulnerabilities sums the latest scans of the images of the
	// deployed Preparation. It keeps changing after the deployment, as new
	// vulnerabilities are published.
	// +optional
	DeployedVulnerabilities *VulnerabilitySummary `json:"deployedVulnerabilities,omitempty"`

	// conditions represent the current state of the Serving resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DeploymentEndReason says why a deployment ended.
// +kubebuilder:validation:Enum=Superseded;Degraded
type DeploymentEndReason string

const (
	// DeploymentEndReasonSuperseded ends a deployment when another
	// Preparation is promoted.
	DeploymentEndReasonSuperseded DeploymentEndReason = "Superseded"
	// DeploymentEndReasonDegraded ends a deployment when Argo CD reports it
	// degraded. A recovery opens a new entry for the same Preparation.
	DeploymentEndReasonDegraded DeploymentEndReason = "Degraded"
)

// DeploymentEntry is one continuous deployment of a Preparation by a
// Serving: from its promotion, or its recovery, until it is superseded or
// degraded. It is self-contained, so its records can be read after the
// Preparation is gone.
type DeploymentEntry struct {
	// preparationName is the name of the deployed Preparation.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	PreparationName string `json:"preparationName"`

	// digest is the digest of the Preparation's artifact.
	// +required
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`

	// ociRef is the full OCI reference of the artifact, including digest.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	OCIRef string `json:"ociRef"`

	// registryRef names the Registry that pushed the artifact, through which
	// its records are read.
	// +optional
	RegistryRef *corev1.LocalObjectReference `json:"registryRef,omitempty"`

	// productRevision is the Product revision the Preparation renders.
	// +required
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	ProductRevision string `json:"productRevision"`

	// startedTime is when the Preparation passed its gates, or recovered.
	// +required
	StartedTime metav1.Time `json:"startedTime"`

	// healthyTime is when Argo CD first reported the deployment synced and
	// healthy at the digest.
	// +optional
	HealthyTime *metav1.Time `json:"healthyTime,omitempty"`

	// endedTime is when the deployment ended.
	// +optional
	EndedTime *metav1.Time `json:"endedTime,omitempty"`

	// endReason says why the deployment ended.
	// +optional
	EndReason DeploymentEndReason `json:"endReason,omitempty"`

	// promotionRecord is the digest of the signed promotion record, a
	// referrer of the artifact. A recovery carries the record of the
	// promotion it continues.
	// +optional
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	PromotionRecord string `json:"promotionRecord,omitempty"`

	// deploymentRecord is the digest of the signed deployment record, a
	// referrer of the artifact pushed once the deployment was healthy.
	// +optional
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	DeploymentRecord string `json:"deploymentRecord,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Order",type=string,JSONPath=`.spec.orderName`
// +kubebuilder:printcolumn:name="Preparation",type=string,JSONPath=`.spec.preparationName`,priority=1
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.status.targetPreparationName`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].reason`
// +kubebuilder:printcolumn:name="Approved",type=string,JSONPath=`.status.conditions[?(@.type=='Approved')].reason`
// +kubebuilder:printcolumn:name="Verified",type=string,JSONPath=`.status.conditions[?(@.type=='Verified')].reason`
// +kubebuilder:printcolumn:name="Upstream",type=string,JSONPath=`.status.conditions[?(@.type=='UpstreamDeployed')].reason`,priority=1
// +kubebuilder:printcolumn:name="Latest",type=string,JSONPath=`.status.history[0].preparationName`,priority=1
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

// GetConditions returns the status conditions of the Serving.
func (s *Serving) GetConditions() []metav1.Condition {
	return s.Status.Conditions
}

// SetConditions sets the status conditions of the Serving.
func (s *Serving) SetConditions(conditions []metav1.Condition) {
	s.Status.Conditions = conditions
}

// DeployedEntry returns the deployment the Serving deploys: the newest entry
// of its history whose promotion is recorded, nil when it deploys nothing.
func (s *Serving) DeployedEntry() *DeploymentEntry {
	for i := range s.Status.History {
		if s.Status.History[i].PromotionRecord != "" {
			return &s.Status.History[i]
		}
	}
	return nil
}

// DeployedPreparationName returns the name of the Preparation the Serving
// deploys, "" when it deploys none.
func (s *Serving) DeployedPreparationName() string {
	if e := s.DeployedEntry(); e != nil {
		return e.PreparationName
	}
	return ""
}

// +kubebuilder:object:root=true

// ServingList contains a list of Serving
type ServingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Serving `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Serving{}, &ServingList{})
		return nil
	})
}
