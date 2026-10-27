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

// DefaultRegistryName is the name of the Registry of the bundled registry,
// which the controller creates in its namespace when it is missing.
const DefaultRegistryName = "in-cluster"

// RegistryScheme is the protocol a registry is accessed with.
// +kubebuilder:validation:Enum=HTTPS;HTTP
type RegistryScheme string

const (
	// RegistrySchemeHTTPS accesses the registry over TLS.
	RegistrySchemeHTTPS RegistryScheme = "HTTPS"
	// RegistrySchemeHTTP accesses the registry over plain HTTP.
	RegistrySchemeHTTP RegistryScheme = "HTTP"
)

// RegistrySpec defines how Kokumi connects to an OCI registry, or to a part
// of one.
// +kubebuilder:validation:XValidation:rule="!(has(self.certSecretRef) && self.scheme == 'HTTP')",message="certSecretRef needs scheme HTTPS"
type RegistrySpec struct {
	// url is the oci:// URL this Registry covers: a registry such as
	// oci://ghcr.io, or a prefix of its repositories such as
	// oci://ghcr.io/myorg. A Product or Order that lists the Registry in its
	// registryRefs connects through it to every location at or below url.
	// When several of its Registries cover a location, the one with the
	// longest url wins.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^oci://[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?(:[0-9]+)?(/[a-z0-9]+(([._]|__|-+)[a-z0-9]+)*)*$`
	// +kubebuilder:validation:XValidation:rule="!self.matches('^oci://[^/]+:443(/|$)')",message="must not name the default port 443"
	URL string `json:"url"`

	// secretRef names a kubernetes.io/dockerconfigjson Secret in the
	// Registry's namespace. Its entry for the host of url, matched as the
	// kubelet matches image pull Secrets, authenticates every request.
	// Without it the registry is accessed anonymously.
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`

	// certSecretRef names a Secret in the Registry's namespace with TLS
	// settings: ca.crt adds a certificate authority to the ones of the
	// system, and tls.crt with tls.key is a client certificate. A
	// kubernetes.io/tls or an Opaque Secret both work.
	// +optional
	CertSecretRef *corev1.LocalObjectReference `json:"certSecretRef,omitempty"`

	// scheme is the protocol the registry is accessed with. HTTP sends
	// credentials in plain text and suits only registries inside the
	// cluster network.
	// +optional
	// +kubebuilder:default=HTTPS
	Scheme RegistryScheme `json:"scheme,omitempty"`
}

// EffectiveScheme returns scheme, defaulting to HTTPS.
func (s RegistrySpec) EffectiveScheme() RegistryScheme {
	if s.Scheme == "" {
		return RegistrySchemeHTTPS
	}
	return s.Scheme
}

// RegistryStatus defines the observed state of Registry.
type RegistryStatus struct {
	// observedGeneration is the most recent generation observed by the
	// controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the Registry. Ready reports
	// whether the registry accepted the Registry's credentials and TLS
	// settings at its API base endpoint or, without credentials, whether it
	// answered there without requiring basic credentials. It does not tell
	// which repositories may be read or written.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// RegistryOperation is what a client does at a registry location.
// +kubebuilder:validation:Enum=Pull;Push
type RegistryOperation string

const (
	// RegistryOperationPull reads from the location.
	RegistryOperationPull RegistryOperation = "Pull"
	// RegistryOperationPush writes to the location.
	RegistryOperationPush RegistryOperation = "Push"
)

// RegistryClient is who accesses a registry location.
// +kubebuilder:validation:Enum=Kokumi;ArgoCD;Kubelet
type RegistryClient string

const (
	// RegistryClientKokumi is the Kokumi controller.
	RegistryClientKokumi RegistryClient = "Kokumi"
	// RegistryClientArgoCD is Argo CD, which pulls the Preparations it
	// deploys.
	RegistryClientArgoCD RegistryClient = "ArgoCD"
	// RegistryClientKubelet is the kubelet, which pulls the images of the
	// deployed workloads.
	RegistryClientKubelet RegistryClient = "Kubelet"
)

// RegistryAccess is a registry location a Product or an Order needs, who
// accesses it, and the Registry that authenticates Kokumi there. Kokumi
// gives no credentials to Argo CD and the kubelet: they need their own for
// the locations reported for them.
type RegistryAccess struct {
	// url is the location, or the prefix of the locations, accessed.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	URL string `json:"url"`

	// operation is what client does there.
	// +required
	Operation RegistryOperation `json:"operation"`

	// client is who accesses the location.
	// +required
	Client RegistryClient `json:"client"`

	// registry names the Registry whose connection Kokumi uses there. It is
	// empty when Kokumi accesses the location anonymously, and for the
	// other clients.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Registry string `json:"registry,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=kokumi
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Registry is a connection to an OCI registry, or to a part of one: its URL,
// the credentials and TLS settings for it, and its scheme. Products and
// Orders list the Registries they connect through in their registryRefs.
type Registry struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Registry
	// +required
	Spec RegistrySpec `json:"spec"`

	// status defines the observed state of Registry
	// +optional
	Status RegistryStatus `json:"status,omitzero"`
}

// GetConditions returns the status conditions of the Registry.
func (r *Registry) GetConditions() []metav1.Condition {
	return r.Status.Conditions
}

// SetConditions sets the status conditions of the Registry.
func (r *Registry) SetConditions(conditions []metav1.Condition) {
	r.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// RegistryList contains a list of Registry
type RegistryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Registry `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Registry{}, &RegistryList{})
		return nil
	})
}
