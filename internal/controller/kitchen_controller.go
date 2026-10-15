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

package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/sigstore/cosign/v3/pkg/cosign"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// KitchenReconciler reconciles a Kitchen object
type KitchenReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	// SigningNamespace holds the signing key Secret, apart from everything
	// users may be granted access to.
	SigningNamespace string
	// Signing is reloaded whenever the Kitchen or its signing key changes,
	// so that the controllers that sign and verify use the current trust.
	Signing *signing.Provider
}

// minTokenSigningKeyLen is the minimum accepted HMAC signing-key length (bytes).
const minTokenSigningKeyLen = 32

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// The Kitchen is a self-healing singleton: a missing object is recreated so
// the UI always has a resource to read/write. There is no finalizer because
// there is nothing to clean up.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/reconcile
func (r *KitchenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Kitchen", "namespace", req.Namespace, "name", req.Name)

	// Singleton: only the "default" Kitchen in the install namespace is managed.
	if req.Name != deliveryv1alpha1.DefaultKitchenName || req.Namespace != r.Namespace {
		return ctrl.Result{}, nil
	}

	kitchen := &deliveryv1alpha1.Kitchen{}
	if err := r.Get(ctx, req.NamespacedName, kitchen); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "Failed to get Kitchen")
			return ctrl.Result{}, fmt.Errorf("failed to get Kitchen: %w", err)
		}
		// Ensure the singleton exists so the UI always has a resource to read/write.
		kitchen = &deliveryv1alpha1.Kitchen{
			Name:      deliveryv1alpha1.DefaultKitchenName,
			Namespace: r.Namespace,
		}
		if err := r.Create(ctx, kitchen); err != nil && !apierrors.IsAlreadyExists(err) {
			logger.Error(err, "Failed to create default Kitchen")
			return ctrl.Result{}, err
		}
	}

	return r.reconcileKitchen(ctx, kitchen)
}

// reconcileKitchen ensures the signing key and the auth-related Secrets and
// records the Kitchen's readiness.
func (r *KitchenReconciler) reconcileKitchen(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen) (ctrl.Result, error) {
	updater := status.NewKitchenUpdater(r.Client)

	ready, err := r.reconcileSigningKey(ctx, kitchen, updater)
	if !ready || err != nil {
		return ctrl.Result{}, err
	}

	if err := r.validateAuth(ctx, kitchen); err != nil {
		return ctrl.Result{}, updater.Failed(ctx, kitchen, err)
	}

	return ctrl.Result{}, updater.Ready(ctx, kitchen, "Kitchen is valid and available")
}

// reconcileSigningKey generates the signing key when it does not exist,
// reloads the signing state, and publishes the public key. It reports whether
// the key is ready.
func (r *KitchenReconciler) reconcileSigningKey(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen, updater *status.KitchenUpdater) (bool, error) {
	// The key is loaded even when generating it failed, so that a deleted key
	// is no longer used.
	generateErr := r.ensureSigningKeySecret(ctx)

	state, err := r.Signing.Load(ctx)
	if err != nil {
		if !errors.Is(err, signing.ErrKeyNotReady) {
			return false, errors.Join(generateErr, err)
		}
		if generateErr != nil {
			// Neither the key's namespace nor its RBAC is watched, so the
			// error requeues the Kitchen to retry.
			err := fmt.Errorf("generating signing key Secret: %w", generateErr)
			return false, errors.Join(updater.SigningKeyFailed(ctx, kitchen, deliveryv1alpha1.ReasonKeyMissing, err), err)
		}
		reason := deliveryv1alpha1.ReasonKeyInvalid
		if errors.Is(err, signing.ErrInvalidTrustedKey) {
			reason = deliveryv1alpha1.ReasonTrustedKeyInvalid
		}
		log.FromContext(ctx).Error(err, "Could not load signing key")
		return false, updater.SigningKeyFailed(ctx, kitchen, reason, err)
	}

	return true, updater.SigningKeyReady(ctx, kitchen, deliveryv1alpha1.KitchenSigningStatus{
		KeyID:     state.Signer.KeyID(),
		PublicKey: string(state.Signer.PublicKeyPEM()),
	})
}

// validateAuth ensures the token signing-key Secret and checks that the
// Secrets of the configured identity providers exist, so that a
// misconfiguration is visible instead of silently disabling login.
func (r *KitchenReconciler) validateAuth(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen) error {
	auth := kitchen.Spec.Auth
	if auth == nil {
		return nil
	}

	if err := r.ensureTokenSigningKeySecret(ctx, kitchen); err != nil {
		return fmt.Errorf("ensuring token signing-key Secret: %w", err)
	}

	// secretRefs are defaulted by CRD defaulting, so a missing ref is a
	// configuration error.
	if adminUser := auth.AdminUser; adminUser != nil && adminUser.Enabled != nil && *adminUser.Enabled {
		if adminUser.SecretRef == nil || adminUser.SecretRef.Name == "" {
			return errors.New("admin credentials secretRef not set")
		}
		if err := r.requireSecretKey(ctx, kitchen.Namespace, adminUser.SecretRef.Name, "password-hash"); err != nil {
			return fmt.Errorf("admin credentials: %w", err)
		}
	}

	// The token signing key is validated at runtime by the server.
	if oidc := auth.OIDC; oidc != nil && oidc.IssuerURL != "" && oidc.ClientID != "" {
		if oidc.ClientSecretRef == nil || oidc.ClientSecretRef.Name == "" {
			return errors.New("oidc client secretRef not set")
		}
		if err := r.requireSecretKey(ctx, kitchen.Namespace, oidc.ClientSecretRef.Name, "client-secret"); err != nil {
			return fmt.Errorf("oidc client: %w", err)
		}
	}

	return nil
}

// requireSecretKey checks that the Secret name holds key.
func (r *KitchenReconciler) requireSecretKey(ctx context.Context, namespace, name, key string) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil || secret.Data[key] == nil {
		return fmt.Errorf("secret %q missing or incomplete", name)
	}
	return nil
}

// ensureDefaultRunner returns a manager.Runnable that creates the singleton
// Kitchen/default once on startup if it does not already exist.
func (r *KitchenReconciler) ensureDefaultRunner(mgr manager.Manager) manager.RunnableFunc {
	return func(ctx context.Context) error {
		// Use the uncached API reader so this works before the informer cache
		// has started.
		reader := mgr.GetAPIReader()
		kitchen := &deliveryv1alpha1.Kitchen{}
		err := reader.Get(ctx, client.ObjectKey{
			Namespace: r.Namespace,
			Name:      deliveryv1alpha1.DefaultKitchenName,
		}, kitchen)
		if err == nil {
			return nil
		}
		if !apierrors.IsNotFound(err) {
			return err
		}
		kitchen = &deliveryv1alpha1.Kitchen{
			Name:      deliveryv1alpha1.DefaultKitchenName,
			Namespace: r.Namespace,
		}
		if err := r.Create(ctx, kitchen); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		return nil
	}
}

// ensureSigningKeySecret generates the signing key Secret in cosign's layout
// when it does not exist. A key brought by a cluster admin, for example with
// "cosign generate-key-pair k8s://kokumi-signing/kokumi-signing-key", is kept.
func (r *KitchenReconciler) ensureSigningKeySecret(ctx context.Context) error {
	namespace := r.SigningNamespace
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: deliveryv1alpha1.SigningKeySecretName}, secret)
	if err == nil || !apierrors.IsNotFound(err) {
		return err
	}

	password, err := generateSigningKey()
	if err != nil {
		return fmt.Errorf("generating signing key password: %w", err)
	}
	keys, err := cosign.GenerateKeyPair(func(bool) ([]byte, error) { return password, nil })
	if err != nil {
		return fmt.Errorf("generating signing key: %w", err)
	}

	secret = &corev1.Secret{
		Name:      deliveryv1alpha1.SigningKeySecretName,
		Namespace: namespace,
		Labels:    kitchenSecretLabels("controller"),
		Data: map[string][]byte{
			signing.SecretPrivateKey: keys.PrivateBytes,
			signing.SecretPassword:   password,
			signing.SecretPublicKey:  keys.PublicBytes,
		},
	}
	if err := r.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	log.FromContext(ctx).Info("Generated signing key", "namespace", namespace, "secret", deliveryv1alpha1.SigningKeySecretName)
	return nil
}

// ensureTokenSigningKeySecret makes sure the token signing-key Secret exists.
func (r *KitchenReconciler) ensureTokenSigningKeySecret(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen) error {
	namespace := kitchen.Namespace
	name := deliveryv1alpha1.DefaultTokenSigningKeySecretName
	if ref := kitchen.Spec.Auth.TokenSigningKeySecretRef; ref != nil && ref.Name != "" {
		name = ref.Name
	}

	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := r.Get(ctx, key, secret); err == nil {
		if len(secret.Data["signing-key"]) < minTokenSigningKeyLen {
			return fmt.Errorf("token signing-key Secret %q must contain a %q of at least %d bytes", name, "signing-key", minTokenSigningKeyLen)
		}
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	generated, err := generateSigningKey()
	if err != nil {
		return fmt.Errorf("generating token signing key: %w", err)
	}
	secret = &corev1.Secret{
		Name:      name,
		Namespace: namespace,
		Labels:    kitchenSecretLabels("server"),
		Data: map[string][]byte{
			"signing-key": generated,
		},
	}
	if err := r.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		if rerr := r.Get(ctx, key, secret); rerr != nil {
			return rerr
		}
		if len(secret.Data["signing-key"]) < minTokenSigningKeyLen {
			return fmt.Errorf("token signing-key Secret %q created concurrently but lacks a valid %q", name, "signing-key")
		}
	}
	return nil
}

// kitchenSecretLabels returns the labels of a Secret the Kitchen controller
// generates for component.
func kitchenSecretLabels(component string) map[string]string {
	const app = "kokumi"
	return map[string]string{
		"app.kubernetes.io/name":       app,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": app,
	}
}

// generateSigningKey returns a random 32-byte base64-encoded signing key.
func generateSigningKey() ([]byte, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	out := make([]byte, base64.StdEncoding.EncodedLen(len(buf)))
	base64.StdEncoding.Encode(out, buf)
	return out, nil
}

// isKitchenSecret filters Secret events to the Secrets the Kitchen depends
// on: the auth Secrets, which may have any name in the install namespace, and
// the signing key.
func (r *KitchenReconciler) isKitchenSecret() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == r.Namespace ||
			obj.GetNamespace() == r.SigningNamespace && obj.GetName() == deliveryv1alpha1.SigningKeySecretName
	})
}

// mapSecretToKitchen maps a Secret of the Kitchen to the singleton Kitchen so
// its signing key is reloaded and its readiness re-evaluated.
func (r *KitchenReconciler) mapSecretToKitchen(_ context.Context, _ client.Object) []ctrl.Request {
	return []ctrl.Request{
		{Namespace: r.Namespace, Name: deliveryv1alpha1.DefaultKitchenName},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *KitchenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Ensure the singleton exists at startup so the UI always has a resource
	// to read/write, even when no Kitchen has ever been created.
	if err := mgr.Add(r.ensureDefaultRunner(mgr)); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Kitchen{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapSecretToKitchen),
			builder.WithPredicates(r.isKitchenSecret()),
		).
		Named("kitchen").
		Complete(r)
}
