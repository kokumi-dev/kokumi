package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handleListProducts handles GET /api/v1/products.
// Lists Products across all namespaces. An optional ?namespace= query param
// can be used to filter to a specific namespace.
func handleListProducts(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		listOpts := []client.ListOption{}
		if ns := r.URL.Query().Get("namespace"); ns != "" {
			listOpts = append(listOpts, client.InNamespace(ns))
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		productList := &deliveryv1alpha1.ProductList{}
		if err := uc.list(r.Context(), productList, listOpts...); err != nil {
			respondForbiddenOrError(w, err, "failed to list products")
			return
		}

		respondJSON(w, http.StatusOK, productsToDTO(productList.Items))
	}
}

// handleGetProduct handles GET /api/v1/products/{namespace}/{name}.
func handleGetProduct(deps *apiDeps) http.HandlerFunc {
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

		product, ok := getProduct(w, r, uc)
		if !ok {
			return
		}

		respondJSON(w, http.StatusOK, productToDTO(*product))
	}
}

// handleCreateProduct handles POST /api/v1/products.
func handleCreateProduct(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req CreateProductRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.Name == "" {
			respondError(w, http.StatusBadRequest, "name is required")
			return
		}
		if req.Namespace == "" {
			respondError(w, http.StatusBadRequest, "namespace is required")
			return
		}

		product := &deliveryv1alpha1.Product{
			Namespace: req.Namespace,
			Name:      req.Name,
			Spec:      productSpecFromDTO(req.ProductSpecDTO),
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}
		if err := uc.create(r.Context(), product, "products"); err != nil {
			deps.logger.Error(err, "Failed to create Product", "name", req.Name)
			respondForbiddenOrError(w, err, "failed to create product")
			return
		}

		respondJSON(w, http.StatusCreated, productToDTO(*product))
	}
}

// handleUpdateProduct handles PUT /api/v1/products/{namespace}/{name}.
func handleUpdateProduct(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req UpdateProductRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		product, ok := getProduct(w, r, uc)
		if !ok {
			return
		}

		product.Spec = productSpecFromDTO(req.ProductSpecDTO)

		if err := uc.update(r.Context(), product, "products"); err != nil {
			deps.logger.Error(err, "Failed to update Product", "name", product.Name)
			respondForbiddenOrError(w, err, "failed to update product")
			return
		}

		respondJSON(w, http.StatusOK, productToDTO(*product))
	}
}

// handleDeleteProduct handles DELETE /api/v1/products/{namespace}/{name}.
// The Product stays until its Orders are deleted.
func handleDeleteProduct(deps *apiDeps) http.HandlerFunc {
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

		product, ok := getProduct(w, r, uc)
		if !ok {
			return
		}

		if err := uc.delete(r.Context(), product, "products"); err != nil {
			deps.logger.Error(err, "Failed to delete Product", "name", product.Name)
			respondForbiddenOrError(w, err, "failed to delete product")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// getProduct fetches the Product named by the request path. It writes the
// HTTP error response itself and returns false when the request should not
// proceed.
func getProduct(w http.ResponseWriter, r *http.Request, uc *userClient) (*deliveryv1alpha1.Product, bool) {
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")

	product := &deliveryv1alpha1.Product{}
	if err := uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, product); err != nil {
		if client.IgnoreNotFound(err) == nil {
			respondError(w, http.StatusNotFound, fmt.Sprintf("product %s/%s not found", namespace, name))
			return nil, false
		}
		respondForbiddenOrError(w, err, "failed to get product")
		return nil, false
	}

	return product, true
}
