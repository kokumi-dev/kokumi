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

const (
	DefaultKitchenName = "default"
)

// KitchenSpec defines the desired state of Kitchen
type KitchenSpec struct {
	// ArgoCDURL is the base URL of the Argo CD instance used to build deep
	// links on the Servings page. Empty means no links.
	// +kubebuilder:validation:XValidation:rule="self == '' || (isURL(self) && url(self).getScheme() in ['http', 'https'])",message="must be a valid http or https URL"
	// +optional
	ArgoCDURL string `json:"argoCDURL,omitempty"`

	// Auth configures UI/API authentication. The built-in admin account
	// (auth.adminUser) and an external OIDC provider (auth.oidc) can be enabled
	// independently; both may be active at once. When Auth is unset, no identity
	// provider is configured and the server runs without authentication.
	// +optional
	Auth *KitchenAuth `json:"auth,omitempty"`
}

// KitchenAuth groups the authentication configuration for the Kitchen server.
type KitchenAuth struct {
	// AdminUser configures the built-in admin account used for UI login.
	// +optional
	AdminUser *AdminUserConfig `json:"adminUser,omitempty"`

	// OIDC configures an external OpenID Connect identity provider for UI
	// login. When set, the login page offers an SSO button alongside (or
	// instead of) the admin account, depending on whether AdminUser is also
	// enabled.
	// +optional
	OIDC *OIDCConfig `json:"oidc,omitempty"`
}

// AdminUserConfig configures the built-in admin account used for UI login.
// Credentials are never stored in this object; they live in the referenced
// Secret (see SecretRef). This keeps the admin login usable while allowing it
// to be disabled (e.g. when an external OIDC provider takes over) or renamed.
type AdminUserConfig struct {
	// Enabled toggles the admin account. When false, admin login is disabled and
	// the server falls back to no authentication unless another identity
	// provider (e.g. OIDC) is configured. Defaults to true.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Username is the login name for the admin account. Defaults to "admin".
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[^/\s]+$`
	// +kubebuilder:default="admin"
	// +optional
	Username string `json:"username,omitempty"`

	// SecretRef points to the Secret holding the admin credentials. The Secret
	// must reside in the same namespace as the Kitchen. Recognized keys are
	// "password-hash" (bcrypt hash) and "signing-key" (HMAC key for JWTs).
	// Defaults to "kokumi-server-auth".
	// +kubebuilder:default={name:"kokumi-server-auth"}
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`
}

func (c *AdminUserConfig) IsEnabled() bool {
	if c == nil {
		return true
	}
	return c.Enabled == nil || *c.Enabled
}

// OIDCConfig configures an external OpenID Connect identity provider for UI
// login. The client secret lives in a referenced Secret.
type OIDCConfig struct {
	// IssuerURL is the base URL of the OIDC issuer (e.g.
	// "https://accounts.google.com"). The server discovers the authorization
	// and token endpoints from the issuer's well-known configuration.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() in ['http', 'https']",message="must be a valid http or https URL"
	IssuerURL string `json:"issuerURL"`

	// ClientID is the OAuth2/OIDC client identifier registered with the issuer.
	// +kubebuilder:validation:Required
	ClientID string `json:"clientID"`

	// ClientSecretRef points to the Secret holding the OAuth2 client secret in
	// the "client-secret" key. The Secret must reside in the same namespace as
	// the Kitchen.
	// Defaults to "kokumi-server-oidc".
	// +kubebuilder:default={name:"kokumi-server-oidc"}
	// +optional
	ClientSecretRef *corev1.LocalObjectReference `json:"clientSecretRef,omitempty"`

	// UsernameClaim is the ID-token claim used as the kokumi username.
	// Defaults to "email".
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:default=email
	// +optional
	UsernameClaim string `json:"usernameClaim,omitempty"`

	// Scopes is the list of OAuth2 scopes requested at login.
	// Defaults to ["openid", "profile", "email"].
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:default={openid,profile,email}
	// +optional
	Scopes []string `json:"scopes,omitempty"`
}

// KitchenStatus defines the observed state of Kitchen.
type KitchenStatus struct {
	// observedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the Kitchen resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].reason`
// +kubebuilder:printcolumn:name="Argo CD URL",type=string,JSONPath=`.spec.argoCDURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Kitchen is the Schema for the kitchens API
type Kitchen struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Kitchen
	// +required
	Spec KitchenSpec `json:"spec"`

	// status defines the observed state of Kitchen
	// +optional
	Status KitchenStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// KitchenList contains a list of Kitchen
type KitchenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Kitchen `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Kitchen{}, &KitchenList{})
		return nil
	})
}
