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

// EnvironmentSpec defines a group of clusters and the engine that deploys
// there.
type EnvironmentSpec struct {
	// description says what the Environment is for.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description,omitempty"`

	// clusterSelector selects the Clusters of the Environment's namespace by
	// their labels. {} selects every Cluster of the namespace.
	// +required
	ClusterSelector metav1.LabelSelector `json:"clusterSelector"`

	// deployer is the engine that deploys to the Environment's clusters.
	// +required
	Deployer Deployer `json:"deployer"`
}

// DeployerType is an engine that deploys Preparations.
// +kubebuilder:validation:Enum=ArgoCD
type DeployerType string

const (
	// DeployerTypeArgoCD deploys through one Argo CD Application per
	// cluster.
	DeployerTypeArgoCD DeployerType = "ArgoCD"
)

// Deployer is the engine that deploys to the clusters of an Environment,
// with its settings.
// +union
// +kubebuilder:validation:XValidation:rule="self.type == 'ArgoCD' ? has(self.argoCD) : !has(self.argoCD)",message="argoCD is required when type is ArgoCD, and forbidden otherwise"
type Deployer struct {
	// type is the engine: ArgoCD.
	// +required
	// +unionDiscriminator
	Type DeployerType `json:"type"`

	// argoCD configures the Argo CD Applications of the Environment.
	// +optional
	// +unionMember
	ArgoCD *ArgoCDDeployer `json:"argoCD,omitempty"`
}

// ArgoCDDeployer configures the Argo CD Applications of an Environment.
type ArgoCDDeployer struct {
	// project is the Argo CD AppProject, in the argocd namespace, of every
	// Application that deploys to the Environment. Outside Kokumi's own
	// namespace, its sourceNamespaces must admit the Environment's
	// namespace; its destinations decide which servers and target
	// namespaces Orders may deploy to.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Project string `json:"project"`
}

// EnvironmentStatus defines the observed state of Environment.
type EnvironmentStatus struct {
	// observedGeneration is the most recent generation observed by the
	// controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// clusters counts the Clusters the Environment selects.
	// +optional
	Clusters *EnvironmentClusters `json:"clusters,omitempty"`

	// conditions represent the current state of the Environment. Ready
	// reports whether it selects any Cluster and its engine admits it; each
	// Cluster reports its own readiness.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// EnvironmentClusters counts the Clusters an Environment selects.
type EnvironmentClusters struct {
	// selected is how many Clusters of the namespace the clusterSelector
	// selects.
	// +required
	// +kubebuilder:validation:Minimum=0
	Selected int32 `json:"selected"`

	// ready is how many of them are Ready.
	// +required
	// +kubebuilder:validation:Minimum=0
	Ready int32 `json:"ready"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=env,categories=kokumi
// +kubebuilder:printcolumn:name="Deployer",type=string,JSONPath=`.spec.deployer.type`
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=`.spec.deployer.argoCD.project`
// +kubebuilder:printcolumn:name="Clusters",type=integer,JSONPath=`.status.clusters.selected`
// +kubebuilder:printcolumn:name="Ready Clusters",type=integer,JSONPath=`.status.clusters.ready`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Environment is a group of clusters for the Orders of its namespace to
// deploy to. It selects the Clusters of its namespace by their labels, and
// names the engine that deploys there.
type Environment struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Environment
	// +required
	Spec EnvironmentSpec `json:"spec"`

	// status defines the observed state of Environment
	// +optional
	Status EnvironmentStatus `json:"status,omitzero"`
}

// GetConditions returns the status conditions of the Environment.
func (e *Environment) GetConditions() []metav1.Condition {
	return e.Status.Conditions
}

// SetConditions sets the status conditions of the Environment.
func (e *Environment) SetConditions(conditions []metav1.Condition) {
	e.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// EnvironmentList contains a list of Environment
type EnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Environment `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Environment{}, &EnvironmentList{})
		return nil
	})
}
