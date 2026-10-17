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

// ImagePolicy controls what Kokumi does with the container images the
// rendered manifests reference.
// +kubebuilder:validation:Enum=Record;Pin;Vendor
type ImagePolicy string

const (
	// ImagePolicyRecord records every image by digest and leaves the
	// manifests unchanged. Images that cannot be resolved are recorded
	// without a digest.
	ImagePolicyRecord ImagePolicy = "Record"
	// ImagePolicyPin rewrites every image reference to its digest, keeping
	// the name and tag for readability (repo:tag@sha256:...).
	ImagePolicyPin ImagePolicy = "Pin"
	// ImagePolicyVendor copies every image, with all platforms and its
	// referrers, into the Preparation's repository and rewrites the
	// references to pull it from there by digest.
	ImagePolicyVendor ImagePolicy = "Vendor"
)

// ImageFieldSpec locates a field that holds a container image reference, in
// the shape of a kustomize FieldSpec.
type ImageFieldSpec struct {
	// group is the API group of the resources. Empty matches the core group
	// and every other group.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Group string `json:"group,omitempty"`

	// version is the API version of the resources. Empty matches every
	// version.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	Version string `json:"version,omitempty"`

	// kind is the kind of the resources, for example "Prometheus".
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Kind string `json:"kind"`

	// path is the slash-separated path to the field, with [] for every
	// element of a list, for example "spec/image" or
	// "spec/sidecars[]/image".
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Path string `json:"path"`
}

// ProductImages configures how the container images of a Product's rendered
// manifests are handled.
type ProductImages struct {
	// policy controls what Kokumi does with the images: Record lists them by
	// digest, Pin also rewrites the manifests to those digests, and Vendor
	// copies the images into each Preparation's repository and rewrites the
	// manifests to pull them from there.
	// +optional
	// +kubebuilder:default=Record
	Policy ImagePolicy `json:"policy,omitempty"`

	// fieldSpecs lists the fields of custom resources that hold image
	// references, in addition to the containers, initContainers,
	// ephemeralContainers and image volumes of every Pod template.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=32
	FieldSpecs []ImageFieldSpec `json:"fieldSpecs,omitempty"`

	// pullSecretRef names a kubernetes.io/dockerconfigjson Secret in the
	// Product's namespace whose credentials are used, per registry host, to
	// resolve and vendor the images. Like a Pod's imagePullSecrets, it lets
	// everyone who may edit the Product use these credentials. Without it,
	// registries are accessed anonymously.
	// +optional
	PullSecretRef *corev1.LocalObjectReference `json:"pullSecretRef,omitempty"`
}

// EffectivePolicy returns policy, defaulting to Record.
func (i ProductImages) EffectivePolicy() ImagePolicy {
	if i.Policy == "" {
		return ImagePolicyRecord
	}
	return i.Policy
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

	// images configures how the container images the rendered manifests
	// reference are recorded, pinned or vendored. It applies to every Order
	// of the Product.
	// +optional
	// +kubebuilder:default={}
	Images ProductImages `json:"images,omitzero"`
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

	// signatures lists every signature in the graph of the source: on the
	// source itself and on what is attached to it, such as an SBOM. Each is
	// verified against spec.source.verify, or against the keys the Kitchen
	// trusts when it is not set. Only verified is a decision; signatures is
	// the evidence.
	// +optional
	// +listType=map
	// +listMapKey=digest
	// +kubebuilder:validation:MaxItems=64
	Signatures []SignatureVerdict `json:"signatures,omitempty"`

	// signaturesCheckedTime is when signatures was last checked. It is
	// refreshed when the signatures change, and otherwise at most once an
	// hour.
	// +optional
	SignaturesCheckedTime *metav1.Time `json:"signaturesCheckedTime,omitempty"`
}

// SignatureResult is the result of verifying one signature.
// +kubebuilder:validation:Enum=Verified;UntrustedSigner;Invalid;Unsupported
type SignatureResult string

const (
	// SignatureVerified is a signature by a key or identity the policy
	// accepts.
	SignatureVerified SignatureResult = "Verified"
	// SignatureUntrustedSigner is a signature by a key or identity the
	// policy does not accept. The signature itself may be valid.
	SignatureUntrustedSigner SignatureResult = "UntrustedSigner"
	// SignatureInvalid is a signature that does not verify, for example
	// because it signs another digest.
	SignatureInvalid SignatureResult = "Invalid"
	// SignatureUnsupported is a signature in a format Kokumi does not
	// verify, such as Notary.
	SignatureUnsupported SignatureResult = "Unsupported"
)

// SignatureVerdict is the result of verifying one signature in the graph of
// a source.
type SignatureVerdict struct {
	// digest is the digest of the signature: a Sigstore bundle referrer, a
	// legacy cosign signature manifest, or a signature in another format.
	// +required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`

	// subject is the digest of the manifest the signature signs.
	// +required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Subject string `json:"subject"`

	// result is the result of the verification.
	// +required
	Result SignatureResult `json:"result"`

	// signer identifies whose signature verified. Set only when result is
	// Verified.
	// +optional
	Signer *SourceVerification `json:"signer,omitempty"`

	// message says why the signature did not verify.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Message string `json:"message,omitempty"`
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
