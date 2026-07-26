package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handleListPantries handles GET /api/v1/pantries.
// Lists Pantries across all namespaces. An optional ?namespace= query param
// can be used to filter to a specific namespace.
func handleListPantries(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		listOpts := []client.ListOption{}
		if ns := r.URL.Query().Get("namespace"); ns != "" {
			listOpts = append(listOpts, client.InNamespace(ns))
		}

		pantryList := &deliveryv1alpha1.PantryList{}
		if err := deps.reader.List(r.Context(), pantryList, listOpts...); err != nil {
			deps.logger.Error(err, "Failed to list Pantries")
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list pantries: %s", err))
			return
		}

		respondJSON(w, http.StatusOK, pantriesFromList(*pantryList))
	}
}

// handleGetPantry handles GET /api/v1/pantries/{namespace}/{name}.
func handleGetPantry(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		pantry := &deliveryv1alpha1.Pantry{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}, pantry); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("pantry %q not found", name))
				return
			}
			deps.logger.Error(err, "Failed to get Pantry", "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get pantry: %s", err))
			return
		}

		respondJSON(w, http.StatusOK, pantryToDTO(*pantry))
	}
}

// handleCreatePantry handles POST /api/v1/pantries.
// The request body must include a namespace field. When username and password
// are provided it creates a kubernetes.io/dockerconfigjson Secret in the same
// namespace as the Pantry and links it via spec.secretRef.
func handleCreatePantry(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req CreatePantryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.Name == "" {
			respondError(w, http.StatusBadRequest, "name is required")
			return
		}
		if req.URL == "" {
			respondError(w, http.StatusBadRequest, "url is required")
			return
		}
		namespace := req.Namespace
		if namespace == "" {
			namespace = defaultNamespace
		}

		var secretRef *corev1.LocalObjectReference

		switch {
		case req.Username != "" && req.Password != "":
			secretName := req.Name + "-registry-creds"
			if err := createDockerConfigSecret(r.Context(), deps, namespace, secretName, req.URL, req.Username, req.Password); err != nil {
				deps.logger.Error(err, "Failed to create registry credential Secret")
				respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create credential secret: %s", err))
				return
			}
			secretRef = &corev1.LocalObjectReference{Name: secretName}
		case req.SecretRef != "":
			secretRef = &corev1.LocalObjectReference{Name: req.SecretRef}
		}

		pantry := &deliveryv1alpha1.Pantry{
			ObjectMeta: metav1.ObjectMeta{
				Name:      req.Name,
				Namespace: namespace,
			},
			Spec: deliveryv1alpha1.PantrySpec{
				URL:         req.URL,
				Description: req.Description,
				SecretRef:   secretRef,
			},
		}

		if err := deps.writer.Create(r.Context(), pantry); err != nil {
			if apierrors.IsAlreadyExists(err) {
				respondError(w, http.StatusConflict, fmt.Sprintf("pantry %q already exists", req.Name))
				return
			}
			deps.logger.Error(err, "Failed to create Pantry", "name", req.Name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create pantry: %s", err))
			return
		}

		respondJSON(w, http.StatusCreated, pantryToDTO(*pantry))
	}
}

// handleUpdatePantry handles PUT /api/v1/pantries/{namespace}/{name}.
func handleUpdatePantry(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		var req UpdatePantryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.URL == "" {
			respondError(w, http.StatusBadRequest, "url is required")
			return
		}
		pantry := &deliveryv1alpha1.Pantry{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}, pantry); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("pantry %q not found", name))
				return
			}
			deps.logger.Error(err, "Failed to get Pantry", "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get pantry: %s", err))
			return
		}

		patch := client.MergeFrom(pantry.DeepCopy())

		// Update credentials if provided.
		switch {
		case req.Username != "" && req.Password != "":
			secretName := name + "-registry-creds"
			if err := upsertDockerConfigSecret(r.Context(), deps, namespace, secretName, req.URL, req.Username, req.Password); err != nil {
				deps.logger.Error(err, "Failed to upsert registry credential Secret")
				respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update credential secret: %s", err))
				return
			}
			pantry.Spec.SecretRef = &corev1.LocalObjectReference{Name: secretName}
		case req.SecretRef != "":
			pantry.Spec.SecretRef = &corev1.LocalObjectReference{Name: req.SecretRef}
		}

		pantry.Spec.URL = req.URL
		pantry.Spec.Description = req.Description

		if err := deps.writer.Patch(r.Context(), pantry, patch); err != nil {
			deps.logger.Error(err, "Failed to update Pantry", "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update pantry: %s", err))
			return
		}

		respondJSON(w, http.StatusOK, pantryToDTO(*pantry))
	}
}

// handleDeletePantry handles DELETE /api/v1/pantries/{namespace}/{name}.
func handleDeletePantry(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		pantry := &deliveryv1alpha1.Pantry{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}, pantry); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("pantry %q not found", name))
				return
			}
			deps.logger.Error(err, "Failed to get Pantry for deletion", "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get pantry: %s", err))
			return
		}

		if err := deps.writer.Delete(r.Context(), pantry); err != nil {
			deps.logger.Error(err, "Failed to delete Pantry", "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete pantry: %s", err))
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// createDockerConfigSecret creates a new kubernetes.io/dockerconfigjson Secret.
func createDockerConfigSecret(ctx context.Context, deps *apiDeps, namespace, name, registry, username, password string) error {
	data, err := buildDockerConfigJSON(registry, username, password)
	if err != nil {
		return err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: data,
		},
	}

	return deps.writer.Create(ctx, secret)
}

// upsertDockerConfigSecret creates or updates a kubernetes.io/dockerconfigjson Secret.
func upsertDockerConfigSecret(ctx context.Context, deps *apiDeps, namespace, name, registry, username, password string) error {
	data, err := buildDockerConfigJSON(registry, username, password)
	if err != nil {
		return err
	}

	var secret corev1.Secret
	getErr := deps.reader.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, &secret)

	if apierrors.IsNotFound(getErr) {
		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{corev1.DockerConfigJsonKey: data},
		}
		return deps.writer.Create(ctx, newSecret)
	}
	if getErr != nil {
		return getErr
	}

	patch := client.MergeFrom(secret.DeepCopy())
	secret.Data = map[string][]byte{corev1.DockerConfigJsonKey: data}
	return deps.writer.Patch(ctx, &secret, patch)
}

// buildDockerConfigJSON produces a minimal .dockerconfigjson payload for a
// single registry host from the given credentials.
func buildDockerConfigJSON(url, username, password string) ([]byte, error) {
	host := oci.ExtractHost(url)

	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))

	cfg := map[string]any{
		"auths": map[string]any{
			host: map[string]any{
				"auth": auth,
			},
		},
	}

	return json.Marshal(cfg)
}
