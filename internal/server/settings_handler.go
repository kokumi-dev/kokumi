package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// settingsResponse is the JSON body for GET /api/v1/settings.
type settingsResponse struct {
	ArgoCDURL string `json:"argoCDURL"`
}

// settingsRequest is the JSON body for PUT /api/v1/settings.
type settingsRequest struct {
	ArgoCDURL string `json:"argoCDURL"`
}

// handleGetSettings handles GET /api/v1/settings.
// Returns the current Argo CD URL from the singleton Kitchen/default.
func handleGetSettings(deps *apiDeps, namespace string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		kitchen := &deliveryv1alpha1.Kitchen{}
		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}
		err = uc.get(r.Context(), types.NamespacedName{
			Namespace: namespace,
			Name:      deliveryv1alpha1.DefaultKitchenName,
		}, kitchen)
		if err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondJSON(w, http.StatusOK, settingsResponse{})
				return
			}
			respondForbiddenOrError(w, err, "failed to get settings")
			return
		}

		respondJSON(w, http.StatusOK, settingsResponse{ArgoCDURL: kitchen.Spec.ArgoCDURL})
	}
}

// handlePutSettings handles PUT /api/v1/settings.
// Updates spec.argoCDURL on the singleton Kitchen/default, creating it if absent.
func handlePutSettings(deps *apiDeps, namespace string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req settingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		raw := strings.TrimSpace(req.ArgoCDURL)
		if raw != "" {
			u, err := url.Parse(raw)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				respondError(w, http.StatusBadRequest, "argoCDURL must be a valid http:// or https:// URL")
				return
			}
		}

		kitchen := &deliveryv1alpha1.Kitchen{}
		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}
		err = uc.get(r.Context(), types.NamespacedName{
			Namespace: namespace,
			Name:      deliveryv1alpha1.DefaultKitchenName,
		}, kitchen)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				respondForbiddenOrError(w, err, "failed to get settings")
				return
			}
			kitchen = &deliveryv1alpha1.Kitchen{}
			kitchen.Name = deliveryv1alpha1.DefaultKitchenName
			kitchen.Namespace = namespace
			kitchen.Spec.ArgoCDURL = raw
			if err := uc.create(r.Context(), kitchen, "kitchens"); err != nil {
				deps.logger.Error(err, "Failed to create Kitchen")
				respondForbiddenOrError(w, err, "failed to save settings")
				return
			}
			respondJSON(w, http.StatusOK, settingsResponse{ArgoCDURL: raw})
			return
		}

		if kitchen.Spec.ArgoCDURL == raw {
			respondJSON(w, http.StatusOK, settingsResponse{ArgoCDURL: raw})
			return
		}
		kitchen.Spec.ArgoCDURL = raw
		if err := uc.update(r.Context(), kitchen, "kitchens"); err != nil {
			deps.logger.Error(err, "Failed to update Kitchen")
			respondForbiddenOrError(w, err, "failed to save settings")
			return
		}

		respondJSON(w, http.StatusOK, settingsResponse{ArgoCDURL: raw})
	}
}
