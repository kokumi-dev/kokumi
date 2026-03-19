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

// handleListMenus handles GET /api/v1/menus.
func handleListMenus(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		menuList := &deliveryv1alpha1.MenuList{}
		if err := deps.reader.List(r.Context(), menuList); err != nil {
			deps.logger.Error(err, "Failed to list Menus")
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list menus: %s", err))
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

		menu := &deliveryv1alpha1.Menu{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{Name: name}, menu); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("menu %s not found", name))
				return
			}
			deps.logger.Error(err, "Failed to get Menu", "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get menu: %s", err))
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
			ObjectMeta: metav1.ObjectMeta{
				Name: req.Name,
			},
			Spec: deliveryv1alpha1.MenuSpec{
				Source:    deliveryv1alpha1.OCISource{OCI: req.Source.OCI, Version: req.Source.Version},
				Render:    renderFromDTO(req.Render),
				Patches:   patchesFromDTO(req.Patches),
				Overrides: overridePolicyFromDTO(req.Overrides),
				Defaults: deliveryv1alpha1.MenuDefaults{
					AutoDeploy: req.Defaults.AutoDeploy,
				},
			},
		}

		if err := deps.writer.Create(r.Context(), menu); err != nil {
			deps.logger.Error(err, "Failed to create Menu", "name", req.Name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create menu: %s", err))
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

		menu := &deliveryv1alpha1.Menu{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{Name: name}, menu); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("menu %s not found", name))
				return
			}
			deps.logger.Error(err, "Failed to get Menu", "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get menu: %s", err))
			return
		}

		menu.Spec.Source = deliveryv1alpha1.OCISource{OCI: req.Source.OCI, Version: req.Source.Version}
		menu.Spec.Render = renderFromDTO(req.Render)
		menu.Spec.Patches = patchesFromDTO(req.Patches)
		menu.Spec.Overrides = overridePolicyFromDTO(req.Overrides)
		menu.Spec.Defaults = deliveryv1alpha1.MenuDefaults{AutoDeploy: req.Defaults.AutoDeploy}

		if err := deps.writer.Update(r.Context(), menu); err != nil {
			deps.logger.Error(err, "Failed to update Menu", "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update menu: %s", err))
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

		menu := &deliveryv1alpha1.Menu{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{Name: name}, menu); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("menu %s not found", name))
				return
			}
			deps.logger.Error(err, "Failed to get Menu", "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get menu: %s", err))
			return
		}

		if err := deps.writer.Delete(r.Context(), menu); err != nil {
			deps.logger.Error(err, "Failed to delete Menu", "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete menu: %s", err))
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
