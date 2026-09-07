package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handleListMenus handles GET /api/v1/menus.
func handleListMenus(deps *apiDeps) http.HandlerFunc {
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

		menuList := &deliveryv1alpha1.MenuList{}
		if err := uc.list(r.Context(), menuList); err != nil {
			respondForbiddenOrError(w, err, "failed to list menus")
			return
		}

		respondJSON(w, http.StatusOK, menusToDTO(menuList.Items))
	}
}

// handleGetMenu handles GET /api/v1/menus/{name}.
func handleGetMenu(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		name := r.PathValue("name")

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		menu := &deliveryv1alpha1.Menu{}
		if err := uc.get(r.Context(), types.NamespacedName{Name: name}, menu); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("menu %s not found", name))
				return
			}
			respondForbiddenOrError(w, err, "failed to get menu")
			return
		}

		respondJSON(w, http.StatusOK, menuToDTO(*menu))
	}
}

// handleCreateMenu handles POST /api/v1/menus.
func handleCreateMenu(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req CreateMenuRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.Name == "" {
			respondError(w, http.StatusBadRequest, "name is required")
			return
		}

		menu := &deliveryv1alpha1.Menu{
			Name: req.Name,
			Spec: deliveryv1alpha1.MenuSpec{
				Source:    deliveryv1alpha1.OCISource{OCI: req.Source.OCI, Version: req.Source.Version},
				Render:    renderFromDTO(req.Render),
				Patches:   patchesFromDTO(req.Patches),
				Overrides: overridePolicyFromDTO(req.Overrides),
				Defaults: deliveryv1alpha1.MenuDefaults{
					AutoDeploy: deliveryv1alpha1.AutoDeployPolicy(req.Defaults.AutoDeploy),
				},
			},
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}
		if err := uc.create(r.Context(), menu, "menus"); err != nil {
			deps.logger.Error(err, "Failed to create Menu", "name", req.Name)
			respondForbiddenOrError(w, err, "failed to create menu")
			return
		}

		respondJSON(w, http.StatusCreated, menuToDTO(*menu))
	}
}

// handleUpdateMenu handles PUT /api/v1/menus/{name}.
func handleUpdateMenu(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		name := r.PathValue("name")

		var req UpdateMenuRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		menu := &deliveryv1alpha1.Menu{}
		if err := uc.get(r.Context(), types.NamespacedName{Name: name}, menu); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("menu %s not found", name))
				return
			}
			respondForbiddenOrError(w, err, "failed to get menu")
			return
		}

		menu.Spec.Source = deliveryv1alpha1.OCISource{OCI: req.Source.OCI, Version: req.Source.Version}
		menu.Spec.Render = renderFromDTO(req.Render)
		menu.Spec.Patches = patchesFromDTO(req.Patches)
		menu.Spec.Overrides = overridePolicyFromDTO(req.Overrides)
		menu.Spec.Defaults = deliveryv1alpha1.MenuDefaults{AutoDeploy: deliveryv1alpha1.AutoDeployPolicy(req.Defaults.AutoDeploy)}

		if err := uc.update(r.Context(), menu, "menus"); err != nil {
			deps.logger.Error(err, "Failed to update Menu", "name", name)
			respondForbiddenOrError(w, err, "failed to update menu")
			return
		}

		respondJSON(w, http.StatusOK, menuToDTO(*menu))
	}
}

// handleDeleteMenu handles DELETE /api/v1/menus/{name}.
func handleDeleteMenu(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		name := r.PathValue("name")

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		menu := &deliveryv1alpha1.Menu{}
		if err := uc.get(r.Context(), types.NamespacedName{Name: name}, menu); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("menu %s not found", name))
				return
			}
			respondForbiddenOrError(w, err, "failed to get menu")
			return
		}

		if err := uc.delete(r.Context(), menu, "menus"); err != nil {
			deps.logger.Error(err, "Failed to delete Menu", "name", name)
			respondForbiddenOrError(w, err, "failed to delete menu")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
