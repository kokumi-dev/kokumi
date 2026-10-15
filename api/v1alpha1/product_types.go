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

// OverridePolicy controls which changes the Orders of a Product may make.
// +kubebuilder:validation:Enum=None;Listed;All
type OverridePolicy string

const (
	// OverridePolicyNone allows Orders no changes.
	OverridePolicyNone OverridePolicy = "None"
	// OverridePolicyListed allows Orders only the listed changes.
	OverridePolicyListed OverridePolicy = "Listed"
	// OverridePolicyAll allows Orders any change.
	OverridePolicyAll OverridePolicy = "All"
)

// AllowedPatchTarget defines which patch target and JSON paths Orders may use.
type AllowedPatchTarget struct {
	// target identifies the resource that Orders may patch. A target without
	// a namespace matches the resource in any namespace.
	// +kubebuilder:validation:Required
	Target PatchTarget `json:"target"`

	// paths lists the allowed JSONPath expressions within the target
	// (e.g. ".spec.replicas").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Paths []string `json:"paths"`
}

// ValueOverrides controls which Helm values Orders may set.
// +kubebuilder:validation:XValidation:rule="has(self.policy) && self.policy == 'Listed' ? has(self.paths) : !has(self.paths)",message="paths must be set when policy is Listed, and only then"
type ValueOverrides struct {
	// policy controls which Helm values Orders may set.
	// None: Orders may not set any values.
	// Listed: Orders may set only the values listed in paths.
	// All: Orders may set any value.
	// +optional
	// +kubebuilder:default=None
	Policy OverridePolicy `json:"policy,omitempty"`

	// paths lists the dot-separated Helm value paths that Orders may set
	// (e.g. "ui.message", "replicaCount"). Only used when policy is Listed.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	Paths []string `json:"paths,omitempty"`
}

// PatchOverrides controls which patches and edits Orders may set.
// +kubebuilder:validation:XValidation:rule="has(self.policy) && self.policy == 'Listed' ? has(self.targets) : !has(self.targets)",message="targets must be set when policy is Listed, and only then"
type PatchOverrides struct {
	// policy controls which patches and edits Orders may set.
	// None: Orders may not set any patches or edits.
	// Listed: Orders may patch and edit only the paths listed in targets.
	// All: Orders may patch and edit anything.
	// +optional
	// +kubebuilder:default=None
	Policy OverridePolicy `json:"policy,omitempty"`

	// targets lists the patch targets and paths that Orders may patch or
	// edit. Only used when policy is Listed.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	Targets []AllowedPatchTarget `json:"targets,omitempty"`
}

// Overrides controls what the Orders of a Product may change.
type Overrides struct {
	// values controls which Helm values Orders may set.
	// +optional
	// +kubebuilder:default={}
	Values ValueOverrides `json:"values,omitzero"`

	// patches controls which patches and edits Orders may set.
	// +optional
	// +kubebuilder:default={}
	Patches PatchOverrides `json:"patches,omitzero"`
}

// ProductSpec defines the desired state of Product.
// A Product is the complete definition of an application: the source artifact,
// how it is rendered, and its base patches. Each Order is a variant of
// exactly one Product and only declares what differs.
type ProductSpec struct {
	// source defines the OCI artifact the Product is built from. Orders
	// cannot change the source or version.
	// +kubebuilder:validation:Required
	Source OCISource `json:"source"`

	// vendor optionally copies the source artifact to another OCI registry.
	// When set, Orders render from the vendored copy. When neither oci nor
	// pantryRef is set, the source is copied to
	// oci://<in-cluster registry>/<namespace>/vendor/<name>.
	// +optional
	Vendor *OCIDestination `json:"vendor,omitempty"`

	// render defines optional rendering configuration for the source artifact.
	// When absent the source is treated as a pre-rendered manifest bundle.
	// Orders cannot change the render type.
	// +optional
	Render *Render `json:"render,omitempty"`

	// patches defines base patches applied for every Order. Order patches
	// are merged on top, per target.
	// +optional
	Patches []Patch `json:"patches,omitempty"`

	// overrides controls what Orders may change. By default Orders may not
	// set any Helm values, patches, or edits.
	// +optional
	// +kubebuilder:default={}
	Overrides Overrides `json:"overrides,omitzero"`
}

// ProductSourceStatus is the source address advertised by the Product.
// Orders render from exactly what is published here.
type ProductSourceStatus struct {
	// oci is the full OCI URL of the source artifact.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'oci'",message="must be a valid OCI URL"
	OCI string `json:"oci"`

	// version is the tag of the source artifact.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// pantryRef references the Pantry providing pull credentials for oci.
	// Absent when the registry is anonymously accessible.
	// +optional
	PantryRef *PantryRef `json:"pantryRef,omitempty"`

	// digest is the digest version resolved to. Orders render exactly this
	// artifact, so every variant of the Product renders the same source.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`

	// verified records who signed the source, when its signature verified:
	// through spec.source.verify, or by a key the Kitchen trusts. Absent
	// when the source was not verified.
	// +optional
	Verified *SourceVerification `json:"verified,omitempty"`
}

// SourceVerification records the signer whose signature on a source
// verified.
type SourceVerification struct {
	// provider is the signature format that verified.
	// +required
	Provider VerificationProvider `json:"provider"`

	// keyID identifies the public key that verified the signature: the
	// base64-encoded SHA-256 of its DER encoding. Set for key-based
	// verification.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	KeyID string `json:"keyID,omitempty"`

	// issuer is the OIDC issuer of the keyless signer's certificate.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Issuer string `json:"issuer,omitempty"`

	// subject is the subject of the keyless signer's certificate.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Subject string `json:"subject,omitempty"`
}

// ProductStatus defines the observed state of Product.
type ProductStatus struct {
	// observedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// configHash is a SHA-256 hash of the source and vendor configuration
	// and the OCI URLs they resolved to. When it matches the current inputs
	// the published source is reused without resolving it again.
	// +optional
	ConfigHash string `json:"configHash,omitempty"`

	// source is the source address Orders render from. Absent until the
	// Product controller has resolved (and, if configured, vendored) the
	// source.
	// +optional
	Source *ProductSourceStatus `json:"source,omitempty"`

	// conditions represent the current state of the Product resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].reason`
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.source.oci`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.source.version`
// +kubebuilder:printcolumn:name="Verified",type=string,JSONPath=`.status.conditions[?(@.type=='SourceVerified')].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Product is the Schema for the products API.
// A Product defines an application completely; Orders in the same namespace
// reference it and add their own values, patches, and promotion settings.
type Product struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Product
	// +required
	Spec ProductSpec `json:"spec"`

	// status defines the observed state of Product
	// +optional
	Status ProductStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ProductList contains a list of Product
type ProductList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Product `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Product{}, &ProductList{})
		return nil
	})
}
