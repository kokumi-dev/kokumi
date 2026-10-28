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

// ClusterConnection is how Kokumi and its engines reach a cluster.
// +kubebuilder:validation:Enum=InCluster;Kubeconfig
type ClusterConnection string

const (
	// ClusterConnectionInCluster is the cluster Kokumi runs in, reached with
	// Kokumi's own connection.
	ClusterConnectionInCluster ClusterConnection = "InCluster"
	// ClusterConnectionKubeconfig is a cluster reached with the kubeconfig
	// in a Secret.
	ClusterConnectionKubeconfig ClusterConnection = "Kubeconfig"
)

// ClusterSpec defines a cluster that Orders deploy to, and how to reach it.
// +union
// +kubebuilder:validation:XValidation:rule="self.connection == 'Kubeconfig' ? has(self.kubeconfig) : !has(self.kubeconfig)",message="kubeconfig is required when connection is Kubeconfig, and forbidden otherwise"
type ClusterSpec struct {
	// description says what the cluster is, such as where it runs and who
	// runs it.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description,omitempty"`

	// connection is how the cluster is reached: InCluster, the cluster
	// Kokumi runs in, or Kubeconfig, the kubeconfig in a Secret.
	// +required
	// +unionDiscriminator
	Connection ClusterConnection `json:"connection"`

	// kubeconfig names the Secret that holds the cluster's kubeconfig.
	// +optional
	// +unionMember
	Kubeconfig *ClusterKubeconfig `json:"kubeconfig,omitempty"`
}

// ClusterKubeconfig names the Secret that holds the kubeconfig of a cluster.
type ClusterKubeconfig struct {
	// secretRef names the Secret, in the Cluster's namespace, and its key
	// that hold a self-contained kubeconfig: the server of its current
	// context with its CA, and a token or a client certificate and key,
	// inline. Exec plugins, auth providers and file references are not
	// supported.
	// +required
	SecretRef KubeconfigSecretReference `json:"secretRef"`
}

// KubeconfigSecretReference names the Secret and the key of a kubeconfig.
type KubeconfigSecretReference struct {
	// name is the name of the Secret.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// key is the key of the kubeconfig in the Secret: value by default, as
	// Cluster API names it.
	// +optional
	// +kubebuilder:default=value
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Key string `json:"key,omitempty"`
}

// ClusterStatus defines the observed state of Cluster.
type ClusterStatus struct {
	// observedGeneration is the most recent generation observed by the
	// controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// server is the URL of the cluster's API server.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Server string `json:"server,omitempty"`

	// kubernetesVersion is the Kubernetes version the cluster's API server
	// reports.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	KubernetesVersion string `json:"kubernetesVersion,omitempty"`

	// conditions represent the current state of the Cluster. Ready reports
	// whether Orders can deploy to it: it is reachable and accepts the
	// credential.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=kcl,categories=kokumi
// +kubebuilder:validation:XValidation:rule="self.metadata.name.size() <= 63",message="the name of a Cluster is at most 63 characters: it is a label value of what deploys there"
// +kubebuilder:printcolumn:name="Connection",type=string,JSONPath=`.spec.connection`
// +kubebuilder:printcolumn:name="Server",type=string,JSONPath=`.status.server`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.kubernetesVersion`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Cluster is a cluster that Orders deploy to. Environments of its namespace
// select it by its labels. Kokumi reports whether it is reachable.
type Cluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Cluster
	// +required
	Spec ClusterSpec `json:"spec"`

	// status defines the observed state of Cluster
	// +optional
	Status ClusterStatus `json:"status,omitzero"`
}

// GetConditions returns the status conditions of the Cluster.
func (c *Cluster) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

// SetConditions sets the status conditions of the Cluster.
func (c *Cluster) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// ClusterList contains a list of Cluster.
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Cluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Cluster{}, &ClusterList{})
		return nil
	})
}
