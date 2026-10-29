package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// environmentsResource is the resource of Environments.
const environmentsResource = "environments"

// handleListEnvironments handles GET /api/v1/environments, of one namespace
// with ?namespace=, such as the Order form offers.
func handleListEnvironments(deps *apiDeps) http.HandlerFunc {
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

		list := &deliveryv1alpha1.EnvironmentList{}
		if err := uc.list(r.Context(), list, listOpts...); err != nil {
			respondForbiddenOrError(w, err, "failed to list environments")
			return
		}

		respondJSON(w, http.StatusOK, environmentsToDTO(list.Items, deps.installNamespace))
	}
}

// handleGetEnvironment handles GET /api/v1/environments/{namespace}/{name}.
func handleGetEnvironment(deps *apiDeps) http.HandlerFunc {
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

		env, ok := getEnvironment(w, r, uc)
		if !ok {
			return
		}
		respondJSON(w, http.StatusOK, environmentToDTO(*env, deps.installNamespace))
	}
}

// handleCreateEnvironment handles POST /api/v1/environments.
func handleCreateEnvironment(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req EnvironmentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.Namespace == "" || req.Name == "" {
			respondError(w, http.StatusBadRequest, "namespace and name are required")
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		if err := validClusterSelector(req.EnvironmentSpec); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		env := &deliveryv1alpha1.Environment{Name: req.Name, Namespace: req.Namespace, Spec: req.EnvironmentSpec}
		if err := uc.create(r.Context(), env, environmentsResource); err != nil {
			deps.logger.Error(err, "Failed to create Environment", "namespace", req.Namespace, "name", req.Name)
			respondForbiddenOrError(w, err, "failed to create environment")
			return
		}

		respondJSON(w, http.StatusCreated, environmentToDTO(*env, deps.installNamespace))
	}
}

// handleUpdateEnvironment handles PUT /api/v1/environments/{namespace}/{name}.
func handleUpdateEnvironment(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req EnvironmentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		env, ok := getEnvironment(w, r, uc)
		if !ok {
			return
		}
		if err := validClusterSelector(req.EnvironmentSpec); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		env.Spec = req.EnvironmentSpec
		if err := uc.update(r.Context(), env, environmentsResource); err != nil {
			deps.logger.Error(err, "Failed to update Environment", "namespace", env.Namespace, "name", env.Name)
			respondForbiddenOrError(w, err, "failed to update environment")
			return
		}

		respondJSON(w, http.StatusOK, environmentToDTO(*env, deps.installNamespace))
	}
}

// handleDeleteEnvironment handles DELETE
// /api/v1/environments/{namespace}/{name}. The Orders that deploy to it stop
// deploying and say why.
func handleDeleteEnvironment(deps *apiDeps) http.HandlerFunc {
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

		env, ok := getEnvironment(w, r, uc)
		if !ok {
			return
		}
		if err := uc.delete(r.Context(), env, environmentsResource); err != nil {
			deps.logger.Error(err, "Failed to delete Environment", "namespace", env.Namespace, "name", env.Name)
			respondForbiddenOrError(w, err, "failed to delete environment")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// getEnvironment reads the Environment named by the request path as uc. It
// writes the error response and reports false when it cannot be read.
func getEnvironment(w http.ResponseWriter, r *http.Request, uc *userClient) (*deliveryv1alpha1.Environment, bool) {
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	env := &deliveryv1alpha1.Environment{}
	if err := uc.get(r.Context(), types.NamespacedName{Namespace: ns, Name: name}, env); err != nil {
		if client.IgnoreNotFound(err) == nil {
			respondError(w, http.StatusNotFound, fmt.Sprintf("environment %s/%s not found", ns, name))
			return nil, false
		}
		respondForbiddenOrError(w, err, "failed to get environment")
		return nil, false
	}
	return env, true
}

// validClusterSelector reports a clusterSelector that is no valid label
// selector, which the API accepts and the Environment would report.
func validClusterSelector(spec deliveryv1alpha1.EnvironmentSpec) error {
	if _, err := metav1.LabelSelectorAsSelector(&spec.ClusterSelector); err != nil {
		return fmt.Errorf("invalid clusterSelector: %w", err)
	}
	return nil
}
