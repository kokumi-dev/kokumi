package server

import (
	"encoding/json"
	"net/http"

	"github.com/kokumi-dev/kokumi/internal/version"
)

// InfoResponse is the response body for GET /api/v1/info.
type InfoResponse struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// HandleInfo handles GET /api/v1/info.
func handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(InfoResponse{
		Name:    "kokumi",
		Version: version.Version,
	}); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("OK"))
}

func handleReadyz(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("OK"))
}
