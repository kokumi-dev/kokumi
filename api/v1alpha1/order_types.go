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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// PromotionMode controls how new Preparations become active Servings.
// +kubebuilder:validation:Enum=Automatic;Manual
type PromotionMode string

const (
	// PromotionModeAutomatic automatically promotes new Preparations to active
	// Servings when all preconditions are satisfied.
	PromotionModeAutomatic PromotionMode = "Automatic"
	// PromotionModeManual requires explicit promotion of Preparations.
	PromotionModeManual PromotionMode = "Manual"
)

// OCISource defines the OCI artifact a Product is built from.
type OCISource struct {
	// url is the oci:// URL of the repository of the source artifact. The
	// Product connects to it through the Registry among its registryRefs
	// that covers it.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'oci'",message="must be a valid OCI URL"
	URL string `json:"url"`

	// version is the semantic version or tag of the artifact
	// The controller will resolve this to a digest
	// +kubebuilder:validation:Required
	Version string `json:"version"`

	// verify requires the artifact the version resolves to to carry a
	// trusted signature, by key or keyless against an expected signer
	// identity. A source that does not verify is not rendered.
	// +optional
	Verify *OCISourceVerification `json:"verify,omitempty"`
}

// VerificationProvider names the signature format a source is verified with.
// +kubebuilder:validation:Enum=cosign
type VerificationProvider string

// VerificationProviderCosign verifies cosign signatures and Sigstore bundles.
const VerificationProviderCosign VerificationProvider = "cosign"

// OCISourceVerification configures how the signature of a source artifact is
// verified. It follows the shape of Flux's OCIRepository spec.verify.
// +kubebuilder:validation:XValidation:rule="has(self.secretRef) != has(self.matchOIDCIdentity)",message="exactly one of secretRef or matchOIDCIdentity must be set"
// +kubebuilder:validation:XValidation:rule="!has(self.trustedRootSecretRef) || has(self.matchOIDCIdentity)",message="trustedRootSecretRef is only used with matchOIDCIdentity"
type OCISourceVerification struct {
	// provider is the signature format. Only "cosign" is supported.
	// +optional
	// +kubebuilder:default=cosign
	Provider VerificationProvider `json:"provider,omitempty"`

	// secretRef names a Secret in the Product's namespace holding public
	// keys. Every key whose name ends in ".pub" is tried; one verifying
	// signature suffices. Signatures are verified offline, without a
	// transparency log.
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`

	// matchOIDCIdentity verifies keyless signatures whose certificate was
	// issued to one of the listed identities.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	MatchOIDCIdentity []OIDCIdentityMatch `json:"matchOIDCIdentity,omitempty"`

	// trustedRootSecretRef names a Secret in the Product's namespace whose
	// key "trusted_root.json" holds the Sigstore trusted root keyless
	// signatures are verified against. When unset, the Sigstore public-good
	// instance is used through TUF, which needs network access; set it in
	// air-gapped clusters.
	// +optional
	TrustedRootSecretRef *corev1.LocalObjectReference `json:"trustedRootSecretRef,omitempty"`
}

// OIDCIdentityMatch matches the identity of a keyless signer. Both fields are
// RE2 regular expressions and are not anchored.
type OIDCIdentityMatch struct {
	// issuer matches the OIDC issuer of the signer's certificate, for
	// example "^https://token\.actions\.githubusercontent\.com$".
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Issuer string `json:"issuer"`

	// subject matches the subject of the signer's certificate, for example
	// the workflow that released the artifact.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Subject string `json:"subject"`
}

// HelmRender defines Helm-specific rendering options for the source artifact.
type HelmRender struct {
	// releaseName is the Helm release name passed to helm template.
	// Defaults to the Order's metadata.name when omitted.
	// +optional
	ReleaseName string `json:"releaseName,omitempty"`

	// namespace is the target namespace passed to helm template --namespace.
	// Defaults to the Order's metadata.namespace when omitted.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// includeCRDs controls whether CRDs are included in the rendered output.
	// Equivalent to helm template --include-crds.
	// +optional
	// +kubebuilder:default=false
	IncludeCRDs bool `json:"includeCRDs,omitempty"`

	// values holds inline Helm values. On a Product they are the base values;
	// an Order's spec.values are deep-merged over them.
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Values *apiextensionsv1.JSON `json:"values,omitempty"`
}

// FileLayout controls how a multi-file raw manifest artifact is stored
// in the rendered Preparation artifact.
// +kubebuilder:validation:Enum=Single;Multi
type FileLayout string

const (
	// FileLayoutSingle merges all YAML files into a single manifest.yaml.
	FileLayoutSingle FileLayout = "Single"
	// FileLayoutMulti keeps the artifact's individual files as they are.
	FileLayoutMulti FileLayout = "Multi"
)

// ManifestRender defines rendering options for raw manifest bundle sources.
type ManifestRender struct {
	// layout controls whether the artifact's YAML files are merged into a
	// single manifest.yaml (Single) or kept as separate files (Multi).
	// Artifacts containing a kustomization.yaml are never merged.
	// +optional
	// +kubebuilder:default=Single
	Layout FileLayout `json:"layout,omitempty"`
}

// Render defines optional rendering to apply to the source artifact.
// When absent, the source is treated as a pre-rendered manifest bundle.
// +kubebuilder:validation:XValidation:rule="!(has(self.helm) && has(self.manifest))",message="helm and manifest are mutually exclusive"
type Render struct {
	// helm renders the source OCI artifact as a Helm chart.
	// When set, the source must be a Helm chart in OCI format.
	// +optional
	Helm *HelmRender `json:"helm,omitempty"`

	// manifest configures rendering of a raw manifest bundle source.
	// +optional
	Manifest *ManifestRender `json:"manifest,omitempty"`
}

// OCIDestination defines the OCI repository an artifact is pushed to.
type OCIDestination struct {
	// url is the oci:// URL of the repository the artifact is pushed to.
	// When omitted, a repository of the bundled registry is used.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'oci'",message="must be a valid OCI URL"
	URL string `json:"url,omitempty"`
}

// PatchTarget identifies which resource to patch
type PatchTarget struct {
	// kind specifies the Kubernetes resource kind to patch
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`

	// name specifies the name of the resource to patch
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// namespace specifies the namespace of the resource (optional, defaults to same namespace as Order)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// Patch defines a modification to apply to a resource
type Patch struct {
	// target identifies which resource to patch
	// +kubebuilder:validation:Required
	Target PatchTarget `json:"target"`

	// set contains JSONPath expressions and their values to set
	// Keys are JSONPath expressions (e.g., ".spec.replicas")
	// Values are the desired values for those paths
	// +kubebuilder:validation:Required
	Set map[string]string `json:"set"`
}

// ProductRef references a Product by name in the Order's namespace.
type ProductRef struct {
	// name is the name of the Product. The Product must exist in the same
	// namespace as the Order.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// OrderSpec defines the desired state of Order.
// An Order is a variant of a Product: it renders the Product's source with
// the Product's configuration plus the Order's own values, patches, and edits.
type OrderSpec struct {
	// productRef references the Product in the Order's namespace that this
	// Order is a variant of. It cannot be changed after creation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="productRef is immutable"
	ProductRef ProductRef `json:"productRef"`

	// values holds Helm values deep-merged over the Product's
	// render.helm.values; the Order's values win. Only valid when the
	// Product renders with Helm, and subject to the Product's overrides.
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Values *apiextensionsv1.JSON `json:"values,omitempty"`

	// destination defines where the rendered Preparation artifact will be pushed.
	// When omitted, the in-cluster registry is used automatically.
	// +optional
	Destination *OCIDestination `json:"destination,omitempty"`

	// registryRefs lists Registries in the Order's namespace. Kokumi
	// connects to the destination through the one whose url covers it,
	// the longest when several do. The source, the images and the vendor
	// location belong to the Product, and its registryRefs connect to them.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=16
	RegistryRefs []corev1.LocalObjectReference `json:"registryRefs,omitempty"`

	// patches are merged over the Product's patches, per target, before
	// producing a Preparation. Subject to the Product's overrides.
	// +optional
	Patches []Patch `json:"patches,omitempty"`

	// edits defines UI-driven field modifications applied after patches.
	// They are intended for interactive changes made through the web UI and
	// are subject to the Product's patch overrides.
	// +optional
	Edits []Patch `json:"edits,omitempty"`

	// promotion configures how new Preparations become active Servings.
	// +optional
	Promotion *PromotionSpec `json:"promotion,omitempty"`
}

// PromotionSpec defines how new Preparations are promoted to active Servings.
type PromotionSpec struct {
	// mode controls how a newly created Preparation becomes the active Serving.
	// Automatic promotes it immediately; Manual requires explicit promotion.
	// +optional
	// +kubebuilder:default=Manual
	Mode PromotionMode `json:"mode,omitempty"`

	// approvals configures an approval gate on the promoted artifact. When
	// set, the Preparation is blocked from being served until enough
	// distinct approvers from the listed groups have approved it, in both
	// Automatic and Manual mode. When omitted, no approval gate applies.
	// The policy is copied into each Preparation when it is created, so a
	// change produces a new Preparation that needs fresh approvals.
	// +optional
	Approvals *ApprovalPolicy `json:"approvals,omitempty"`
}

// ApprovalPolicy defines the approval quorum required before a Preparation can be served.
type ApprovalPolicy struct {
	// requiredApprovals is the minimum number of distinct eligible approvers.
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=32
	RequiredApprovals int32 `json:"requiredApprovals"`

	// allowedGroups lists the groups whose members are eligible to approve.
	// +required
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=253
	AllowedGroups []string `json:"allowedGroups"`
}

// OrderStatus defines the observed state of Order.
type OrderStatus struct {
	// observedGeneration is the most recent generation observed by the controller
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// latestPreparationName is the name of the most recent immutable Preparation
	// produced from this Order
	// +optional
	LatestPreparationName string `json:"latestPreparationName,omitempty"`

	// latestArtifactDigest is the SHA256 digest of the OCI artifact produced by the
	// most recent Preparation. It is used as the pointer for the parentDigest of
	// the next Preparation.
	// +optional
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	LatestArtifactDigest string `json:"latestArtifactDigest,omitempty"`

	// latestConfigHash is a SHA-256 hash of the intent: the resolved source and
	// destination OCI URLs plus version, render, patches, edits, and approval
	// policy after merging the Order over its Product.
	// +optional
	LatestConfigHash string `json:"latestConfigHash,omitempty"`

	// registryAccess lists the registry locations the Order needs: where
	// Kokumi pushes the Preparation and through which Registry, where Argo
	// CD pulls it from, and where the kubelet pulls the images of the
	// latest Preparation from. Argo CD and the kubelet need their own
	// credentials for the locations listed for them.
	// +optional
	// +listType=map
	// +listMapKey=url
	// +listMapKey=operation
	// +listMapKey=client
	// +kubebuilder:validation:MaxItems=32
	RegistryAccess []RegistryAccess `json:"registryAccess,omitempty"`

	// conditions represent the current state of the Order resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].reason`
// +kubebuilder:printcolumn:name="Product",type=string,JSONPath=`.spec.productRef.name`
// +kubebuilder:printcolumn:name="Latest Preparation",type=string,JSONPath=`.status.latestPreparationName`
// +kubebuilder:printcolumn:name="Promotion",type=string,JSONPath=`.spec.promotion.mode`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:selectablefield:JSONPath=`.spec.productRef.name`

// Order is the Schema for the orders API.
// An Order is a variant of a Product, such as one environment of an
// application.
type Order struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Order
	// +required
	Spec OrderSpec `json:"spec"`

	// status defines the observed state of Order
	// +optional
	Status OrderStatus `json:"status,omitzero"`
}

// EffectivePromotionMode returns spec.promotion.mode, defaulting to Manual.
func (o *Order) EffectivePromotionMode() PromotionMode {
	if o.Spec.Promotion == nil || o.Spec.Promotion.Mode == "" {
		return PromotionModeManual
	}
	return o.Spec.Promotion.Mode
}

// ApprovalPolicy returns spec.promotion.approvals, or nil when no gate applies.
func (o *Order) ApprovalPolicy() *ApprovalPolicy {
	if o.Spec.Promotion == nil {
		return nil
	}
	return o.Spec.Promotion.Approvals
}

// +kubebuilder:object:root=true

// OrderList contains a list of Order
type OrderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Order `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Order{}, &OrderList{})
		return nil
	})
}
