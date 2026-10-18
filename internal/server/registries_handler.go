package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

		credentials := req.Username != "" && req.Password != ""
		if credentials {
			reg.Spec.SecretRef = &corev1.LocalObjectReference{Name: req.Name + credentialsSecretSuffix}
			// Refuse before creating anything when the Secret belongs to
			// someone else.
			if err := checkManagedSecret(r.Context(), uc, ns, reg.Spec.SecretRef.Name); err != nil {
				respondSecretError(w, err)
				return
			}
		}

		writer, err := uc.writerForRegistry(r.Context(), "create", ns, registrySecretNames(reg))
		if err != nil {
			respondForbiddenOrError(w, err, "failed to create registry")
			return
		}
		if err := writer.Create(r.Context(), reg); err != nil {
			if apierrors.IsAlreadyExists(err) {
				respondError(w, http.StatusConflict, fmt.Sprintf("registry %q already exists", req.Name))
				return
			}
			deps.logger.Error(err, "Failed to create Registry", "name", req.Name)
			respondForbiddenOrError(w, err, "failed to create registry")
			return
		}

		if credentials {
			if err := upsertCredentialsSecret(r.Context(), uc, reg, req.Username, req.Password); err != nil {
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

		credentials := req.Username != "" && req.Password != ""
		if credentials {
			reg.Spec.SecretRef = &corev1.LocalObjectReference{Name: reg.Name + credentialsSecretSuffix}
			if err := upsertCredentialsSecret(r.Context(), uc, reg, req.Username, req.Password); err != nil {
				deps.logger.Error(err, "Failed to store Registry credentials", "name", reg.Name)
				respondSecretError(w, err)
				return
			}
		}

		writer, err := uc.writerForRegistry(r.Context(), "patch", reg.Namespace, registrySecretNames(reg))
		if err != nil {
			respondForbiddenOrError(w, err, "failed to update registry")
			return
		}
		if err := writer.Patch(r.Context(), reg, patch); err != nil {
			deps.logger.Error(err, "Failed to update Registry", "name", reg.Name)
			respondForbiddenOrError(w, err, "failed to update registry")
			return
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

// checkManagedSecret fails with errSecretNotManaged when the named Secret
// exists and the UI did not create it.
func checkManagedSecret(ctx context.Context, uc *userClient, ns, name string) error {
	secret := &corev1.Secret{}
	if err := uc.get(ctx, types.NamespacedName{Namespace: ns, Name: name}, secret); err != nil {
		return client.IgnoreNotFound(err)
	}
	if secret.Labels[labelManagedBy] != managedByKokumi {
		return fmt.Errorf("%w: Secret %q exists; name it in secretRef instead, or delete it", errSecretNotManaged, name)
	}
	return nil
}

// upsertCredentialsSecret stores username and password for the host of reg
// in the Secret reg.Spec.SecretRef names, owned by reg. It creates the
// Secret, or updates it when the UI created it; any other Secret of that
// name is left alone.
func upsertCredentialsSecret(ctx context.Context, uc *userClient, reg *deliveryv1alpha1.Registry, username, password string) error {
	data, err := dockerConfigJSON(reg.Spec.URL, username, password)
	if err != nil {
		return err
	}

	name := reg.Spec.SecretRef.Name
	secret := &corev1.Secret{}
	err = uc.get(ctx, types.NamespacedName{Namespace: reg.Namespace, Name: name}, secret)
	switch {
	case apierrors.IsNotFound(err):
		secret = &corev1.Secret{
			Name:            name,
			Namespace:       reg.Namespace,
			Labels:          map[string]string{labelManagedBy: managedByKokumi},
			OwnerReferences: []metav1.OwnerReference{registryOwnerReference(reg)},
			Type:            corev1.SecretTypeDockerConfigJson,
			Data:            map[string][]byte{corev1.DockerConfigJsonKey: data},
		}
		return uc.create(ctx, secret, secretsResource)
	case err != nil:
		return err
	case secret.Labels[labelManagedBy] != managedByKokumi:
		return fmt.Errorf("%w: Secret %q exists; name it in secretRef instead, or delete it", errSecretNotManaged, name)
	}

	patch := client.MergeFrom(secret.DeepCopy())
	secret.Type = corev1.SecretTypeDockerConfigJson
	secret.Data = map[string][]byte{corev1.DockerConfigJsonKey: data}
	return uc.patch(ctx, secret, patch, secretsResource)
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
	respondForbiddenOrError(w, err, "failed to store the registry credentials")
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
