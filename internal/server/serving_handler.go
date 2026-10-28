package server

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/argocd"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handlePromote handles POST /api/v1/orders/{namespace}/{name}/promote.
// It points the Order's Serving at the requested Preparation (Manual
// promotion only): a promotion is an intention. One that its gates hold,
// its upstream stages, its checks or its approvals, is queued: the Serving
// deploys it once they pass. Promoting the deployed Preparation withdraws a
// queued promotion.
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

		// Early feedback only: the Serving controller enforces readiness and
		// every gate, and verifies the evidence in the registry. A
		// Preparation that is not ready may never be, unlike one its gates
		// hold.
		if !apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeSigned) {
			respondError(w, http.StatusConflict, "preparation is not signed")
			return
		}
		if !apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) {
			respondError(w, http.StatusConflict, "preparation is not ready")
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

// handleServingTargets handles GET /api/v1/servings/{namespace}/{name}/targets:
// every cluster the Serving deploys to, the Clusters its Order's Environment
// selects, with how its deployment stands there. The Serving's status counts
// them and lists only those that do not run its Preparation healthily, so
// this is worked out on request.
func handleServingTargets(deps *apiDeps) http.HandlerFunc {
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
		namespace, name := r.PathValue("namespace"), r.PathValue("name")
		serving := &deliveryv1alpha1.Serving{}
		if err := uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, serving); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("serving %s/%s not found", namespace, name))
				return
			}
			respondForbiddenOrError(w, err, "failed to get serving")
			return
		}
		deployed := serving.DeployedEntry()
		order := &deliveryv1alpha1.Order{}
		env := &deliveryv1alpha1.Environment{}
		clusters := &deliveryv1alpha1.ClusterList{}
		if deployed == nil ||
			uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: serving.Spec.OrderName}, order) != nil ||
			uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: order.Spec.EnvironmentRef.Name}, env) != nil {
			// Nothing is deployed, or the Order or its Environment is gone:
			// it deploys nowhere.
			respondJSON(w, http.StatusOK, []DeploymentTargetDTO{})
			return
		}
		if err := uc.list(r.Context(), clusters, client.InNamespace(namespace)); err != nil {
			respondForbiddenOrError(w, err, "failed to list clusters")
			return
		}
		selected, err := resolve.SelectedClusters(env, clusters.Items)
		if err != nil {
			respondJSON(w, http.StatusOK, []DeploymentTargetDTO{})
			return
		}

		out := make([]DeploymentTargetDTO, 0, len(selected))
		for _, c := range selected {
			target := servingTarget(serving, deployed, c.Name)
			target.Server = cmp.Or(target.Server, c.Status.Server)
			out = append(out, target)
		}
		respondJSON(w, http.StatusOK, out)
	}
}

// servingTarget describes how the deployment of deployed by serving stands
// on the cluster clusterName: as the Serving lists it, or, for a cluster it
// does not list, healthy at the deployed revision, unless the Serving cut its
// list short or deploys nowhere yet.
func servingTarget(serving *deliveryv1alpha1.Serving, deployed *deliveryv1alpha1.DeploymentEntry, clusterName string) DeploymentTargetDTO {
	dto := DeploymentTargetDTO{Cluster: clusterName, State: stateUnknown, ApplicationName: argocd.Key(serving, clusterName).Name}
	if i := slices.IndexFunc(serving.Status.Targets, func(t deliveryv1alpha1.DeploymentTarget) bool { return t.Cluster == clusterName }); i >= 0 {
		listed := serving.Status.Targets[i]
		dto.Revision = listed.Revision
		if ready := apimeta.FindStatusCondition(listed.Conditions, deliveryv1alpha1.ConditionTypeReady); ready != nil {
			dto.State, dto.Message = ready.Reason, ready.Message
			dto.Ready = ready.Status == metav1.ConditionTrue
			dto.Since = new(ready.LastTransitionTime.UTC())
		}
		return dto
	}
	counts := serving.Status.Clusters
	switch {
	case counts == nil || counts.Selected == 0:
		dto.Message = "The Serving deploys to no cluster yet"
		if ready := apimeta.FindStatusCondition(serving.Status.Conditions, deliveryv1alpha1.ConditionTypeReady); ready != nil {
			dto.State, dto.Message = ready.Reason, ready.Message
		}
	case int(counts.Progressing+counts.Degraded+counts.Held) > len(serving.Status.Targets):
		dto.Message = fmt.Sprintf("The Serving lists %d of the %d clusters that do not run Preparation %s healthily; this one is not among them",
			len(serving.Status.Targets), counts.Progressing+counts.Degraded+counts.Held, deployed.PreparationName)
	default:
		dto.State, dto.Ready, dto.Revision = deliveryv1alpha1.ReasonHealthy, true, deployed.Digest
		dto.Message = "Runs Preparation " + deployed.PreparationName
	}
	return dto
}
