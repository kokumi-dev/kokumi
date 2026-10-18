package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/namespace"
)

// handleListRepositories handles GET /api/v1/repositories. An optional
// ?namespace= query parameter filters to one namespace.
func handleListRepositories(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var listOpts []client.ListOption
		if ns := r.URL.Query().Get("namespace"); ns != "" {
			listOpts = append(listOpts, client.InNamespace(ns))
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		list := &deliveryv1alpha1.RepositoryList{}
		if err := uc.list(r.Context(), list, listOpts...); err != nil {
			respondForbiddenOrError(w, err, "failed to list repositories")
			return
		}

		respondJSON(w, http.StatusOK, repositoriesToDTO(list.Items))
	}
}

// handleGetRepository handles GET /api/v1/repositories/{namespace}/{name}.
func handleGetRepository(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		repo, ok := getRepository(w, r, uc)
		if !ok {
			return
		}

		respondJSON(w, http.StatusOK, repositoryToDTO(*repo))
	}
}

// handleCreateRepository handles POST /api/v1/repositories.
func handleCreateRepository(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req CreateRepositoryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.Name == "" || req.URL == "" {
			respondError(w, http.StatusBadRequest, "name and url are required")
			return
		}
		ns := req.Namespace
		if ns == "" {
			ns = namespace.Default
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		repo := &deliveryv1alpha1.Repository{
			Name:      req.Name,
			Namespace: ns,
			Spec:      repositorySpec(req.URL, req.RegistryRef, req.Description),
		}
		if err := uc.create(r.Context(), repo, repositoriesResource); err != nil {
			if apierrors.IsAlreadyExists(err) {
				respondError(w, http.StatusConflict, fmt.Sprintf("repository %q already exists", req.Name))
				return
			}
			deps.logger.Error(err, "Failed to create Repository", "name", req.Name)
			respondForbiddenOrError(w, err, "failed to create repository")
			return
		}

		respondJSON(w, http.StatusCreated, repositoryToDTO(*repo))
	}
}

// handleUpdateRepository handles PUT /api/v1/repositories/{namespace}/{name}.
func handleUpdateRepository(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req UpdateRepositoryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.URL == "" {
			respondError(w, http.StatusBadRequest, "url is required")
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		repo, ok := getRepository(w, r, uc)
		if !ok {
			return
		}

		patch := client.MergeFrom(repo.DeepCopy())
		repo.Spec = repositorySpec(req.URL, req.RegistryRef, req.Description)
		if err := uc.patch(r.Context(), repo, patch, repositoriesResource); err != nil {
			deps.logger.Error(err, "Failed to update Repository", "name", repo.Name)
			respondForbiddenOrError(w, err, "failed to update repository")
			return
		}

		respondJSON(w, http.StatusOK, repositoryToDTO(*repo))
	}
}

// handleDeleteRepository handles DELETE /api/v1/repositories/{namespace}/{name}.
func handleDeleteRepository(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		repo, ok := getRepository(w, r, uc)
		if !ok {
			return
		}

		if err := uc.delete(r.Context(), repo, repositoriesResource); err != nil {
			deps.logger.Error(err, "Failed to delete Repository", "name", repo.Name)
			respondForbiddenOrError(w, err, "failed to delete repository")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// getRepository reads the Repository named by the request path as uc. It
// writes the error response and reports false when it cannot be read.
func getRepository(w http.ResponseWriter, r *http.Request, uc *userClient) (*deliveryv1alpha1.Repository, bool) {
	ns, name := r.PathValue("namespace"), r.PathValue("name")

	repo := &deliveryv1alpha1.Repository{}
	if err := uc.get(r.Context(), types.NamespacedName{Namespace: ns, Name: name}, repo); err != nil {
		if client.IgnoreNotFound(err) == nil {
			respondError(w, http.StatusNotFound, fmt.Sprintf("repository %s/%s not found", ns, name))
			return nil, false
		}
		respondForbiddenOrError(w, err, "failed to get repository")
		return nil, false
	}
	return repo, true
}

// repositorySpec builds the spec of a Repository from a request.
func repositorySpec(url, registryRef, description string) deliveryv1alpha1.RepositorySpec {
	spec := deliveryv1alpha1.RepositorySpec{URL: url, Description: description}
	if registryRef != "" {
		spec.RegistryRef = &corev1.LocalObjectReference{Name: registryRef}
	}
	return spec
}
