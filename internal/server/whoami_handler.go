package server

import (
	"net/http"
)

// whoamiResponse is the JSON body for GET /api/v1/whoami. It reports the
// authenticated identity and the capabilities the UI should offer.
type whoamiResponse struct {
	Subject  string   `json:"subject"`
	Username string   `json:"username,omitempty"`
	Email    string   `json:"email,omitempty"`
	Provider string   `json:"provider,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	// Capabilities are sorted and never null; see capabilityCatalog.
	Capabilities []Capability `json:"capabilities"`
	// Undetermined lists the capabilities whose RBAC checks failed, sorted and
	// never null. They are not in Capabilities; the UI may keep its previous
	// answer for them instead of revoking them.
	Undetermined []Capability `json:"undetermined"`
}

// handleWhoami handles GET /api/v1/whoami. The endpoint answers 200 for every
// authenticated user, with no capabilities when the identity maps to no
// ServiceAccount: the UI only needs to know which features to offer, and the
// underlying RBAC checks are authoritative.
func handleWhoami(deps *apiDeps, namespace string) http.HandlerFunc {
	rules := capabilityCatalog(namespace)
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}
		// The answer depends on the caller and on live RBAC.
		w.Header().Set("Cache-Control", "no-store")

		resp := whoamiResponse{Capabilities: []Capability{}, Undetermined: []Capability{}}
		id := identityFromRequest(r)
		if id == nil {
			// Auth disabled: no identity exists, nothing to report.
			respondJSON(w, http.StatusOK, resp)
			return
		}
		resp.Subject = id.Subject
		resp.Username = id.Username
		resp.Email = id.Email
		resp.Provider = id.Provider
		resp.Groups = id.Groups

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			// No mapped ServiceAccount: the user is authenticated but has no
			// permissions, so no capabilities.
			respondJSON(w, http.StatusOK, resp)
			return
		}

		resp.Capabilities, resp.Undetermined, err = uc.capabilities(r.Context(), id, rules)
		if err != nil {
			// Usually a mapped ServiceAccount kokumi-server may not impersonate.
			// The other ServiceAccounts still count, so answer with what is known.
			deps.logger.Error(err, "Could not evaluate all capabilities",
				"subject", id.Subject, "undetermined", resp.Undetermined)
		}

		respondJSON(w, http.StatusOK, resp)
	}
}
