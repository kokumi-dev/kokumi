package server

import (
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handleListPreparations handles GET /api/v1/orders/{namespace}/{name}/preparations.
// Returns all Preparations for the given Order, sorted newest-first by createdAt,
// with IsActive populated from the linked Serving.
func handleListPreparations(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		orderName := r.PathValue("name")

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		prepList := &deliveryv1alpha1.PreparationList{}
		if err := uc.list(r.Context(), prepList, client.InNamespace(namespace)); err != nil {
			respondForbiddenOrError(w, err, "failed to list preparations")
			return
		}

		// Client-side filter by order name.
		filtered := prepList.Items[:0]
		for _, p := range prepList.Items {
			if p.Spec.OrderName == orderName {
				filtered = append(filtered, p)
			}
		}
		prepList.Items = filtered

		servingList := &deliveryv1alpha1.ServingList{}
		if err := uc.list(r.Context(), servingList, client.InNamespace(namespace)); err != nil {
			respondForbiddenOrError(w, err, "failed to list servings")
			return
		}

		respondJSON(w, http.StatusOK, enrichPreparations(prepList.Items, servingList.Items))
	}
}

// handleGetPreparationManifest handles GET /api/v1/preparations/{namespace}/{name}/manifest.
// It fetches the rendered Kubernetes YAML from the Preparation's OCI artifact.
func handleGetPreparationManifest(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		prep := &deliveryv1alpha1.Preparation{}
		if err := uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, prep); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("preparation %s/%s not found", namespace, name))
				return
			}
			respondForbiddenOrError(w, err, "failed to get preparation")
			return
		}

		result, err := deps.store.Inspect(r.Context(), prep.Spec.Artifact.OCIRef, nil)
		if err != nil {
			deps.logger.Error(err, "Failed to fetch manifest from OCI",
				"namespace", namespace, "name", name,
				"ociRef", prep.Spec.Artifact.OCIRef)
			respondError(w, http.StatusBadGateway, fmt.Sprintf("failed to fetch manifest from OCI: %s", err))
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(result.Manifest))
	}
}

// handleGetPreparationManifestFiles handles GET /api/v1/preparations/{namespace}/{name}/manifest/files.
// It returns the individual YAML files of the Preparation's OCI artifact,
// preserving the artifact's file layout.
func handleGetPreparationManifestFiles(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		prep := &deliveryv1alpha1.Preparation{}
		if err := uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, prep); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("preparation %s/%s not found", namespace, name))
				return
			}
			respondForbiddenOrError(w, err, "failed to get preparation")
			return
		}

		result, err := deps.store.Inspect(r.Context(), prep.Spec.Artifact.OCIRef, nil)
		if err != nil {
			deps.logger.Error(err, "Failed to read manifest files", "ociRef", prep.Spec.Artifact.OCIRef)
			respondError(w, http.StatusBadGateway, "could not read manifest files: "+err.Error())
			return
		}

		files := make([]ArtifactFileDTO, 0, len(result.Files))
		for _, f := range result.Files {
			files = append(files, ArtifactFileDTO{Path: f.Path, Content: f.Content})
		}

		respondJSON(w, http.StatusOK, files)
	}
}
