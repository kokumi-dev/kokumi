package server

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/renderer"
	"github.com/kokumi-dev/kokumi/internal/service"
	"github.com/spf13/afero"
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

// chartInfoResponse is the JSON shape returned by GET /api/v1/registry/chart-info.
type chartInfoResponse struct {
	IsHelm        bool   `json:"isHelm"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	ChartVersion  string `json:"chartVersion,omitempty"`
	DefaultValues string `json:"defaultValues,omitempty"`
	Readme        string `json:"readme,omitempty"`
	HasSchema     bool   `json:"hasSchema,omitempty"`
}

// handleGetChartInfo handles GET /api/v1/registry/chart-info?ref=<oci-ref>&version=<tag>.
// It pulls the OCI artifact, checks whether it is a Helm chart, and when it is,
// returns the chart name, description, chart version, default values YAML,
// README content, and whether a JSON schema is present.
// For non-Helm artifacts it returns {"isHelm": false}.
func handleGetChartInfo(deps *apiDeps) http.HandlerFunc {
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
		version := r.URL.Query().Get("version")
		if version == "" {
			respondError(w, http.StatusBadRequest, "version query parameter is required")
			return
		}

		ref = strings.TrimPrefix(ref, "oci://")
		if ref == "" {
			respondError(w, http.StatusBadRequest, "ref is empty after stripping scheme")
			return
		}

		tmpDir, err := afero.TempDir(deps.fs, "", "kokumi-chart-info-*")
		if err != nil {
			deps.logger.Error(err, "Failed to create temp directory")
			respondError(w, http.StatusInternalServerError, "could not create temp directory")
			return
		}
		defer deps.fs.RemoveAll(tmpDir) //nolint:errcheck

		mediaType, _, err := deps.ociClient.Pull(r.Context(), ref, version, tmpDir)
		if err != nil {
			deps.logger.Error(err, "Failed to pull OCI artifact", "ref", ref, "version", version)
			respondError(w, http.StatusBadGateway, "could not pull artifact: "+err.Error())
			return
		}

		if mediaType != oci.HelmChartLayerMediaType {
			respondJSON(w, http.StatusOK, chartInfoResponse{IsHelm: false})
			return
		}

		info, err := renderer.InspectChart(filepath.Join(tmpDir, "chart.tgz"))
		if err != nil {
			deps.logger.Error(err, "Failed to inspect Helm chart", "ref", ref, "version", version)
			respondError(w, http.StatusBadGateway, "could not inspect chart: "+err.Error())
			return
		}

		respondJSON(w, http.StatusOK, chartInfoResponse{
			IsHelm:        true,
			Name:          info.Name,
			Description:   info.Description,
			ChartVersion:  info.ChartVersion,
			DefaultValues: info.DefaultValues,
			Readme:        info.Readme,
			HasSchema:     info.HasSchema,
		})
	}
}
