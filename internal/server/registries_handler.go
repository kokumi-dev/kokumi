package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/namespace"
	"github.com/kokumi-dev/kokumi/internal/registry"
)

const (
	// credentialsSecretSuffix names the Secret the UI stores the
	// credentials of a Registry in: <registry>-credentials.
	credentialsSecretSuffix = "-credentials"
	// labelManagedBy marks the credential Secrets the UI manages. The UI
	// overwrites only Secrets that carry it.
	labelManagedBy = "app.kubernetes.io/managed-by"
	// managedByKokumi is the value of labelManagedBy on those Secrets.
	managedByKokumi = "kokumi"
)

// errSecretNotManaged reports a credential Secret the UI would overwrite but
// did not create.
var errSecretNotManaged = errors.New("secret is not managed by kokumi")

// handleListRegistries handles GET /api/v1/registries. An optional
// ?namespace= query parameter filters to one namespace.
func handleListRegistries(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var listOpts []client.ListOption
		if ns := r.URL.Query().Get("namespace"); ns != "" {
			listOpts = append(listOpts, client.InNamespace(ns))
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		list := &deliveryv1alpha1.RegistryList{}
		if err := uc.list(r.Context(), list, listOpts...); err != nil {
			respondForbiddenOrError(w, err, "failed to list registries")
			return
		}

		respondJSON(w, http.StatusOK, registriesToDTO(list.Items))
	}
}

// handleGetRegistry handles GET /api/v1/registries/{namespace}/{name}.
func handleGetRegistry(deps *apiDeps) http.HandlerFunc {
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

		reg, ok := getRegistry(w, r, uc)
		if !ok {
			return
		}

		respondJSON(w, http.StatusOK, registryToDTO(*reg))
	}
}

// handleCreateRegistry handles POST /api/v1/registries. With a username and
// a password it stores them in a kubernetes.io/dockerconfigjson Secret
// <name>-credentials that the Registry owns, so that deleting the Registry
// deletes it; secretRef names an existing Secret instead.
func handleCreateRegistry(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req CreateRegistryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.Name == "" || req.URL == "" {
			respondError(w, http.StatusBadRequest, "name and url are required")
			return
		}
		ns := req.Namespace
		if ns == "" {
			ns = namespace.Default
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		reg := &deliveryv1alpha1.Registry{
			Name:      req.Name,
			Namespace: ns,
			Spec: deliveryv1alpha1.RegistrySpec{
				URL:    req.URL,
				Scheme: deliveryv1alpha1.RegistryScheme(req.Scheme),
			},
		}
		setRegistryRefs(reg, req.SecretRef, req.CertSecretRef)

		var secret *corev1.Secret
		if req.Username != "" && req.Password != "" {
			reg.Spec.SecretRef = &corev1.LocalObjectReference{Name: req.Name + credentialsSecretSuffix}
			if secret, err = credentialsSecret(reg, req.Username, req.Password); err != nil {
				respondError(w, http.StatusBadRequest, err.Error())
				return
			}
		}

		writer, err := uc.writerForRegistry(r.Context(), "create", ns, registrySecretNames(reg))
		if err != nil {
			respondForbiddenOrError(w, err, "failed to create registry")
			return
		}
		// The Registry comes first: the Secret of an existing Registry of
		// that name is never written, and the Secret is owned by it.
		if secret != nil {
			if err := checkCredentialSecret(r.Context(), uc, ns, secret.Name); err != nil {
				respondSecretError(w, err)
				return
			}
		}
		if err := writer.Create(r.Context(), reg); err != nil {
			deps.logger.Error(err, "Failed to create Registry", "name", req.Name)
			respondForbiddenOrError(w, err, "failed to create registry")
			return
		}
		if secret != nil {
			secret.OwnerReferences = []metav1.OwnerReference{registryOwnerReference(reg)}
			if err := writeCredentialSecret(r.Context(), uc, secret); err != nil {
				deps.logger.Error(err, "Failed to store Registry credentials", "name", req.Name)
				respondSecretError(w, err)
				return
			}
		}

		respondJSON(w, http.StatusCreated, registryToDTO(*reg))
	}
}

// handleUpdateRegistry handles PUT /api/v1/registries/{namespace}/{name}.
func handleUpdateRegistry(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req UpdateRegistryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.URL == "" {
			respondError(w, http.StatusBadRequest, "url is required")
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		reg, ok := getRegistry(w, r, uc)
		if !ok {
			return
		}
		patch := client.MergeFrom(reg.DeepCopy())

		reg.Spec.URL = req.URL
		reg.Spec.Scheme = deliveryv1alpha1.RegistryScheme(req.Scheme)
		setRegistryRefs(reg, req.SecretRef, req.CertSecretRef)

		var secret *corev1.Secret
		if req.Username != "" && req.Password != "" {
			reg.Spec.SecretRef = &corev1.LocalObjectReference{Name: reg.Name + credentialsSecretSuffix}
			if secret, err = credentialsSecret(reg, req.Username, req.Password); err != nil {
				respondError(w, http.StatusBadRequest, err.Error())
				return
			}
		}

		writer, err := uc.writerForRegistry(r.Context(), "patch", reg.Namespace, registrySecretNames(reg))
		if err != nil {
			respondForbiddenOrError(w, err, "failed to update registry")
			return
		}
		// The Registry comes first, as on create: a refused patch leaves the
		// Secret as it was.
		if secret != nil {
			if err := checkCredentialSecret(r.Context(), uc, reg.Namespace, secret.Name); err != nil {
				respondSecretError(w, err)
				return
			}
		}
		if err := writer.Patch(r.Context(), reg, patch); err != nil {
			deps.logger.Error(err, "Failed to update Registry", "name", reg.Name)
			respondForbiddenOrError(w, err, "failed to update registry")
			return
		}
		if secret != nil {
			secret.OwnerReferences = []metav1.OwnerReference{registryOwnerReference(reg)}
			if err := writeCredentialSecret(r.Context(), uc, secret); err != nil {
				deps.logger.Error(err, "Failed to store Registry credentials", "name", reg.Name)
				respondSecretError(w, err)
				return
			}
		}

		respondJSON(w, http.StatusOK, registryToDTO(*reg))
	}
}

// handleDeleteRegistry handles DELETE /api/v1/registries/{namespace}/{name}.
// The credential Secret the UI created is owned by the Registry and goes
// with it.
func handleDeleteRegistry(deps *apiDeps) http.HandlerFunc {
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

		reg, ok := getRegistry(w, r, uc)
		if !ok {
			return
		}

		if err := uc.delete(r.Context(), reg, registriesResource); err != nil {
			deps.logger.Error(err, "Failed to delete Registry", "name", reg.Name)
			respondForbiddenOrError(w, err, "failed to delete registry")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// getRegistry reads the Registry named by the request path as uc. It writes
// the error response and reports false when it cannot be read.
func getRegistry(w http.ResponseWriter, r *http.Request, uc *userClient) (*deliveryv1alpha1.Registry, bool) {
	ns, name := r.PathValue("namespace"), r.PathValue("name")

	reg := &deliveryv1alpha1.Registry{}
	if err := uc.get(r.Context(), types.NamespacedName{Namespace: ns, Name: name}, reg); err != nil {
		if client.IgnoreNotFound(err) == nil {
			respondError(w, http.StatusNotFound, fmt.Sprintf("registry %s/%s not found", ns, name))
			return nil, false
		}
		respondForbiddenOrError(w, err, "failed to get registry")
		return nil, false
	}
	return reg, true
}

// setRegistryRefs sets the Secret references of reg; an empty name removes
// one.
func setRegistryRefs(reg *deliveryv1alpha1.Registry, secretRef, certSecretRef string) {
	reg.Spec.SecretRef, reg.Spec.CertSecretRef = nil, nil
	if secretRef != "" {
		reg.Spec.SecretRef = &corev1.LocalObjectReference{Name: secretRef}
	}
	if certSecretRef != "" {
		reg.Spec.CertSecretRef = &corev1.LocalObjectReference{Name: certSecretRef}
	}
}

// registrySecretNames returns the Secrets reg names.
func registrySecretNames(reg *deliveryv1alpha1.Registry) []string {
	var names []string
	if ref := reg.Spec.SecretRef; ref != nil {
		names = append(names, ref.Name)
	}
	if ref := reg.Spec.CertSecretRef; ref != nil && (len(names) == 0 || names[0] != ref.Name) {
		names = append(names, ref.Name)
	}
	return names
}

// credentialsSecret returns the Secret, named by reg.Spec.SecretRef, that
// holds username and password for the host of reg.
func credentialsSecret(reg *deliveryv1alpha1.Registry, username, password string) (*corev1.Secret, error) {
	data, err := dockerConfigJSON(reg.Spec.URL, username, password)
	if err != nil {
		return nil, err
	}
	return &corev1.Secret{
		Name:      reg.Spec.SecretRef.Name,
		Namespace: reg.Namespace,
		Type:      corev1.SecretTypeDockerConfigJson,
		Data:      map[string][]byte{corev1.DockerConfigJsonKey: data},
	}, nil
}

// registryOwnerReference makes reg the owner of its credential Secret.
// It does not block the Registry's deletion, which would need update
// rights on registries/finalizers.
func registryOwnerReference(reg *deliveryv1alpha1.Registry) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         deliveryv1alpha1.GroupVersion.String(),
		Kind:               "Registry",
		Name:               reg.Name,
		UID:                reg.UID,
		BlockOwnerDeletion: new(false),
	}
}

// respondSecretError writes the response for a failure to store
// credentials: 409 for a Secret the UI does not manage.
func respondSecretError(w http.ResponseWriter, err error) {
	if errors.Is(err, errSecretNotManaged) {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondForbiddenOrError(w, err, "failed to store the credential Secret")
}

// dockerConfigJSON returns a .dockerconfigjson payload with username and
// password for the host of the registry url.
func dockerConfigJSON(url, username, password string) ([]byte, error) {
	loc, err := registry.ParseLocation(url)
	if err != nil {
		return nil, err
	}

	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return json.Marshal(map[string]any{
		"auths": map[string]any{
			loc.Host: map[string]string{"auth": auth},
		},
	})
}
