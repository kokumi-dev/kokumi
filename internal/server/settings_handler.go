package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/signing"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// schemeHTTP and schemeHTTPS are the only accepted URL schemes for
// argoCDURL and the OIDC issuer URL.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// maxTrustedKeys mirrors the MaxItems of spec.signing.trustedKeys.
const maxTrustedKeys = 16

// maxImageMirrors mirrors the MaxItems of spec.images.mirrors.
const maxImageMirrors = 32

// trustedKeyName mirrors the pattern of a trusted key's name.
var trustedKeyName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

// secretRefDTO is the JSON shape of a LocalObjectReference in the settings API.
type secretRefDTO struct {
	Name string `json:"name"`
}

// adminUserDTO mirrors spec.auth.adminUser of the Kitchen singleton.
type adminUserDTO struct {
	Enabled   *bool         `json:"enabled,omitempty"`
	Username  string        `json:"username,omitempty"`
	SecretRef *secretRefDTO `json:"secretRef,omitempty"`
}

// oidcDTO mirrors spec.auth.oidc of the Kitchen singleton.
type oidcDTO struct {
	IssuerURL       string        `json:"issuerURL"`
	ClientID        string        `json:"clientID"`
	ClientSecretRef *secretRefDTO `json:"clientSecretRef,omitempty"`
	UsernameClaim   string        `json:"usernameClaim,omitempty"`
	EmailClaim      string        `json:"emailClaim,omitempty"`
	GroupsClaim     string        `json:"groupsClaim,omitempty"`
	Scopes          []string      `json:"scopes,omitempty"`
}

// authDTO mirrors spec.auth of the Kitchen singleton.
type authDTO struct {
	AdminUser                *adminUserDTO `json:"adminUser,omitempty"`
	OIDC                     *oidcDTO      `json:"oidc,omitempty"`
	TokenSigningKeySecretRef *secretRefDTO `json:"tokenSigningKeySecretRef,omitempty"`
}

// trustedKeyDTO mirrors an entry of spec.signing.trustedKeys.
type trustedKeyDTO struct {
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
}

// signingSettingsDTO mirrors spec.signing of the Kitchen singleton.
type signingSettingsDTO struct {
	TrustedKeys               []trustedKeyDTO `json:"trustedKeys"`
	RequireSourceVerification bool            `json:"requireSourceVerification"`
}

// imageMirrorDTO mirrors an entry of spec.images.mirrors.
type imageMirrorDTO struct {
	Prefix   string `json:"prefix"`
	Location string `json:"location"`
}

// imagesSettingsDTO mirrors spec.images of the Kitchen singleton.
type imagesSettingsDTO struct {
	Mirrors []imageMirrorDTO `json:"mirrors"`
}

// settingsResponse is the JSON body for GET /api/v1/settings.
type settingsResponse struct {
	ArgoCDURL string             `json:"argoCDURL"`
	Auth      *authDTO           `json:"auth,omitempty"`
	Signing   signingSettingsDTO `json:"signing"`
	Images    imagesSettingsDTO  `json:"images"`
}

// settingsRequest is the JSON body for PUT /api/v1/settings. Top-level fields
// are optional: an absent field leaves the corresponding Kitchen spec part
// unchanged, so the Settings tabs can save independently. A present auth,
// signing or images object replaces the whole subtree.
type settingsRequest struct {
	ArgoCDURL *string             `json:"argoCDURL,omitempty"`
	Auth      *authDTO            `json:"auth,omitempty"`
	Signing   *signingSettingsDTO `json:"signing,omitempty"`
	Images    *imagesSettingsDTO  `json:"images,omitempty"`
}

// settingsToResponse converts the Kitchen spec to the settings response.
func settingsToResponse(spec deliveryv1alpha1.KitchenSpec) settingsResponse {
	return settingsResponse{
		ArgoCDURL: spec.ArgoCDURL,
		Auth:      authToDTO(spec.Auth),
		Signing:   signingToDTO(spec.Signing),
		Images:    imagesSettingsToDTO(spec.Images),
	}
}

// imagesSettingsToDTO converts a Kitchen spec.images to its DTO shape.
func imagesSettingsToDTO(images *deliveryv1alpha1.KitchenImages) imagesSettingsDTO {
	out := imagesSettingsDTO{Mirrors: []imageMirrorDTO{}}
	if images == nil {
		return out
	}
	for _, m := range images.Mirrors {
		out.Mirrors = append(out.Mirrors, imageMirrorDTO{Prefix: m.Prefix, Location: m.Location})
	}
	return out
}

// imagesSettingsFromDTO converts the request DTO to a Kitchen spec.images.
// Settings without mirrors clear it.
func imagesSettingsFromDTO(dto *imagesSettingsDTO) *deliveryv1alpha1.KitchenImages {
	if len(dto.Mirrors) == 0 {
		return nil
	}
	out := &deliveryv1alpha1.KitchenImages{}
	for _, m := range dto.Mirrors {
		out.Mirrors = append(out.Mirrors, deliveryv1alpha1.ImageMirror{Prefix: m.Prefix, Location: m.Location})
	}
	return out
}

// validateImagesDTO checks an images payload for what the UI can get wrong;
// the API server validates the rest.
func validateImagesDTO(dto *imagesSettingsDTO) error {
	if dto == nil {
		return nil
	}
	if len(dto.Mirrors) > maxImageMirrors {
		return fmt.Errorf("images.mirrors must contain at most %d entries", maxImageMirrors)
	}
	prefixes := map[string]bool{}
	for _, m := range dto.Mirrors {
		if m.Prefix == "" {
			return fmt.Errorf("images.mirrors prefix must not be empty")
		}
		if prefixes[m.Prefix] {
			return fmt.Errorf("images.mirrors prefix %q is used twice", m.Prefix)
		}
		prefixes[m.Prefix] = true
		if !strings.HasPrefix(m.Location, "oci://") || len(m.Location) == len("oci://") {
			return fmt.Errorf("images.mirrors location for %q must be an oci:// URL", m.Prefix)
		}
	}
	return nil
}

// secretRefToDTO converts a LocalObjectReference (nil-safe).
func secretRefToDTO(ref *corev1.LocalObjectReference) *secretRefDTO {
	if ref == nil {
		return nil
	}
	return &secretRefDTO{Name: ref.Name}
}

// authToDTO converts a Kitchen spec.auth to its DTO shape (nil-safe).
func authToDTO(auth *deliveryv1alpha1.KitchenAuth) *authDTO {
	if auth == nil {
		return nil
	}
	out := &authDTO{
		TokenSigningKeySecretRef: secretRefToDTO(auth.TokenSigningKeySecretRef),
	}
	if auth.AdminUser != nil {
		out.AdminUser = &adminUserDTO{
			Enabled:   auth.AdminUser.Enabled,
			Username:  auth.AdminUser.Username,
			SecretRef: secretRefToDTO(auth.AdminUser.SecretRef),
		}
	}
	if auth.OIDC != nil {
		out.OIDC = &oidcDTO{
			IssuerURL:       auth.OIDC.IssuerURL,
			ClientID:        auth.OIDC.ClientID,
			ClientSecretRef: secretRefToDTO(auth.OIDC.ClientSecretRef),
			UsernameClaim:   auth.OIDC.UsernameClaim,
			EmailClaim:      auth.OIDC.EmailClaim,
			GroupsClaim:     auth.OIDC.GroupsClaim,
			Scopes:          auth.OIDC.Scopes,
		}
	}
	return out
}

// secretRefFromDTO converts a DTO back to a LocalObjectReference (nil-safe).
func secretRefFromDTO(ref *secretRefDTO) *corev1.LocalObjectReference {
	if ref == nil {
		return nil
	}
	return &corev1.LocalObjectReference{Name: ref.Name}
}

// signingToDTO converts a Kitchen spec.signing to its DTO shape. An unset
// spec.signing trusts no further keys and requires nothing.
func signingToDTO(s *deliveryv1alpha1.KitchenSigning) signingSettingsDTO {
	out := signingSettingsDTO{TrustedKeys: []trustedKeyDTO{}}
	if s == nil {
		return out
	}
	for _, k := range s.TrustedKeys {
		out.TrustedKeys = append(out.TrustedKeys, trustedKeyDTO{Name: k.Name, PublicKey: k.PublicKey})
	}
	out.RequireSourceVerification = s.RequireSourceVerification
	return out
}

// applyAuth overwrites the target spec.auth subtree from the request DTO. nil
// sub-objects in the DTO clear the corresponding config.
func applyAuth(auth *deliveryv1alpha1.KitchenAuth, dto *authDTO) {
	if dto.AdminUser != nil {
		auth.AdminUser = &deliveryv1alpha1.AdminUserConfig{
			Enabled:   dto.AdminUser.Enabled,
			Username:  dto.AdminUser.Username,
			SecretRef: secretRefFromDTO(dto.AdminUser.SecretRef),
		}
	} else {
		auth.AdminUser = nil
	}
	if dto.OIDC != nil {
		auth.OIDC = &deliveryv1alpha1.OIDCConfig{
			IssuerURL:       dto.OIDC.IssuerURL,
			ClientID:        dto.OIDC.ClientID,
			ClientSecretRef: secretRefFromDTO(dto.OIDC.ClientSecretRef),
			UsernameClaim:   dto.OIDC.UsernameClaim,
			EmailClaim:      dto.OIDC.EmailClaim,
			GroupsClaim:     dto.OIDC.GroupsClaim,
			Scopes:          dto.OIDC.Scopes,
		}
	} else {
		auth.OIDC = nil
	}
	auth.TokenSigningKeySecretRef = secretRefFromDTO(dto.TokenSigningKeySecretRef)
}

// signingFromDTO converts the request DTO to a Kitchen spec.signing. Settings
// that trust nothing and require nothing clear it.
func signingFromDTO(dto *signingSettingsDTO) *deliveryv1alpha1.KitchenSigning {
	if len(dto.TrustedKeys) == 0 && !dto.RequireSourceVerification {
		return nil
	}
	out := &deliveryv1alpha1.KitchenSigning{RequireSourceVerification: dto.RequireSourceVerification}
	for _, k := range dto.TrustedKeys {
		out.TrustedKeys = append(out.TrustedKeys, deliveryv1alpha1.TrustedKey{Name: k.Name, PublicKey: k.PublicKey})
	}
	return out
}

// validateAuthDTO checks an auth payload against the constraints mirrored from
// the Kitchen CRD markers. It returns an error suitable for a 400 response.
func validateAuthDTO(dto *authDTO) error {
	if dto == nil {
		return nil
	}
	// An empty auth object would null the entire spec.auth subtree on write,
	// disabling every identity provider at once. Nothing intends that; require
	// at least one group to be addressed.
	if dto.AdminUser == nil && dto.OIDC == nil && dto.TokenSigningKeySecretRef == nil {
		return fmt.Errorf("auth must set adminUser, oidc, or tokenSigningKeySecretRef")
	}
	if dto.AdminUser != nil {
		if len(dto.AdminUser.Username) > 253 {
			return fmt.Errorf("adminUser.username must be at most 253 characters")
		}
		if strings.ContainsAny(dto.AdminUser.Username, "/ \t\r\n") {
			return fmt.Errorf("adminUser.username must not contain / or whitespace")
		}
		if ref := dto.AdminUser.SecretRef; ref != nil && ref.Name == "" {
			return fmt.Errorf("adminUser.secretRef.name must not be empty")
		}
	}
	if dto.OIDC != nil {
		u, err := url.Parse(dto.OIDC.IssuerURL)
		if err != nil || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) {
			return fmt.Errorf("oidc.issuerURL must be a valid http or https URL")
		}
		if dto.OIDC.ClientID == "" {
			return fmt.Errorf("oidc.clientID is required")
		}
		for _, c := range []struct{ label, val string }{
			{"usernameClaim", dto.OIDC.UsernameClaim},
			{"emailClaim", dto.OIDC.EmailClaim},
			{"groupsClaim", dto.OIDC.GroupsClaim},
		} {
			if len(c.val) > 253 {
				return fmt.Errorf("oidc.%s must be at most 253 characters", c.label)
			}
		}
		if len(dto.OIDC.Scopes) > 16 {
			return fmt.Errorf("oidc.scopes must contain at most 16 entries")
		}
		if ref := dto.OIDC.ClientSecretRef; ref != nil && ref.Name == "" {
			return fmt.Errorf("oidc.clientSecretRef.name must not be empty")
		}
	}
	if ref := dto.TokenSigningKeySecretRef; ref != nil && ref.Name == "" {
		return fmt.Errorf("tokenSigningKeySecretRef.name must not be empty")
	}
	return nil
}

// validateSigningDTO checks a signing payload. Every trusted key must parse:
// the controller signs and verifies nothing while one does not.
func validateSigningDTO(dto *signingSettingsDTO) error {
	if dto == nil {
		return nil
	}
	if len(dto.TrustedKeys) > maxTrustedKeys {
		return fmt.Errorf("signing.trustedKeys must contain at most %d entries", maxTrustedKeys)
	}
	names := map[string]bool{}
	keys := make([]signing.TrustedPublicKey, 0, len(dto.TrustedKeys))
	for _, k := range dto.TrustedKeys {
		if len(k.Name) > 63 || !trustedKeyName.MatchString(k.Name) {
			return fmt.Errorf("signing.trustedKeys name %q must be a lowercase DNS label of at most 63 characters", k.Name)
		}
		if names[k.Name] {
			return fmt.Errorf("signing.trustedKeys name %q is used twice", k.Name)
		}
		names[k.Name] = true
		keys = append(keys, signing.TrustedPublicKey{Name: k.Name, PEM: []byte(k.PublicKey)})
	}
	if _, err := signing.NewTrustSet(keys...); err != nil {
		return fmt.Errorf("signing.trustedKeys: %w", err)
	}
	return nil
}

// handleGetSettings handles GET /api/v1/settings.
// Returns the settings of the singleton Kitchen/default.
func handleGetSettings(deps *apiDeps, namespace string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		kitchen := &deliveryv1alpha1.Kitchen{}
		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}
		err = uc.get(r.Context(), types.NamespacedName{
			Namespace: namespace,
			Name:      deliveryv1alpha1.DefaultKitchenName,
		}, kitchen)
		if err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondJSON(w, http.StatusOK, settingsToResponse(deliveryv1alpha1.KitchenSpec{}))
				return
			}
			respondForbiddenOrError(w, err, "failed to get settings")
			return
		}

		respondJSON(w, http.StatusOK, settingsToResponse(kitchen.Spec))
	}
}

// handlePutSettings handles PUT /api/v1/settings.
// Updates argoCDURL and the auth, signing and images subtrees of the
// singleton Kitchen/default, creating it if absent. Absent request fields
// leave the Kitchen unchanged.
func handlePutSettings(deps *apiDeps, namespace string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req settingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.ArgoCDURL == nil && req.Auth == nil && req.Signing == nil && req.Images == nil {
			respondError(w, http.StatusBadRequest, "request must set argoCDURL, auth, signing or images")
			return
		}
		if err := validateAuthDTO(req.Auth); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := validateSigningDTO(req.Signing); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := validateImagesDTO(req.Images); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}

		var argoCDURL string
		if req.ArgoCDURL != nil {
			raw := strings.TrimSpace(*req.ArgoCDURL)
			if raw != "" {
				u, err := url.Parse(raw)
				if err != nil || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) {
					respondError(w, http.StatusBadRequest, "argoCDURL must be a valid http:// or https:// URL")
					return
				}
			}
			argoCDURL = raw
		}

		kitchen := &deliveryv1alpha1.Kitchen{}
		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}
		err = uc.get(r.Context(), types.NamespacedName{
			Namespace: namespace,
			Name:      deliveryv1alpha1.DefaultKitchenName,
		}, kitchen)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				respondForbiddenOrError(w, err, "failed to get settings")
				return
			}
			kitchen = &deliveryv1alpha1.Kitchen{}
			kitchen.Name = deliveryv1alpha1.DefaultKitchenName
			kitchen.Namespace = namespace
			if req.ArgoCDURL != nil {
				kitchen.Spec.ArgoCDURL = argoCDURL
			}
			if req.Auth != nil {
				kitchen.Spec.Auth = &deliveryv1alpha1.KitchenAuth{}
				applyAuth(kitchen.Spec.Auth, req.Auth)
			}
			if req.Signing != nil {
				kitchen.Spec.Signing = signingFromDTO(req.Signing)
			}
			if req.Images != nil {
				kitchen.Spec.Images = imagesSettingsFromDTO(req.Images)
			}
			if err := uc.create(r.Context(), kitchen, "kitchens"); err != nil {
				deps.logger.Error(err, "Failed to create Kitchen")
				respondForbiddenOrError(w, err, "failed to save settings")
				return
			}
			respondJSON(w, http.StatusOK, settingsToResponse(kitchen.Spec))
			return
		}

		// DeepCopy so later mutations (applyAuth writes through the shared
		// *KitchenAuth pointer) cannot alias the snapshot used for the no-op check.
		before := *kitchen.Spec.DeepCopy()
		if req.ArgoCDURL != nil {
			kitchen.Spec.ArgoCDURL = argoCDURL
		}
		if req.Auth != nil {
			if kitchen.Spec.Auth == nil {
				kitchen.Spec.Auth = &deliveryv1alpha1.KitchenAuth{}
			}
			applyAuth(kitchen.Spec.Auth, req.Auth)
		}
		if req.Signing != nil {
			kitchen.Spec.Signing = signingFromDTO(req.Signing)
		}
		if req.Images != nil {
			kitchen.Spec.Images = imagesSettingsFromDTO(req.Images)
		}
		if apiequality.Semantic.DeepEqual(&before, &kitchen.Spec) {
			respondJSON(w, http.StatusOK, settingsToResponse(kitchen.Spec))
			return
		}
		if err := uc.update(r.Context(), kitchen, "kitchens"); err != nil {
			deps.logger.Error(err, "Failed to update Kitchen")
			respondForbiddenOrError(w, err, "failed to save settings")
			return
		}

		respondJSON(w, http.StatusOK, settingsToResponse(kitchen.Spec))
	}
}
