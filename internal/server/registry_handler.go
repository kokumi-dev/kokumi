package server

import (
	"net/http"
	"strings"

	"github.com/kokumi-dev/kokumi/internal/service"
)

// handleGetDefaultRegistry handles GET /api/v1/registry/default.
// It returns the base URL of the in-cluster OCI registry so the UI can
// compute placeholder destination paths without hardcoding the host.
func handleGetDefaultRegistry() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"baseURL": service.DefaultRegistryHost})
	}
}

// handleListRegistryTags handles GET /api/v1/registry/tags?ref=<oci-ref>.
// It strips the oci:// scheme prefix if present, fetches tags from the registry
// and returns {"tags": [...]}.
func handleListRegistryTags(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		ref := r.URL.Query().Get("ref")
		if ref == "" {
			respondError(w, http.StatusBadRequest, "ref query parameter is required")
			return
		}

		ref = strings.TrimPrefix(ref, "oci://")
		if ref == "" {
			respondError(w, http.StatusBadRequest, "ref is empty after stripping scheme")
			return
		}

		tags, err := deps.ociClient.ListTags(r.Context(), ref)
		if err != nil {
			deps.logger.Error(err, "Failed to list tags", "ref", ref)
			respondError(w, http.StatusBadGateway, "could not list tags: "+err.Error())
			return
		}

		respondJSON(w, http.StatusOK, map[string][]string{"tags": tags})
	}
}
