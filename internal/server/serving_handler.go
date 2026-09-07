package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handlePromote handles POST /api/v1/orders/{namespace}/{name}/promote.
// It upserts a Serving for the Order: if one already exists it patches
// spec.preparation; otherwise a new Serving is created.
func handlePromote(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		orderName := r.PathValue("name")

		var req PromoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.Preparation == "" {
			respondError(w, http.StatusBadRequest, "preparation is required")
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		// Verify the Order exists.
		order := &deliveryv1alpha1.Order{}
		if err := uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: orderName}, order); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("order %s/%s not found", namespace, orderName))
				return
			}
			respondForbiddenOrError(w, err, "failed to get order")
			return
		}

		// Find an existing Serving for this Order (same namespace, spec.order == orderName).
		servingList := &deliveryv1alpha1.ServingList{}
		if err := uc.list(r.Context(), servingList, client.InNamespace(namespace)); err != nil {
			respondForbiddenOrError(w, err, "failed to list servings")
			return
		}

		var existing *deliveryv1alpha1.Serving
		for i := range servingList.Items {
			if servingList.Items[i].Spec.OrderName == orderName {
				existing = &servingList.Items[i]
				break
			}
		}

		if existing != nil {
			// Update the existing Serving's desired preparation.
			existing.Spec.PreparationName = req.Preparation
			if err := uc.update(r.Context(), existing, "servings"); err != nil {
				deps.logger.Error(err, "Failed to update Serving",
					"namespace", namespace, "name", existing.Name)
				respondForbiddenOrError(w, err, "failed to update serving")
				return
			}

			deps.logger.Info("Updated Serving preparation",
				"namespace", namespace, "serving", existing.Name,
				"preparation", req.Preparation)
			respondJSON(w, http.StatusOK, map[string]string{"serving": existing.Name})
			return
		}

		// No Serving exists yet — create one named after the Order.
		newServing := &deliveryv1alpha1.Serving{
			Name:      orderName,
			Namespace: namespace,
			Spec: deliveryv1alpha1.ServingSpec{
				OrderName:       orderName,
				PreparationName: req.Preparation,
				PreparationPolicy: deliveryv1alpha1.PreparationPolicy{
					Type: deliveryv1alpha1.PreparationPolicyManual,
				},
			},
		}

		if err := uc.create(r.Context(), newServing, "servings"); err != nil {
			deps.logger.Error(err, "Failed to create Serving",
				"namespace", namespace, "name", newServing.Name)
			respondForbiddenOrError(w, err, "failed to create serving")
			return
		}

		deps.logger.Info("Created Serving",
			"namespace", namespace, "serving", newServing.Name,
			"preparation", req.Preparation)
		respondJSON(w, http.StatusCreated, map[string]string{"serving": newServing.Name})
	}
}
