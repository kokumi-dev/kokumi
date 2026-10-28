package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/registry"
)

// Resources the server reads and writes as the user, besides Secrets.
const (
	registriesResource   = "registries"
	repositoriesResource = "repositories"
)

// registryCredentialsRole is the ClusterRole that grants reading the Secrets
// of Registries in a namespace.
const registryCredentialsRole = "kokumi-registry-credentials"

// userRegistryReader reads Registries and their Secrets as the user of a
// request, with a mapped ServiceAccount that may get the named object. The
// server reads through a Registry only for users who may read its Secret
// themselves, so the UI never lends credentials to its users.
type userRegistryReader struct {
	uc *userClient
}

// Registry implements registry.Reader.
func (u userRegistryReader) Registry(ctx context.Context, namespace, name string) (*deliveryv1alpha1.Registry, error) {
	reader, err := u.uc.readerForObject(ctx, "get", registriesResource, namespace, name)
	if err != nil {
		return nil, err
	}
	reg := &deliveryv1alpha1.Registry{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, reg); err != nil {
		return nil, err
	}
	return reg, nil
}

// Secret implements registry.Reader.
func (u userRegistryReader) Secret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	reader, err := u.uc.readerForObject(ctx, "get", secretsResource, namespace, name)
	if err != nil {
		if errors.Is(err, errNotAuthorized) {
			return nil, fmt.Errorf("you are %w to read it in namespace %q: a platform admin grants it by binding the ClusterRole %s there",
				errNotAuthorized, namespace, registryCredentialsRole)
		}
		return nil, err
	}
	secret := &corev1.Secret{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

// resolverFor returns the Resolver that connects as uc.
func (deps *apiDeps) resolverFor(uc *userClient) *registry.Resolver {
	return registry.NewResolver(deps.registries, userRegistryReader{uc: uc})
}

// scopeFor returns the Scope of an object in namespace that lists refs, for
// the user of r. Without refs it reads nothing, so it needs no identity.
func (deps *apiDeps) scopeFor(r *http.Request, namespace string, refs []corev1.LocalObjectReference) (registry.Scope, error) {
	if len(refs) == 0 {
		return deps.registries.Scope(nil), nil
	}
	uc, err := deps.resolveUserClient(r)
	if err != nil {
		return registry.Scope{}, err
	}
	return deps.userScope(r.Context(), uc, namespace, refs)
}

// userScope reads the Registries refs name in namespace, with their
// Secrets, as uc and returns the Scope they form.
func (deps *apiDeps) userScope(ctx context.Context, uc *userClient, namespace string, refs []corev1.LocalObjectReference) (registry.Scope, error) {
	return deps.resolverFor(uc).Scope(ctx, namespace, refs)
}

// connectFor returns the connection to url of an object in namespace that
// lists refs, for the user of r.
func (deps *apiDeps) connectFor(r *http.Request, namespace string, refs []corev1.LocalObjectReference, url string) (registry.Connection, error) {
	scope, err := deps.scopeFor(r, namespace, refs)
	if err != nil {
		return registry.Connection{}, err
	}
	return scope.Connect(url)
}

// preparationConnection returns the connection to the artifact of prep for
// uc: through the Registry recorded with it while it exists and still
// covers the artifact, and through the registryRefs of its Order otherwise.
func (deps *apiDeps) preparationConnection(ctx context.Context, uc *userClient, prep *deliveryv1alpha1.Preparation) (registry.Connection, error) {
	return deps.resolverFor(uc).ConnectRecorded(ctx, prep.Namespace, prep.Spec.Artifact.RegistryRef, prep.Spec.Artifact.OCIRef,
		func(ctx context.Context) ([]corev1.LocalObjectReference, error) {
			order := &deliveryv1alpha1.Order{}
			if err := uc.get(ctx, client.ObjectKey{Namespace: prep.Namespace, Name: prep.Spec.OrderName}, order); err != nil && !apierrors.IsNotFound(err) {
				return nil, err
			}
			return order.Spec.RegistryRefs, nil
		})
}

// respondConnectError writes the response for a failed connection: 403 when
// the user may not read a Registry or its Secret, 422 when a Registry is
// missing or unusable, and 502 otherwise.
func respondConnectError(w http.ResponseWriter, err error) {
	switch {
	case isDenied(err):
		respondError(w, http.StatusForbidden, err.Error())
	case registry.Reason(err, "") != "":
		respondError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		respondError(w, http.StatusBadGateway, "could not connect to the registry: "+err.Error())
	}
}

// registryRefsQuery returns the Registries named in the comma-separated
// registryRefs query parameter of r.
func registryRefsQuery(r *http.Request) []corev1.LocalObjectReference {
	var refs []corev1.LocalObjectReference
	for name := range strings.SplitSeq(r.URL.Query().Get("registryRefs"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			refs = append(refs, corev1.LocalObjectReference{Name: name})
		}
	}
	return refs
}
