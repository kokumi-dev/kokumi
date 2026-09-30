package server

import (
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// whoamiResponse is the JSON body for GET /api/v1/whoami. It reports the
// authenticated identity and whether the user may change Kitchen settings.
type whoamiResponse struct {
	Subject  string   `json:"subject"`
	Username string   `json:"username,omitempty"`
	Email    string   `json:"email,omitempty"`
	Provider string   `json:"provider,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	IsAdmin  bool     `json:"isAdmin"`
}

// handleWhoami handles GET /api/v1/whoami. The endpoint answers 200 with
// isAdmin=false for every authenticated user, even when the identity maps to
// no ServiceAccount: the UI only needs to know whether Settings should be
// offered, and the underlying RBAC check is authoritative.
func handleWhoami(deps *apiDeps, namespace string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		id := identityFromRequest(r)
		if id == nil {
			// Auth disabled: no identity exists, nothing to report.
			respondJSON(w, http.StatusOK, whoamiResponse{IsAdmin: false})
			return
		}

		resp := whoamiResponse{
			Subject:  id.Subject,
			Username: id.Username,
			Email:    id.Email,
			Provider: id.Provider,
			Groups:   id.Groups,
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			// No mapped ServiceAccount: the user is authenticated but has no
			// permissions, so they are not an admin.
			respondJSON(w, http.StatusOK, resp)
			return
		}

		allowed, err := uc.authorized(r.Context(), "update", "kitchens", namespace, deliveryv1alpha1.DefaultKitchenName)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to check kitchen permissions")
			return
		}
		resp.IsAdmin = allowed

		respondJSON(w, http.StatusOK, resp)
	}
}
