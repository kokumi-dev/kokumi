package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
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

// settingsResponse is the JSON body for GET /api/v1/settings.
type settingsResponse struct {
	ArgoCDURL string   `json:"argoCDURL"`
	Auth      *authDTO `json:"auth,omitempty"`
}

// settingsRequest is the JSON body for PUT /api/v1/settings. Top-level fields
// are optional: an absent field leaves the corresponding Kitchen spec part
// unchanged, so the Settings tabs can save independently. A present auth
// object replaces the whole spec.auth subtree.
type settingsRequest struct {
	ArgoCDURL *string  `json:"argoCDURL,omitempty"`
	Auth      *authDTO `json:"auth,omitempty"`
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
				respondJSON(w, http.StatusOK, settingsResponse{})
				return
			}
			respondForbiddenOrError(w, err, "failed to get settings")
			return
		}

		respondJSON(w, http.StatusOK, settingsResponse{
			ArgoCDURL: kitchen.Spec.ArgoCDURL,
			Auth:      authToDTO(kitchen.Spec.Auth),
		})
	}
}

// handlePutSettings handles PUT /api/v1/settings.
// Updates argoCDURL and/or the auth subtree of the singleton Kitchen/default,
// creating it if absent. Absent request fields leave the Kitchen unchanged.
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
		if req.ArgoCDURL == nil && req.Auth == nil {
			respondError(w, http.StatusBadRequest, "request must set argoCDURL or auth")
			return
		}
		if err := validateAuthDTO(req.Auth); err != nil {
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
			if err := uc.create(r.Context(), kitchen, "kitchens"); err != nil {
				deps.logger.Error(err, "Failed to create Kitchen")
				respondForbiddenOrError(w, err, "failed to save settings")
				return
			}
			respondJSON(w, http.StatusOK, settingsResponse{
				ArgoCDURL: kitchen.Spec.ArgoCDURL,
				Auth:      authToDTO(kitchen.Spec.Auth),
			})
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
		if apiequality.Semantic.DeepEqual(&before, &kitchen.Spec) {
			respondJSON(w, http.StatusOK, settingsResponse{
				ArgoCDURL: kitchen.Spec.ArgoCDURL,
				Auth:      authToDTO(kitchen.Spec.Auth),
			})
			return
		}
		if err := uc.update(r.Context(), kitchen, "kitchens"); err != nil {
			deps.logger.Error(err, "Failed to update Kitchen")
			respondForbiddenOrError(w, err, "failed to save settings")
			return
		}

		respondJSON(w, http.StatusOK, settingsResponse{
			ArgoCDURL: kitchen.Spec.ArgoCDURL,
			Auth:      authToDTO(kitchen.Spec.Auth),
		})
	}
}
