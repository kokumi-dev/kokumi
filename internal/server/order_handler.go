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

// handleListOrders handles GET /api/v1/orders.
// It lists all Orders across namespaces, enriched with ActivePreparation
// from the matching Serving in the same namespace.
func handleListOrders(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		orderList := &deliveryv1alpha1.OrderList{}
		if err := deps.reader.List(r.Context(), orderList); err != nil {
			deps.logger.Error(err, "Failed to list Orders")
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list orders: %s", err))
			return
		}

		servingList := &deliveryv1alpha1.ServingList{}
		if err := deps.reader.List(r.Context(), servingList); err != nil {
			deps.logger.Error(err, "Failed to list Servings")
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list servings: %s", err))
			return
		}

		respondJSON(w, http.StatusOK, enrichOrders(orderList.Items, servingList.Items))
	}
}

// handleGetOrder handles GET /api/v1/orders/{namespace}/{name}.
func handleGetOrder(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		order := &deliveryv1alpha1.Order{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, order); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("order %s/%s not found", namespace, name))
				return
			}
			deps.logger.Error(err, "Failed to get Order", "namespace", namespace, "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get order: %s", err))
			return
		}

		servingList := &deliveryv1alpha1.ServingList{}
		if err := deps.reader.List(r.Context(), servingList, client.InNamespace(namespace)); err != nil {
			deps.logger.Error(err, "Failed to list Servings", "namespace", namespace)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list servings: %s", err))
			return
		}

		active := activePreparationFor(namespace, name, servingList.Items)
		respondJSON(w, http.StatusOK, orderToDTO(*order, active))
	}
}

// handleCreateOrder handles POST /api/v1/orders.
func handleCreateOrder(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req CreateOrderRequest
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

		order := &deliveryv1alpha1.Order{
			ObjectMeta: metav1.ObjectMeta{
				Name:      req.Name,
				Namespace: req.Namespace,
			},
			Spec: deliveryv1alpha1.OrderSpec{
				Source:      deliveryv1alpha1.OCISource{OCI: req.Source.OCI, Version: req.Source.Version},
				Destination: deliveryv1alpha1.OCIDestination{OCI: req.Destination.OCI},
				Render:      renderFromDTO(req.Render),
				Patches:     patchesFromDTO(req.Patches),
				AutoDeploy:  req.AutoDeploy,
			},
		}

		if err := deps.writer.Create(r.Context(), order); err != nil {
			deps.logger.Error(err, "Failed to create Order", "namespace", req.Namespace, "name", req.Name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create order: %s", err))
			return
		}

		respondJSON(w, http.StatusCreated, orderToDTO(*order, ""))
	}
}

// handleUpdateOrder handles PUT /api/v1/orders/{namespace}/{name}.
func handleUpdateOrder(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		var req UpdateOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}

		order := &deliveryv1alpha1.Order{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, order); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("order %s/%s not found", namespace, name))
				return
			}
			deps.logger.Error(err, "Failed to get Order", "namespace", namespace, "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get order: %s", err))
			return
		}

		order.Spec.Source = deliveryv1alpha1.OCISource{OCI: req.Source.OCI, Version: req.Source.Version}
		order.Spec.Destination = deliveryv1alpha1.OCIDestination{OCI: req.Destination.OCI}
		order.Spec.Render = renderFromDTO(req.Render)
		order.Spec.Patches = patchesFromDTO(req.Patches)
		order.Spec.AutoDeploy = req.AutoDeploy

		if err := deps.writer.Update(r.Context(), order); err != nil {
			deps.logger.Error(err, "Failed to update Order", "namespace", namespace, "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update order: %s", err))
			return
		}

		respondJSON(w, http.StatusOK, orderToDTO(*order, ""))
	}
}

// handleDeleteOrder handles DELETE /api/v1/orders/{namespace}/{name}.
func handleDeleteOrder(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		order := &deliveryv1alpha1.Order{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, order); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("order %s/%s not found", namespace, name))
				return
			}
			deps.logger.Error(err, "Failed to get Order", "namespace", namespace, "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get order: %s", err))
			return
		}

		if err := deps.writer.Delete(r.Context(), order); err != nil {
			deps.logger.Error(err, "Failed to delete Order", "namespace", namespace, "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete order: %s", err))
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
