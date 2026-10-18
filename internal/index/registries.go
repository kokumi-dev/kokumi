package index

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/registry"
)

// RegistryEntries reads the Registries refs name in namespace, with their
// Secrets, through reader. A Registry that does not exist fails the read:
// whoever listed it meant something that anonymous access would hide. A
// Secret that cannot be read only fails connections through its Registry.
func RegistryEntries(ctx context.Context, reader client.Reader, namespace string, refs []corev1.LocalObjectReference) ([]registry.Entry, error) {
	entries := make([]registry.Entry, 0, len(refs))
	for _, ref := range refs {
		reg := &deliveryv1alpha1.Registry{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, reg); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, registry.RegistryNotFound(ref.Name)
			}
			return nil, fmt.Errorf("failed to get Registry %q: %w", ref.Name, err)
		}
		entries = append(entries, RegistryEntry(ctx, reader, reg))
	}
	return entries, nil
}

// RegistryEntry reads the Secrets reg names through reader.
func RegistryEntry(ctx context.Context, reader client.Reader, reg *deliveryv1alpha1.Registry) registry.Entry {
	entry := registry.Entry{Registry: reg}
	for _, s := range []struct {
		ref    *corev1.LocalObjectReference
		secret **corev1.Secret
	}{
		{reg.Spec.SecretRef, &entry.Secret},
		{reg.Spec.CertSecretRef, &entry.CertSecret},
	} {
		if s.ref == nil {
			continue
		}
		secret := &corev1.Secret{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: reg.Namespace, Name: s.ref.Name}, secret); err != nil {
			if apierrors.IsNotFound(err) {
				entry.Err = registry.SecretNotFound(s.ref.Name)
			} else {
				entry.Err = fmt.Errorf("read Secret %q: %w", s.ref.Name, err)
			}
			return entry
		}
		*s.secret = secret
	}
	return entry
}
