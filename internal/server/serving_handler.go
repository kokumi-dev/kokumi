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

// handlePromote handles POST /api/v1/recipes/{namespace}/{name}/promote.
// It upserts a Serving for the Recipe: if one already exists it patches
// spec.preparation; otherwise a new Serving is created.
func handlePromote(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		recipeName := r.PathValue("name")

		var req PromoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.Preparation == "" {
			respondError(w, http.StatusBadRequest, "preparation is required")
			return
		}

		// Verify the Recipe exists.
		recipe := &deliveryv1alpha1.Recipe{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: recipeName}, recipe); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("recipe %s/%s not found", namespace, recipeName))
				return
			}
			deps.logger.Error(err, "Failed to get Recipe", "namespace", namespace, "name", recipeName)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get recipe: %s", err))
			return
		}

		// Find an existing Serving for this Recipe (same namespace, spec.recipe == recipeName).
		servingList := &deliveryv1alpha1.ServingList{}
		if err := deps.reader.List(r.Context(), servingList, client.InNamespace(namespace)); err != nil {
			deps.logger.Error(err, "Failed to list Servings", "namespace", namespace)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list servings: %s", err))
			return
		}

		var existing *deliveryv1alpha1.Serving
		for i := range servingList.Items {
			if servingList.Items[i].Spec.Recipe == recipeName {
				existing = &servingList.Items[i]
				break
			}
		}

		if existing != nil {
			// Update the existing Serving's desired preparation.
			existing.Spec.Preparation = req.Preparation
			if err := deps.writer.Update(r.Context(), existing); err != nil {
				deps.logger.Error(err, "Failed to update Serving",
					"namespace", namespace, "name", existing.Name)
				respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update serving: %s", err))
				return
			}

			deps.logger.Info("Updated Serving preparation",
				"namespace", namespace, "serving", existing.Name,
				"preparation", req.Preparation)
			respondJSON(w, http.StatusOK, map[string]string{"serving": existing.Name})
			return
		}

		// No Serving exists yet — create one named after the Recipe.
		newServing := &deliveryv1alpha1.Serving{
			ObjectMeta: metav1.ObjectMeta{
				Name:      recipeName,
				Namespace: namespace,
			},
			Spec: deliveryv1alpha1.ServingSpec{
				Recipe:      recipeName,
				Preparation: req.Preparation,
				PreparationPolicy: deliveryv1alpha1.PreparationPolicy{
					Type: deliveryv1alpha1.PreparationPolicyManual,
				},
			},
		}

		if err := deps.writer.Create(r.Context(), newServing); err != nil {
			deps.logger.Error(err, "Failed to create Serving",
				"namespace", namespace, "name", newServing.Name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create serving: %s", err))
			return
		}

		deps.logger.Info("Created Serving",
			"namespace", namespace, "serving", newServing.Name,
			"preparation", req.Preparation)
		respondJSON(w, http.StatusCreated, map[string]string{"serving": newServing.Name})
	}
}
