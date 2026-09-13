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

// OverridePolicyType controls whether consumers may override values or patches.
// +kubebuilder:validation:Enum=All;Restricted;None
type OverridePolicyType string

const (
	// OverridePolicyAll allows consumers to set any value or patch.
	OverridePolicyAll OverridePolicyType = "All"
	// OverridePolicyRestricted allows only the explicitly listed overrides.
	OverridePolicyRestricted OverridePolicyType = "Restricted"
	// OverridePolicyNone forbids all consumer overrides.
	OverridePolicyNone OverridePolicyType = "None"
)

// ValueOverridePolicy defines what Helm values consumers may override.
// +kubebuilder:validation:XValidation:rule="self.policy != 'Restricted' || (has(self.allowed) && size(self.allowed) > 0)",message="allowed must not be empty when policy is Restricted"
type ValueOverridePolicy struct {
	// policy controls how consumers may override Helm values.
	// All: any value path is allowed.
	// Restricted: only paths listed in allowed are permitted.
	// None: no value overrides are permitted.
	// +kubebuilder:validation:Required
	// +kubebuilder:default=None
	Policy OverridePolicyType `json:"policy"`

	// allowed lists the dot-separated Helm value paths that consumers may set
	// (e.g. "ui.message", "replicaCount"). Only used when policy is Restricted.
	// +optional
	Allowed []string `json:"allowed,omitempty"`
}

// AllowedPatchTarget defines which patch target and JSON paths consumers may use.
type AllowedPatchTarget struct {
	// target identifies the resource that consumers may patch.
	// +kubebuilder:validation:Required
	Target PatchTarget `json:"target"`

	// paths lists the allowed JSONPath expressions within the target
	// (e.g. ".spec.replicas").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Paths []string `json:"paths"`
}

// PatchOverridePolicy defines what patches consumers may apply.
// +kubebuilder:validation:XValidation:rule="self.policy != 'Restricted' || (has(self.allowed) && size(self.allowed) > 0)",message="allowed must not be empty when policy is Restricted"
type PatchOverridePolicy struct {
	// policy controls how consumers may apply patches.
	// All: any patch target and path is allowed.
	// Restricted: only the targets and paths listed in allowed are permitted.
	// None: no patch overrides are permitted.
	// +kubebuilder:validation:Required
	// +kubebuilder:default=None
	Policy OverridePolicyType `json:"policy"`

	// allowed lists the patch targets and their allowed JSON paths.
	// Only used when policy is Restricted.
	// +optional
	Allowed []AllowedPatchTarget `json:"allowed,omitempty"`
}

// OverridePolicy defines what consumers (Orders) may override when using this Menu.
type OverridePolicy struct {
	// values controls Helm value overrides.
	// +kubebuilder:validation:Required
	Values ValueOverridePolicy `json:"values"`

	// patches controls patch overrides.
	// +kubebuilder:validation:Required
	Patches PatchOverridePolicy `json:"patches"`
}

// MenuDefaults defines default values that consuming Orders inherit.
type MenuDefaults struct {
	// autoDeploy is the default autoDeploy value for Orders using this Menu.
	// Orders may override this.
	// +optional
	// +kubebuilder:default=Disabled
	AutoDeploy AutoDeployPolicy `json:"autoDeploy,omitempty"`
}

// MenuSpec defines the desired state of Menu.
// A Menu acts as a reusable, operator-managed template that pins the source,
// base configuration, and override constraints for consuming Orders.
type MenuSpec struct {
	// source defines the immutable OCI artifact that consuming Orders will use.
	// Consumers cannot change the source or version.
	// +kubebuilder:validation:Required
	Source OCISource `json:"source"`

	// vendor optionally configures copying the source artifact to another OCI
	// registry. When set, consuming Orders use the vendored copy.
	// +optional
	Vendor *VendorSpec `json:"vendor,omitempty"`

	// render defines optional rendering configuration for the source artifact.
	// When absent the source is treated as a pre-rendered manifest bundle.
	// Consumers cannot change the render type.
	// +optional
	Render *Render `json:"render,omitempty"`

	// patches defines base patches always applied to consuming Orders.
	// These are merged with (and may be overridden by) consumer patches
	// when the override policy allows it.
	// +optional
	Patches []Patch `json:"patches,omitempty"`

	// overrides defines what consumers (Orders) are allowed to customize.
	// +kubebuilder:validation:Required
	Overrides OverridePolicy `json:"overrides"`

	// defaults defines default values inherited by consuming Orders.
	// +optional
	Defaults MenuDefaults `json:"defaults,omitempty"`
}

// VendorSpec configures copying the source artifact to another OCI registry.
type VendorSpec struct {
	// destination is the registry the source artifact is copied to.
	// +kubebuilder:validation:Required
	Destination VendorDestination `json:"destination"`
}

// VendorDestination defines where the vendored artifact is pushed.
// Exactly one of oci or pantryRef must be set.
// +kubebuilder:validation:XValidation:rule="(has(self.oci) && !has(self.pantryRef)) || (!has(self.oci) && has(self.pantryRef))",message="exactly one of oci or pantryRef must be set"
type VendorDestination struct {
	// oci is the full OCI URL the vendored artifact is pushed to.
	// Mutually exclusive with pantryRef.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == '' || (isURL(self) && url(self).getScheme() == 'oci')",message="must be a valid OCI URL"
	OCI string `json:"oci,omitempty"`

	// pantryRef references a Pantry resource whose URL is used as the
	// vendored destination. Mutually exclusive with oci.
	// +optional
	PantryRef *PantryRef `json:"pantryRef,omitempty"`
}

// MenuSourceStatus is the consumable source address advertised by the Menu.
// Orders consume exactly what is published here.
type MenuSourceStatus struct {
	// oci is the full OCI URL of the consumable artifact.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'oci'",message="must be a valid OCI URL"
	OCI string `json:"oci"`

	// version is the tag of the consumable artifact.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// pantryRef references the Pantry providing pull credentials for oci.
	// Absent when the registry is anonymously accessible.
	// +optional
	PantryRef *PantryRef `json:"pantryRef,omitempty"`

	// digest is the resolved digest of the artifact. Empty when resolution
	// was best-effort and the registry could not be reached.
	// +optional
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest,omitempty"`
}

// MenuStatus defines the observed state of Menu.
type MenuStatus struct {
	// observedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// source is the consumable source address for Orders. Absent until the
	// Menu controller has resolved (and, if configured, vendored) the source.
	// +optional
	Source *MenuSourceStatus `json:"source,omitempty"`

	// conditions represent the current state of the Menu resource.
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
// +kubebuilder:printcolumn:name="Values Policy",type=string,JSONPath=`.spec.overrides.values.policy`,priority=1
// +kubebuilder:printcolumn:name="Patches Policy",type=string,JSONPath=`.spec.overrides.patches.policy`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Menu is the Schema for the menus API.
// A Menu is a namespace-scoped, reusable template that operators define to pin
// a source artifact, base configuration, and override constraints.
// Developers consume Menus by referencing them from Orders in the same namespace.
type Menu struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Menu
	// +required
	Spec MenuSpec `json:"spec"`

	// status defines the observed state of Menu
	// +optional
	Status MenuStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// MenuList contains a list of Menu
type MenuList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Menu `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Menu{}, &MenuList{})
		return nil
	})
}
