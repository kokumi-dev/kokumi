package server

import (
	"context"
	"net/http"

	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// handleGetDefaultRegistry handles GET /api/v1/registry/default.
// It returns the base URL of the in-cluster OCI registry so the UI can
// compute placeholder destination paths without hardcoding the host.
func handleGetDefaultRegistry() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"baseURL": artifact.DefaultRegistryHost})
	}
}

// handleListRegistryTags handles GET /api/v1/registry/tags?ref=<oci-ref>.
// Optional query parameters pantryName and pantryNamespace select a specific
// Pantry to use for authenticated access. When omitted the shared
// unauthenticated client is used. The ref is parsed and validated via
// oci.Parse, then all tags are fetched from the registry and returned
// as {"tags": [...]}.
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

		ociRef, err := oci.Parse(ref)
		if err != nil {
			respondError(w, http.StatusBadRequest, "unexpected format for ref")
			return
		}

		ociClient := ociClientForPantryRef(r.Context(), deps, r, r.URL.Query().Get("pantryName"), r.URL.Query().Get("pantryNamespace"))

		tags, err := ociClient.ListTags(r.Context(), ociRef)
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

		ociRef, ok := parseRefVersionQuery(w, r)
		if !ok {
			return
		}

		ociClient := ociClientForPantryRef(r.Context(), deps, r, r.URL.Query().Get("pantryName"), r.URL.Query().Get("pantryNamespace"))

		result, err := deps.store.Inspect(r.Context(), ociRef, ociClient)
		if err != nil {
			deps.logger.Error(err, "Failed to inspect artifact", "ref", ociRef)
			respondError(w, http.StatusBadGateway, "could not pull artifact: "+err.Error())
			return
		}

		if !result.IsHelm {
			respondJSON(w, http.StatusOK, chartInfoResponse{IsHelm: false})
			return
		}

		respondJSON(w, http.StatusOK, chartInfoResponse{
			IsHelm:        true,
			Name:          result.ChartInfo.Name,
			Description:   result.ChartInfo.Description,
			ChartVersion:  result.ChartInfo.ChartVersion,
			DefaultValues: result.ChartInfo.DefaultValues,
			Readme:        result.ChartInfo.Readme,
			HasSchema:     result.ChartInfo.HasSchema,
		})
	}
}

// handleGetRegistryArtifact handles GET /api/v1/registry/artifact?ref=<oci-ref>&version=<tag>.
// It pulls the OCI artifact at the given tag, resolves its manifest digest, and
// classifies it as a Helm chart or a pre-rendered manifest bundle. For Helm
// charts it returns chart metadata, the default values, the README, and the
// manifest rendered with the default values. For manifest bundles it returns the
// concatenated manifest YAML. Optional pantryName/pantryNamespace select a
// Pantry for authenticated access.
func handleGetRegistryArtifact(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		ociRef, ok := parseRefVersionQuery(w, r)
		if !ok {
			return
		}

		ociClient := ociClientForPantryRef(r.Context(), deps, r, r.URL.Query().Get("pantryName"), r.URL.Query().Get("pantryNamespace"))

		result, err := deps.store.Inspect(r.Context(), ociRef, ociClient)
		if err != nil {
			deps.logger.Error(err, "Failed to inspect artifact", "ref", ociRef)
			respondError(w, http.StatusBadGateway, "could not pull artifact: "+err.Error())
			return
		}

		if result.IsHelm {
			respondJSON(w, http.StatusOK, ArtifactInfoDTO{
				IsHelm:     true,
				IsManifest: false,
				Digest:     result.Digest,
				ChartInfo: &ChartInfoDTO{
					Name:          result.ChartInfo.Name,
					Version:       result.ChartInfo.ChartVersion,
					AppVersion:    result.ChartInfo.AppVersion,
					Description:   result.ChartInfo.Description,
					DefaultValues: result.ChartInfo.RawValues,
					Readme:        result.ChartInfo.Readme,
					HasSchema:     result.ChartInfo.HasSchema,
				},
			})
			return
		}

		files := make([]ArtifactFileDTO, 0, len(result.Files))
		for _, f := range result.Files {
			files = append(files, ArtifactFileDTO{Path: f.Path, Content: f.Content})
		}

		respondJSON(w, http.StatusOK, ArtifactInfoDTO{
			IsHelm:     false,
			IsManifest: true,
			Digest:     result.Digest,
			Manifest:   result.Manifest,
			Files:      files,
		})
	}
}

// parseRefVersionQuery extracts and validates the ref and version query
// parameters and returns the tagged OCI reference. It writes the HTTP error
// response itself and reports false when the request should not proceed.
func parseRefVersionQuery(w http.ResponseWriter, r *http.Request) (string, bool) {
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		respondError(w, http.StatusBadRequest, "ref query parameter is required")
		return "", false
	}
	version := r.URL.Query().Get("version")
	if version == "" {
		respondError(w, http.StatusBadRequest, "version query parameter is required")
		return "", false
	}

	ociRef, err := oci.Parse(ref)
	if err != nil {
		respondError(w, http.StatusBadRequest, "unexpected format for ref")
		return "", false
	}
	ociRef.Tag = version

	return ociRef.OCIString(), true
}

// ociClientForPantryRef returns an authenticated OCI client for the explicitly
// named Pantry in the given namespace. When pantryName is empty, or the Pantry
// cannot be resolved, the shared unauthenticated client on deps is returned.
func ociClientForPantryRef(ctx context.Context, deps *apiDeps, r *http.Request, pantryName, pantryNamespace string) oci.Client {
	if pantryName == "" {
		return deps.ociClient
	}
	uc, err := deps.resolveUserClient(r)
	if err != nil {
		return deps.ociClient
	}
	reader, err := uc.readerFor(ctx, "get", "secrets", pantryNamespace)
	if err != nil {
		return deps.ociClient
	}
	if authClient, err := credential.NewKubeResolver(reader).ClientForPantry(ctx, pantryNamespace, pantryName); err == nil && authClient != nil {
		return authClient
	}
	return deps.ociClient
}
