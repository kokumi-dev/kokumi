package server

import (
	"net/http"

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
