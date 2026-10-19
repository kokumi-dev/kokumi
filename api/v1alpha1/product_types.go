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
	// +required
	Target PatchTarget `json:"target"`

	// paths lists the allowed JSONPath expressions within the target
	// (e.g. ".spec.replicas").
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=1024
	// +kubebuilder:validation:MaxItems=64
	// +listType=set
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
	// referrers, into a repository of its own below the Order's destination,
	// <destination>/images/<image name>, tagged with the tag its reference
	// names, and rewrites the references to pull it from there by digest.
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
	// copies each image into a repository of its own below the Product's
	// vendor location, <vendor url>/images/<image name>, and rewrites the
	// manifests to pull them from there. Vendor requires spec.vendor.
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
// +kubebuilder:validation:XValidation:rule="!has(self.images) || !has(self.images.policy) || self.images.policy != 'Vendor' || has(self.vendor)",message="the Vendor image policy needs spec.vendor, the location the images are copied below"
type ProductSpec struct {
	// source defines the OCI artifact the Product is built from. Orders
	// cannot change the source or version.
	// +required
	Source OCISource `json:"source"`

	// vendor optionally copies the source artifact to another OCI
	// repository, and under the Vendor image policy also the images, below
	// it at <url>/images. When set, Orders render from the vendored copy.
	// Without a url, the source is copied to
	// oci://<in-cluster registry>/<namespace>/vendor/<name>. A source read
	// with credentials is only copied where a Registry with credentials
	// connects to.
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
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=128
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

	// registryRefs lists Registries in the Product's namespace. Kokumi
	// connects to the Product's source, the registries of its images and
	// their mirrors, and its vendor location through the one whose url
	// covers each, the longest when several do. Orders connect to their
	// destination through their own registryRefs.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=16
	RegistryRefs []corev1.LocalObjectReference `json:"registryRefs,omitempty"`
}

// ProductSourceStatus is the source address advertised by the Product.
// Orders render from exactly what is published here.
type ProductSourceStatus struct {
	// url is the oci:// URL of the repository of the source artifact: of
	// the vendored copy when the Product vendors.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'oci'",message="must be a valid OCI URL"
	URL string `json:"url"`

	// version is the tag of the source artifact.
	// +required
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// digest is the digest version resolved to. Orders render exactly this
	// artifact, so every variant of the Product renders the same source.
	// +required
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

	// registryAccess lists the registry locations the Product needs: its
	// source and its vendor location, and the Registry Kokumi connects to
	// each through. The images belong to the render of each Order and are
	// listed on the Orders.
	// +optional
	// +listType=map
	// +listMapKey=url
	// +listMapKey=operation
	// +listMapKey=client
	// +kubebuilder:validation:MaxItems=32
	RegistryAccess []RegistryAccess `json:"registryAccess,omitempty"`

	// vulnerabilities summarizes the latest scans of the images the
	// Product vendors and its Orders' Preparations in use run. The signed
	// reports are referrers of the vendored images; this summary is for
	// display. Set while the Kitchen enables scanning and the Product's
	// image policy is Vendor.
	// +optional
	Vulnerabilities *ProductVulnerabilities `json:"vulnerabilities,omitempty"`

	// conditions represent the current state of the Product resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ProductVulnerabilities is the record of the vulnerability scans of a
// Product's vendored images.
type ProductVulnerabilities struct {
	// scanner identifies the scanner and the database of the latest scan.
	// +required
	Scanner VulnerabilityScanner `json:"scanner"`

	// images lists the latest scan of each vendored image digest in use.
	// When more digests are in use, the most recently scanned are listed
	// and the Scanned condition says how many were left out.
	// +optional
	// +listType=map
	// +listMapKey=digest
	// +kubebuilder:validation:MaxItems=512
	Images []ImageScan `json:"images,omitempty"`
}

// VulnerabilityScanner identifies the scanner and the vulnerability database
// a scan ran with.
type VulnerabilityScanner struct {
	// name is the scanner, "trivy".
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// version is the version of the scanner.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Version string `json:"version"`

	// database is the OCI reference of the vulnerability database, with
	// its digest.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Database string `json:"database,omitempty"`

	// databaseUpdatedTime is when the vulnerability database was built.
	// +optional
	DatabaseUpdatedTime *metav1.Time `json:"databaseUpdatedTime,omitempty"`
}

// SBOMOrigin says where the SBOM a scan was computed from came from.
// +kubebuilder:validation:Enum=Generated;Image;Signed
type SBOMOrigin string

const (
	// SBOMOriginGenerated is an SBOM Kokumi generated from the image and
	// attached to it, signed.
	SBOMOriginGenerated SBOMOrigin = "Generated"
	// SBOMOriginImage is an SBOM attestation inside the image index, as
	// BuildKit writes it. The image digest covers it.
	SBOMOriginImage SBOMOrigin = "Image"
	// SBOMOriginSigned is an SBOM attached to the image and signed by a key
	// the Kitchen trusts.
	SBOMOriginSigned SBOMOrigin = "Signed"
)

// ImageScan is the latest scan of one vendored image digest.
type ImageScan struct {
	// digest is the digest of the vendored image, an image index or
	// manifest.
	// +required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`

	// repository is the oci:// URL of the vendored image's repository.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	Repository string `json:"repository"`

	// platform is the platform of the image index that was scanned, such
	// as "linux/amd64". Empty for a single-platform image.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	Platform string `json:"platform,omitempty"`

	// scannedTime is when the image was scanned.
	// +required
	ScannedTime metav1.Time `json:"scannedTime"`

	// sbom is the SBOM the scan was computed from.
	// +required
	SBOM ImageSBOM `json:"sbom"`

	// report is the digest of the signed vulnerability report, a referrer
	// of the vendored image.
	// +required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Report string `json:"report"`

	// summary counts the findings of the report.
	// +required
	Summary VulnerabilitySummary `json:"summary"`
}

// ImageSBOM identifies the SBOM a scan was computed from.
type ImageSBOM struct {
	// digest is the digest of the referrer or attestation manifest holding
	// the SBOM.
	// +required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`

	// origin says where the SBOM came from.
	// +required
	Origin SBOMOrigin `json:"origin"`
}

// VulnerabilitySummary counts vulnerability findings by severity.
type VulnerabilitySummary struct {
	// critical counts the findings of severity Critical.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Critical int32 `json:"critical"`
	// high counts the findings of severity High.
	// +optional
	// +kubebuilder:validation:Minimum=0
	High int32 `json:"high"`
	// medium counts the findings of severity Medium.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Medium int32 `json:"medium"`
	// low counts the findings of severity Low.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Low int32 `json:"low"`
	// unknown counts the findings without a severity.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Unknown int32 `json:"unknown"`
	// fixable counts the findings a newer version of the package fixes.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Fixable int32 `json:"fixable"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].reason`
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.source.url`
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

// GetConditions returns the status conditions of the Product.
func (p *Product) GetConditions() []metav1.Condition {
	return p.Status.Conditions
}

// SetConditions sets the status conditions of the Product.
func (p *Product) SetConditions(conditions []metav1.Condition) {
	p.Status.Conditions = conditions
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
