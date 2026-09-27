package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/index"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handlePromote handles POST /api/v1/orders/{namespace}/{name}/promote.
// It points the Order's Serving at the requested Preparation (Manual
// promotion only). Unapproved Preparations are rejected up front.
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
		if order.EffectivePromotionMode() == deliveryv1alpha1.PromotionModeAutomatic {
			respondError(w, http.StatusConflict, "order uses Automatic promotion; switch it to Manual to promote a specific preparation")
			return
		}

		prep := &deliveryv1alpha1.Preparation{}
		if err := uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: req.Preparation}, prep); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("preparation %s/%s not found", namespace, req.Preparation))
				return
			}
			respondForbiddenOrError(w, err, "failed to get preparation")
			return
		}
		if prep.Spec.OrderName != orderName {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("preparation %s does not belong to order %s", prep.Name, orderName))
			return
		}

		// Early feedback only: the Serving controller enforces the gate.
		approvals := &deliveryv1alpha1.ApprovalList{}
		if err := uc.list(r.Context(), approvals,
			client.InNamespace(namespace),
			client.MatchingFields{index.FieldPreparationRefName: prep.Name},
		); err != nil {
			respondForbiddenOrError(w, err, "failed to list approvals")
			return
		}
		if res := approval.Evaluate(prep, approvals.Items); !res.Approved {
			respondError(w, http.StatusConflict, "preparation is not approved: "+res.Message)
			return
		}

		// The Serving of an Order always carries the Order's name.
		existing := &deliveryv1alpha1.Serving{}
		err = uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: orderName}, existing)
		if err != nil && client.IgnoreNotFound(err) != nil {
			respondForbiddenOrError(w, err, "failed to get serving")
			return
		}

		if err == nil {
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
			Labels:    map[string]string{deliveryv1alpha1.LabelOrder: orderName},
			Spec: deliveryv1alpha1.ServingSpec{
				OrderName:       orderName,
				PreparationName: req.Preparation,
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
