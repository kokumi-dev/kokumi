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

// RepositorySpec defines an OCI repository listed in the catalog.
type RepositorySpec struct {
	// url is the oci:// URL of the repository, without a tag or digest.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^oci://[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?(:[0-9]+)?(/[a-z0-9]+(([._]|__|-+)[a-z0-9]+)*)+$`
	URL string `json:"url"`

	// registryRef names the Registry, in the Repository's namespace, the
	// repository is read through. Its url must cover the repository's.
	// Without it the repository is read anonymously.
	// +optional
	RegistryRef *corev1.LocalObjectReference `json:"registryRef,omitempty"`

	// description is a human-readable description of the repository.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description,omitempty"`
}

// Condition reasons used by Repositories, and by Products and Orders for
// the Registries they list.
const (
	// ReasonRegistryNotFound reports that a Registry named in registryRef
	// or registryRefs does not exist.
	ReasonRegistryNotFound = "RegistryNotFound"
	// ReasonRegistryNotCovered reports on a Repository that its registryRef
	// names a Registry whose url does not cover the repository.
	ReasonRegistryNotCovered = "NotCovered"
	// ReasonRepositoryListed reports on a Repository that it is ready to be
	// browsed.
	ReasonRepositoryListed = "Listed"
)

// RepositoryStatus defines the observed state of Repository.
type RepositoryStatus struct {
	// observedGeneration is the most recent generation observed by the
	// controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the Repository. Ready
	// reports whether its Registry exists and covers it.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Registry",type=string,JSONPath=`.spec.registryRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Repository lists an OCI repository in Kokumi's catalog, so that it can be
// browsed and a Product created from it. Nothing renders from a Repository:
// Products name their source URL and Registries themselves.
type Repository struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Repository
	// +required
	Spec RepositorySpec `json:"spec"`

	// status defines the observed state of Repository
	// +optional
	Status RepositoryStatus `json:"status,omitzero"`
}

// GetConditions returns the status conditions of the Repository.
func (r *Repository) GetConditions() []metav1.Condition {
	return r.Status.Conditions
}

// SetConditions sets the status conditions of the Repository.
func (r *Repository) SetConditions(conditions []metav1.Condition) {
	r.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// RepositoryList contains a list of Repository
type RepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Repository `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Repository{}, &RepositoryList{})
		return nil
	})
}
