package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"oras.land/oras-go/v2/registry/remote/credentials"
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
		if req.Registry == "" {
			respondError(w, http.StatusBadRequest, "registry is required")
			return
		}
		namespace := req.Namespace
		if namespace == "" {
			namespace = defaultNamespace
		}

		var secretRef *corev1.LocalObjectReference

		if req.Username != "" && req.Password != "" {
			secretName := req.Name + "-registry-creds"
			if err := createDockerConfigSecret(r.Context(), deps, namespace, secretName, req.Registry, req.Username, req.Password); err != nil {
				deps.logger.Error(err, "Failed to create registry credential Secret")
				respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create credential secret: %s", err))
				return
			}
			secretRef = &corev1.LocalObjectReference{Name: secretName}
		}

		pantry := &deliveryv1alpha1.Pantry{
			ObjectMeta: metav1.ObjectMeta{
				Name:      req.Name,
				Namespace: namespace,
			},
			Spec: deliveryv1alpha1.PantrySpec{
				Registry:    req.Registry,
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
		if req.Registry == "" {
			respondError(w, http.StatusBadRequest, "registry is required")
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
		if req.Username != "" && req.Password != "" {
			secretName := name + "-registry-creds"
			if err := upsertDockerConfigSecret(r.Context(), deps, namespace, secretName, req.Registry, req.Username, req.Password); err != nil {
				deps.logger.Error(err, "Failed to upsert registry credential Secret")
				respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update credential secret: %s", err))
				return
			}
			pantry.Spec.SecretRef = &corev1.LocalObjectReference{Name: secretName}
		}

		pantry.Spec.Registry = req.Registry
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

// handleListPantryRepositories handles GET /api/v1/pantries/{namespace}/{name}/repositories.
// It lists all repositories available in the Pantry's backing registry.
func handleListPantryRepositories(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		pantry, credStore, err := resolvePantryCredentials(r.Context(), deps, namespace, name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				respondError(w, http.StatusNotFound, fmt.Sprintf("pantry %q not found", name))
				return
			}
			deps.logger.Error(err, "Failed to resolve Pantry credentials", "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to resolve pantry: %s", err))
			return
		}

		host := strings.TrimPrefix(pantry.Spec.Registry, "oci://")

		repos, err := oci.ListRepositories(r.Context(), host, credStore)
		if err != nil {
			deps.logger.Error(err, "Failed to list repositories", "host", host)
			respondError(w, http.StatusBadGateway, fmt.Sprintf("failed to list repositories: %s", err))
			return
		}

		out := make([]RepositoryDTO, len(repos))
		for i, repo := range repos {
			out[i] = RepositoryDTO{Name: repo}
		}
		respondJSON(w, http.StatusOK, out)
	}
}

// handleListPantryTags handles GET /api/v1/pantries/{namespace}/{name}/repositories/{repo...}
// where the repo path must end with /tags.
func handleListPantryTags(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		pantryNamespace := r.PathValue("namespace")
		pantryName := r.PathValue("name")
		raw := r.PathValue("repo")
		repoPath, ok := strings.CutSuffix(raw, "/tags")
		if !ok {
			respondError(w, http.StatusNotFound, "not found")
			return
		}

		pantry, credStore, err := resolvePantryCredentials(r.Context(), deps, pantryNamespace, pantryName)
		if err != nil {
			if apierrors.IsNotFound(err) {
				respondError(w, http.StatusNotFound, fmt.Sprintf("pantry %q not found", pantryName))
				return
			}
			deps.logger.Error(err, "Failed to resolve Pantry credentials", "name", pantryName)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to resolve pantry: %s", err))
			return
		}

		host := strings.TrimPrefix(pantry.Spec.Registry, "oci://")
		fullRef := fmt.Sprintf("%s/%s", host, repoPath)

		var ociClient oci.Client
		if credStore != nil {
			ociClient = oci.NewAuthenticatedORASClient(credStore)
		} else {
			ociClient = deps.ociClient
		}

		tags, err := ociClient.ListTags(r.Context(), fullRef)
		if err != nil {
			deps.logger.Error(err, "Failed to list tags", "ref", fullRef)
			respondError(w, http.StatusBadGateway, fmt.Sprintf("failed to list tags: %s", err))
			return
		}

		respondJSON(w, http.StatusOK, tags)
	}
}

// resolvePantryCredentials fetches the named Pantry and optionally its
// referenced credential Secret. It returns the Pantry object and a credentials
// Store (nil when the Pantry has no secretRef or the Secret has no data).
func resolvePantryCredentials(ctx context.Context, deps *apiDeps, namespace, pantryName string) (*deliveryv1alpha1.Pantry, credentials.Store, error) {
	pantry := &deliveryv1alpha1.Pantry{}
	if err := deps.reader.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      pantryName,
	}, pantry); err != nil {
		return nil, nil, err
	}

	if pantry.Spec.SecretRef == nil {
		return pantry, nil, nil
	}

	var secret corev1.Secret
	if err := deps.reader.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      pantry.Spec.SecretRef.Name,
	}, &secret); err != nil {
		return nil, nil, fmt.Errorf("get credential secret: %w", err)
	}

	credData := secret.Data[".dockerconfigjson"]
	if len(credData) == 0 {
		return pantry, nil, nil
	}

	credStore, err := oci.CredentialsFromDockerConfigJSON(credData)
	if err != nil {
		return nil, nil, fmt.Errorf("parse credentials: %w", err)
	}

	return pantry, credStore, nil
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
func buildDockerConfigJSON(registry, username, password string) ([]byte, error) {
	host := strings.TrimPrefix(registry, "oci://")

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
