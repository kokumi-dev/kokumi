/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package credential resolves Pantry CRD references into OCI URLs and
// authenticated clients.
package credential

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// PantryResolver resolves Pantry CRD references into plain OCI URLs and
// optional authenticated clients.
type PantryResolver interface {
	ResolveSource(ctx context.Context, src deliveryv1alpha1.OCISource, defaultNamespace string) (deliveryv1alpha1.OCISource, oci.Client, error)
	ResolveDestination(ctx context.Context, dest *deliveryv1alpha1.OCIDestination, defaultDest, defaultNamespace, orderNamespace, orderName string) (string, oci.Client, error)
}

// KubeResolver resolves Pantry CRD references into OCI URLs and authenticated
// clients by fetching Pantry and Secret resources from the Kubernetes API.
type KubeResolver struct {
	Reader client.Reader
}

var _ PantryResolver = (*KubeResolver)(nil)

// NewKubeResolver returns a KubeResolver backed by r.
func NewKubeResolver(r client.Reader) *KubeResolver {
	return &KubeResolver{Reader: r}
}

// ResolveSource implements Resolver.
func (kr *KubeResolver) ResolveSource(ctx context.Context, src deliveryv1alpha1.OCISource, defaultNamespace string) (deliveryv1alpha1.OCISource, oci.Client, error) {
	if src.PantryRef == nil {
		return src, nil, nil
	}

	ref := src.PantryRef
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNamespace
	}

	pantry, ociClient, err := kr.resolveForPantry(ctx, ns, ref.Name)
	if err != nil {
		return deliveryv1alpha1.OCISource{}, nil, err
	}

	srcHost := extractOCIHost(src.OCI)
	registryHost := strings.TrimPrefix(pantry.Spec.Registry, "oci://")
	if srcHost != registryHost {
		return deliveryv1alpha1.OCISource{}, nil, fmt.Errorf("source OCI host %q does not match pantry %q registry %q", srcHost, ref.Name, registryHost)
	}

	return src, ociClient, nil
}

// ResolveDestination implements Resolver.
func (kr *KubeResolver) ResolveDestination(ctx context.Context, dest *deliveryv1alpha1.OCIDestination, defaultDest, defaultNamespace, orderNamespace, orderName string) (string, oci.Client, error) {
	if dest == nil || dest.OCI == "" {
		// No explicit destination — use in-cluster default; pantryRef ignored.
		return defaultDest, nil, nil
	}

	if dest.PantryRef == nil {
		return dest.OCI, nil, nil
	}

	ref := dest.PantryRef
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNamespace
	}

	pantry, ociClient, err := kr.resolveForPantry(ctx, ns, ref.Name)
	if err != nil {
		return "", nil, err
	}

	destHost := extractOCIHost(dest.OCI)
	registryHost := strings.TrimPrefix(pantry.Spec.Registry, "oci://")
	if destHost != registryHost {
		return "", nil, fmt.Errorf("destination OCI host %q does not match pantry %q registry %q", destHost, ref.Name, registryHost)
	}

	return dest.OCI, ociClient, nil
}

// extractOCIHost returns the registry host from an OCI URL.
// e.g. "oci://ghcr.io/myorg/myapp" → "ghcr.io"
func extractOCIHost(ociURL string) string {
	trimmed := strings.TrimPrefix(ociURL, "oci://")
	return strings.SplitN(trimmed, "/", 2)[0]
}

// ClientForPantry returns an authenticated OCI client for the named Pantry.
// Returns a nil client when the Pantry has no secretRef.
func (kr *KubeResolver) ClientForPantry(ctx context.Context, namespace, pantryName string) (oci.Client, error) {
	_, c, err := kr.resolveForPantry(ctx, namespace, pantryName)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// resolveForPantry fetches the named Pantry and its credential Secret, returning
// an authenticated ORAS client. Returns a nil client when no secretRef is set.
func (kr *KubeResolver) resolveForPantry(ctx context.Context, namespace, pantryName string) (*deliveryv1alpha1.Pantry, oci.Client, error) {
	var pantry deliveryv1alpha1.Pantry
	if err := kr.Reader.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      pantryName,
	}, &pantry); err != nil {
		return nil, nil, fmt.Errorf("get Pantry %q in namespace %q: %w", pantryName, namespace, err)
	}

	if pantry.Spec.SecretRef == nil {
		return &pantry, nil, nil
	}

	var secret corev1.Secret
	if err := kr.Reader.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      pantry.Spec.SecretRef.Name,
	}, &secret); err != nil {
		return nil, nil, fmt.Errorf("get credential Secret %q for Pantry %q: %w", pantry.Spec.SecretRef.Name, pantryName, err)
	}

	credData := secret.Data[".dockerconfigjson"]
	if len(credData) == 0 {
		return &pantry, nil, nil
	}

	credStore, err := oci.CredentialsFromDockerConfigJSON(credData)
	if err != nil {
		return nil, nil, fmt.Errorf("parse credentials for Pantry %q: %w", pantryName, err)
	}

	return &pantry, oci.NewAuthenticatedORASClient(credStore), nil
}
