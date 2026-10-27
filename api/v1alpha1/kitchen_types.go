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

	// DefaultTokenSigningKeySecretName is the default name of the Secret holding
	// the HMAC signing key for session tokens. The Kitchen controller creates
	// this Secret with a generated key when it does not exist.
	DefaultTokenSigningKeySecretName = "kokumi-server-tokens"
)

// The signing key lives in a namespace of its own under a fixed name, so that
// RBAC and admission can keep everyone but the controller and cluster admins
// away from it without restricting Secrets anywhere else.
const (
	// SigningKeyNamespace is the namespace of the signing key Secret.
	SigningKeyNamespace = "kokumi-signing"

	// SigningKeySecretName is the Secret holding the key that signs every
	// artifact and attestation Kokumi writes, in the layout of cosign's
	// k8s:// key reference: "cosign.key" (encrypted PEM), "cosign.password"
	// and "cosign.pub". The Kitchen controller generates it when it does not
	// exist.
	SigningKeySecretName = "kokumi-signing-key"
)

// KitchenSpec defines the desired state of Kitchen
type KitchenSpec struct {
	// argoCDURL is the base URL of the Argo CD instance used to build deep
	// links on the Servings page. Empty means no links.
	// +kubebuilder:validation:XValidation:rule="self == '' || (isURL(self) && url(self).getScheme() in ['http', 'https'])",message="must be a valid http or https URL"
	// +optional
	ArgoCDURL string `json:"argoCDURL,omitempty"`

	// auth configures UI/API authentication. The built-in admin account
	// (auth.adminUser) and an external OIDC provider (auth.oidc) can be enabled
	// independently; both may be active at once. When Auth is unset, no identity
	// provider is configured and the server runs without authentication.
	// +optional
	Auth *KitchenAuth `json:"auth,omitempty"`

	// signing configures which keys Kokumi trusts besides its own signing
	// key, and whether the source of every Product must verify.
	// +optional
	Signing *KitchenSigning `json:"signing,omitempty"`

	// images configures how Kokumi reaches the registries of the container
	// images that rendered manifests reference.
	// +optional
	Images *KitchenImages `json:"images,omitempty"`

	// scanning enables scanning the images Products vendor for
	// vulnerabilities. When unset, nothing is scanned. An empty object
	// scans with the defaults.
	// +optional
	Scanning *KitchenScanning `json:"scanning,omitempty"`
}

// Default locations of the Trivy vulnerability databases.
const (
	// DefaultScanningDBRepository is the repository of the Trivy
	// vulnerability database, read at tag 2.
	DefaultScanningDBRepository = "oci://mirror.gcr.io/aquasec/trivy-db"
	// DefaultScanningJavaDBRepository is the repository of the Trivy Java
	// database, read at tag 1.
	DefaultScanningJavaDBRepository = "oci://mirror.gcr.io/aquasec/trivy-java-db"
)

// KitchenScanning configures vulnerability scanning. Kokumi scans each image
// a Product vendors once per digest, with the kokumi-scanner component, and
// attaches the signed results to the vendored image.
type KitchenScanning struct {
	// intervalSeconds is how often an image that a Preparation in use runs
	// is scanned again, so that new vulnerabilities show up on what is
	// already deployed.
	// +optional
	// +kubebuilder:default=86400
	// +kubebuilder:validation:Minimum=3600
	// +kubebuilder:validation:Maximum=2592000
	IntervalSeconds int32 `json:"intervalSeconds,omitempty"`

	// maxDatabaseAgeSeconds is how old the vulnerability database may be.
	// Scans with an older one still produce reports, but the Product
	// reports them as DatabaseStale, so that an air-gapped installation
	// notices a database it forgot to update.
	// +optional
	// +kubebuilder:default=604800
	// +kubebuilder:validation:Minimum=3600
	MaxDatabaseAgeSeconds int32 `json:"maxDatabaseAgeSeconds,omitempty"`

	// dbRepository is the oci:// URL of the Trivy vulnerability database.
	// A repository is read at tag 2; a URL with a tag or digest pins the
	// database, so that an update is a reviewed change. Defaults to
	// oci://mirror.gcr.io/aquasec/trivy-db. Set it to a mirror in
	// air-gapped clusters.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'oci'",message="must be a valid OCI URL"
	DBRepository string `json:"dbRepository,omitempty"`

	// javaDBRepository is the oci:// URL of the Trivy Java database, which
	// identifies Java archives. A repository is read at tag 1. It is only
	// downloaded when an image holds Java archives, and such images cannot
	// be scanned without it. Defaults to
	// oci://mirror.gcr.io/aquasec/trivy-java-db.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'oci'",message="must be a valid OCI URL"
	JavaDBRepository string `json:"javaDBRepository,omitempty"`

	// registryRefs lists Registries in the install namespace that connect
	// to the database repositories, for mirrors that need credentials, a
	// CA or plain HTTP. The public default and the bundled registry need
	// none.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=16
	RegistryRefs []corev1.LocalObjectReference `json:"registryRefs,omitempty"`
}

// EffectiveIntervalSeconds returns intervalSeconds, defaulting to a day.
func (s *KitchenScanning) EffectiveIntervalSeconds() int32 {
	if s == nil || s.IntervalSeconds <= 0 {
		return 86400
	}
	return s.IntervalSeconds
}

// EffectiveMaxDatabaseAgeSeconds returns maxDatabaseAgeSeconds, defaulting
// to a week.
func (s *KitchenScanning) EffectiveMaxDatabaseAgeSeconds() int32 {
	if s == nil || s.MaxDatabaseAgeSeconds <= 0 {
		return 604800
	}
	return s.MaxDatabaseAgeSeconds
}

// EffectiveDBRepository returns dbRepository, defaulting to
// DefaultScanningDBRepository.
func (s *KitchenScanning) EffectiveDBRepository() string {
	if s == nil || s.DBRepository == "" {
		return DefaultScanningDBRepository
	}
	return s.DBRepository
}

// EffectiveJavaDBRepository returns javaDBRepository, defaulting to
// DefaultScanningJavaDBRepository.
func (s *KitchenScanning) EffectiveJavaDBRepository() string {
	if s == nil || s.JavaDBRepository == "" {
		return DefaultScanningJavaDBRepository
	}
	return s.JavaDBRepository
}

// KitchenImages configures registry access for container images.
type KitchenImages struct {
	// mirrors redirects image lookups and copies to mirror registries. Like
	// a registries.conf prefix with a location, the mirror with the longest
	// prefix of an image's normalized name replaces that prefix, without
	// falling back to the original registry. Manifests and records keep the
	// original name, so nodes pull through their own mirror configuration.
	// +optional
	// +listType=map
	// +listMapKey=prefix
	// +kubebuilder:validation:MaxItems=32
	Mirrors []ImageMirror `json:"mirrors,omitempty"`
}

// ImageMirror maps a repository prefix to the location of its mirror.
type ImageMirror struct {
	// prefix is a normalized repository prefix, matched at path boundaries:
	// a registry such as "docker.io" or a namespace such as
	// "docker.io/library". It starts with a registry host, as every
	// normalized name does.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.:]*[a-z0-9])?(/[a-z0-9]+([._-][a-z0-9]+)*)*$`
	// +kubebuilder:validation:XValidation:rule="self.matches('^(localhost|[^/]*[.:][^/]*)(/|$)')",message="must start with a registry host, such as docker.io"
	Prefix string `json:"prefix"`

	// location is the oci:// URL the prefix is replaced with, for example
	// "oci://harbor.example.com/dockerhub" for a pull-through cache.
	// A Product connects to it through the Registry among its
	// registryRefs that covers it.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'oci'",message="must be a valid OCI URL"
	Location string `json:"location"`
}

// KitchenSigning configures the trust of the Kitchen. The signing key itself
// is the Secret named by SigningKeySecretName.
type KitchenSigning struct {
	// trustedKeys lists public keys Kokumi trusts for its own artifacts in
	// addition to the current signing key: the key used before a rotation,
	// or the key of another installation whose releases are imported.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=16
	TrustedKeys []TrustedKey `json:"trustedKeys,omitempty"`

	// requireSourceVerification requires the source of every Product to
	// verify, either through the Product's spec.source.verify or by a
	// signature from a key the Kitchen trusts. A Product whose source does
	// not verify is not ready, and its Orders render nothing.
	// +optional
	RequireSourceVerification bool `json:"requireSourceVerification,omitempty"`
}

// TrustedKey is a public key Kokumi trusts for its own artifacts.
type TrustedKey struct {
	// name identifies the key, for example "before-2026-10".
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	Name string `json:"name"`

	// publicKey is the PEM-encoded public key, as in cosign.pub.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	// +kubebuilder:validation:XValidation:rule="self.startsWith('-----BEGIN PUBLIC KEY-----')",message="must be a PEM-encoded public key"
	PublicKey string `json:"publicKey"`
}

// KitchenAuth groups the authentication configuration for the Kitchen server.
type KitchenAuth struct {
	// adminUser configures the built-in admin account used for UI login.
	// +optional
	AdminUser *AdminUserConfig `json:"adminUser,omitempty"`

	// oidc configures an external OpenID Connect identity provider for UI
	// login. When set, the login page offers an SSO button alongside (or
	// instead of) the admin account, depending on whether AdminUser is also
	// enabled.
	// +optional
	OIDC *OIDCConfig `json:"oidc,omitempty"`

	// tokenSigningKeySecretRef points to the Secret holding the HMAC signing
	// key (key "signing-key") used to sign all session tokens, for both admin
	// and OIDC sessions. The Secret must reside in the same namespace as the
	// Kitchen. When the referenced Secret does not exist, the controller
	// creates it with a generated random key.
	// Defaults to "kokumi-server-tokens".
	// +kubebuilder:default={name:"kokumi-server-tokens"}
	// +optional
	TokenSigningKeySecretRef *corev1.LocalObjectReference `json:"tokenSigningKeySecretRef,omitempty"`
}

// AdminUserConfig configures the built-in admin account used for UI login.
// Credentials are never stored in this object; they live in the referenced
// Secret (see SecretRef). This keeps the admin login usable while allowing it
// to be disabled (e.g. when an external OIDC provider takes over) or renamed.
type AdminUserConfig struct {
	// enabled toggles the admin account. When false, admin login is disabled and
	// the server falls back to no authentication unless another identity
	// provider (e.g. OIDC) is configured. Defaults to true.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// username is the login name for the admin account. Defaults to "admin".
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[^/\s]+$`
	// +kubebuilder:default="admin"
	// +optional
	Username string `json:"username,omitempty"`

	// secretRef points to the Secret holding the admin credentials. The Secret
	// must reside in the same namespace as the Kitchen. The recognized key is
	// "password-hash" (bcrypt hash); "username" may optionally override the
	// configured username. Defaults to "kokumi-server-auth".
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
	// issuerURL is the base URL of the OIDC issuer (e.g.
	// "https://accounts.google.com"). The server discovers the authorization
	// and token endpoints from the issuer's well-known configuration.
	// +required
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() in ['http', 'https']",message="must be a valid http or https URL"
	IssuerURL string `json:"issuerURL"`

	// clientID is the OAuth2/OIDC client identifier registered with the issuer.
	// +required
	ClientID string `json:"clientID"`

	// clientSecretRef points to the Secret holding the OAuth2 client secret in
	// the "client-secret" key. The Secret must reside in the same namespace as
	// the Kitchen.
	// Defaults to "kokumi-server-oidc".
	// +kubebuilder:default={name:"kokumi-server-oidc"}
	// +optional
	ClientSecretRef *corev1.LocalObjectReference `json:"clientSecretRef,omitempty"`

	// usernameClaim is the ID-token claim used as the kokumi username.
	// Defaults to "email".
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:default=email
	// +optional
	UsernameClaim string `json:"usernameClaim,omitempty"`

	// emailClaim is the ID-token claim used as the user's email for
	// ServiceAccount identity mapping (kokumi.dev/identity-email). Supports
	// dotted paths for nested claims (e.g. "user.email"). Defaults to "email".
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:default=email
	// +optional
	EmailClaim string `json:"emailClaim,omitempty"`

	// groupsClaim is the ID-token claim holding the user's group memberships
	// (a list of strings) for ServiceAccount identity mapping
	// (kokumi.dev/identity-groups). Supports dotted paths for nested claims
	// (e.g. "realm_access.roles" for Keycloak). Defaults to "groups".
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:default=groups
	// +optional
	GroupsClaim string `json:"groupsClaim,omitempty"`

	// scopes is the list of OAuth2 scopes requested at login.
	// Defaults to ["openid", "profile", "email"].
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:default={openid,profile,email}
	// +optional
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=atomic
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

	// signing publishes the public half of the signing key. Absent until the
	// controller has loaded the key.
	// +optional
	Signing *KitchenSigningStatus `json:"signing,omitempty"`
}

// KitchenSigningStatus is the public half of the Kitchen signing key.
type KitchenSigningStatus struct {
	// keyID identifies the key: the base64-encoded SHA-256 of its DER
	// public key, as recorded in the hint of every bundle it signs.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	KeyID string `json:"keyID"`

	// publicKey is the PEM-encoded public key, as in cosign.pub. It verifies
	// every artifact and attestation Kokumi writes, for example with
	// "cosign verify --key".
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	PublicKey string `json:"publicKey"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=kokumi
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].reason`
// +kubebuilder:printcolumn:name="Key ID",type=string,JSONPath=`.status.signing.keyID`
// +kubebuilder:printcolumn:name="Argo CD URL",type=string,JSONPath=`.spec.argoCDURL`,priority=1
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

// GetConditions returns the status conditions of the Kitchen.
func (k *Kitchen) GetConditions() []metav1.Condition {
	return k.Status.Conditions
}

// SetConditions sets the status conditions of the Kitchen.
func (k *Kitchen) SetConditions(conditions []metav1.Condition) {
	k.Status.Conditions = conditions
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
