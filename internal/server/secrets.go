package server

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// writeCredentialSecret stores the type, data and owner of desired as uc:
// it creates the Secret, or updates the Secret of its name the UI created
// before. A Secret of that name the UI did not create is left alone, with
// errSecretNotManaged.
//
// An object that names a credential Secret is created first and the Secret
// after it, owned by it, so that deleting the object deletes the Secret. The
// admission policies check that the author may get the Secret, not that it
// exists, and the object reports a Secret that is still missing.
func writeCredentialSecret(ctx context.Context, uc *userClient, desired *corev1.Secret) error {
	existing := &corev1.Secret{}
	err := uc.get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case apierrors.IsNotFound(err):
		if desired.Labels == nil {
			desired.Labels = map[string]string{}
		}
		desired.Labels[labelManagedBy] = managedByKokumi
		return uc.create(ctx, desired, secretsResource)
	case err != nil:
		return err
	case existing.Labels[labelManagedBy] != managedByKokumi:
		return fmt.Errorf("%w: Secret %q exists and Kokumi did not create it; delete it, or use another name", errSecretNotManaged, desired.Name)
	}

	patch := client.MergeFrom(existing.DeepCopy())
	existing.Type, existing.Data = desired.Type, desired.Data
	if len(desired.OwnerReferences) > 0 {
		existing.OwnerReferences = desired.OwnerReferences
	}
	return uc.patch(ctx, existing, patch, secretsResource)
}

// checkCredentialSecret fails with errSecretNotManaged when a Secret name in
// namespace exists that the UI did not create, before an object that names
// it is created.
func checkCredentialSecret(ctx context.Context, uc *userClient, namespace, name string) error {
	existing := &corev1.Secret{}
	err := uc.get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, existing)
	switch {
	case apierrors.IsNotFound(err):
		return nil
	case err != nil:
		return err
	case existing.Labels[labelManagedBy] != managedByKokumi:
		return fmt.Errorf("%w: Secret %q exists and Kokumi did not create it; delete it, or use another name", errSecretNotManaged, name)
	}
	return nil
}
