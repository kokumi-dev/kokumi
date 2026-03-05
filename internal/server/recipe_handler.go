package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handleListRecipes handles GET /api/v1/recipes.
// It lists all Recipes across namespaces, enriched with ActivePreparation
// from the matching Serving in the same namespace.
func handleListRecipes(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		recipeList := &deliveryv1alpha1.RecipeList{}
		if err := deps.reader.List(r.Context(), recipeList); err != nil {
			deps.logger.Error(err, "Failed to list Recipes")
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list recipes: %s", err))
			return
		}

		servingList := &deliveryv1alpha1.ServingList{}
		if err := deps.reader.List(r.Context(), servingList); err != nil {
			deps.logger.Error(err, "Failed to list Servings")
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list servings: %s", err))
			return
		}

		respondJSON(w, http.StatusOK, enrichRecipes(recipeList.Items, servingList.Items))
	}
}

// handleGetRecipe handles GET /api/v1/recipes/{namespace}/{name}.
func handleGetRecipe(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		recipe := &deliveryv1alpha1.Recipe{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, recipe); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("recipe %s/%s not found", namespace, name))
				return
			}
			deps.logger.Error(err, "Failed to get Recipe", "namespace", namespace, "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get recipe: %s", err))
			return
		}

		servingList := &deliveryv1alpha1.ServingList{}
		if err := deps.reader.List(r.Context(), servingList, client.InNamespace(namespace)); err != nil {
			deps.logger.Error(err, "Failed to list Servings", "namespace", namespace)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list servings: %s", err))
			return
		}

		active := activePreparationFor(namespace, name, servingList.Items)
		respondJSON(w, http.StatusOK, recipeToDTO(*recipe, active))
	}
}

// handleCreateRecipe handles POST /api/v1/recipes.
func handleCreateRecipe(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req CreateRecipeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.Name == "" {
			respondError(w, http.StatusBadRequest, "name is required")
			return
		}
		if req.Namespace == "" {
			req.Namespace = "default"
		}

		recipe := &deliveryv1alpha1.Recipe{
			ObjectMeta: metav1.ObjectMeta{
				Name:      req.Name,
				Namespace: req.Namespace,
			},
			Spec: deliveryv1alpha1.RecipeSpec{
				Source:      deliveryv1alpha1.OCISource{OCI: req.Source.OCI, Version: req.Source.Version},
				Destination: deliveryv1alpha1.OCIDestination{OCI: req.Destination.OCI},
				Render:      renderFromDTO(req.Render),
				Patches:     patchesFromDTO(req.Patches),
				AutoDeploy:  req.AutoDeploy,
			},
		}

		if err := deps.writer.Create(r.Context(), recipe); err != nil {
			deps.logger.Error(err, "Failed to create Recipe", "namespace", req.Namespace, "name", req.Name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create recipe: %s", err))
			return
		}

		respondJSON(w, http.StatusCreated, recipeToDTO(*recipe, ""))
	}
}

// handleUpdateRecipe handles PUT /api/v1/recipes/{namespace}/{name}.
func handleUpdateRecipe(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		var req UpdateRecipeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}

		recipe := &deliveryv1alpha1.Recipe{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, recipe); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("recipe %s/%s not found", namespace, name))
				return
			}
			deps.logger.Error(err, "Failed to get Recipe", "namespace", namespace, "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get recipe: %s", err))
			return
		}

		recipe.Spec.Source = deliveryv1alpha1.OCISource{OCI: req.Source.OCI, Version: req.Source.Version}
		recipe.Spec.Destination = deliveryv1alpha1.OCIDestination{OCI: req.Destination.OCI}
		recipe.Spec.Render = renderFromDTO(req.Render)
		recipe.Spec.Patches = patchesFromDTO(req.Patches)
		recipe.Spec.AutoDeploy = req.AutoDeploy

		if err := deps.writer.Update(r.Context(), recipe); err != nil {
			deps.logger.Error(err, "Failed to update Recipe", "namespace", namespace, "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update recipe: %s", err))
			return
		}

		respondJSON(w, http.StatusOK, recipeToDTO(*recipe, ""))
	}
}

// handleDeleteRecipe handles DELETE /api/v1/recipes/{namespace}/{name}.
func handleDeleteRecipe(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		recipe := &deliveryv1alpha1.Recipe{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, recipe); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("recipe %s/%s not found", namespace, name))
				return
			}
			deps.logger.Error(err, "Failed to get Recipe", "namespace", namespace, "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get recipe: %s", err))
			return
		}

		if err := deps.writer.Delete(r.Context(), recipe); err != nil {
			deps.logger.Error(err, "Failed to delete Recipe", "namespace", namespace, "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete recipe: %s", err))
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
