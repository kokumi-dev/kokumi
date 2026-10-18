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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
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
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/resources"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

// minTokenSigningKeyLen is the minimum accepted HMAC signing-key length (bytes).
const minTokenSigningKeyLen = 32

// kitchenSigning reloads the signing state from the Kitchen and its key.
// *signing.Provider implements it.
type kitchenSigning interface {
	Load(ctx context.Context) (signing.State, error)
}

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
	Signing kitchenSigning
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile ensures the signing key and the auth Secrets of the Kitchen and
// records its readiness.
//
// The Kitchen is a self-healing singleton: a missing object is recreated so
// the UI always has a resource to read/write. There is no finalizer because
// there is nothing to clean up.
func (r *KitchenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Kitchen", "namespace", req.Namespace, "name", req.Name)

	// Singleton: only the "default" Kitchen in the install namespace is managed.
	if req.Name != deliveryv1alpha1.DefaultKitchenName || req.Namespace != r.Namespace {
		return ctrl.Result{}, nil
	}

	kitchen := &deliveryv1alpha1.Kitchen{}
	if err := r.Get(ctx, req.NamespacedName, kitchen); err != nil {
		if apierrors.IsNotFound(err) {
			// Its creation reconciles it.
			logger.Info("Kitchen resource not found, creating it")
			return ctrl.Result{}, r.createKitchen(ctx)
		}

		logger.Error(err, "Failed to get Kitchen")

		return ctrl.Result{}, fmt.Errorf("failed to get Kitchen: %w", err)
	}

	before := kitchen.Status.DeepCopy()
	defer func() { result, err = r.patchStatus(ctx, kitchen, before, result, err) }()

	if !kitchen.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	return r.reconcileKitchen(ctx, kitchen)
}

// reconcileKitchen loads the signing key and checks the auth Secrets. The
// Kitchen is ready once both are usable.
func (r *KitchenReconciler) reconcileKitchen(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen) (ctrl.Result, error) {
	ready, err := r.reconcileSigningKey(ctx, kitchen)
	if !ready || err != nil {
		return ctrl.Result{}, err
	}

	if err := r.validateAuth(ctx, kitchen); err != nil {
		// A change to the Kitchen or its Secrets reconciles again.
		conditions.MarkFalse(kitchen, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonFailed, err.Error())
		return ctrl.Result{}, nil
	}

	conditions.MarkTrue(kitchen, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonReady, "Kitchen is valid and available")
	return ctrl.Result{}, nil
}

// reconcileSigningKey generates the signing key when it does not exist,
// reloads the signing state, and publishes the public key. It reports whether
// the key is ready.
func (r *KitchenReconciler) reconcileSigningKey(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen) (bool, error) {
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
			markSigningKeyFailed(kitchen, deliveryv1alpha1.ReasonKeyMissing, err)
			return false, err
		}
		reason := deliveryv1alpha1.ReasonKeyInvalid
		if errors.Is(err, signing.ErrInvalidTrustedKey) {
			reason = deliveryv1alpha1.ReasonTrustedKeyInvalid
		}
		log.FromContext(ctx).Error(err, "Could not load signing key")
		markSigningKeyFailed(kitchen, reason, err)
		return false, nil
	}

	keyID := state.Signer.KeyID()
	kitchen.Status.Signing = &deliveryv1alpha1.KitchenSigningStatus{KeyID: keyID, PublicKey: string(state.Signer.PublicKeyPEM())}
	conditions.MarkTrue(kitchen, deliveryv1alpha1.ConditionTypeSigningKeyReady, deliveryv1alpha1.ReasonKeyReady, "Signing key "+keyID+" loaded")
	return true, nil
}

// markSigningKeyFailed records that the signing key cannot be used, for
// reason. The published public key is kept, since artifacts it signed still
// verify.
func markSigningKeyFailed(kitchen *deliveryv1alpha1.Kitchen, reason string, err error) {
	conditions.MarkFalse(kitchen, deliveryv1alpha1.ConditionTypeSigningKeyReady, reason, err.Error())
	conditions.MarkFalse(kitchen, deliveryv1alpha1.ConditionTypeReady, reason, err.Error())
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

// ensureSigningKeySecret generates the signing key Secret in cosign's layout
// when it does not exist. A key brought by a cluster admin, for example with
// "cosign generate-key-pair k8s://kokumi-signing/kokumi-signing-key", is kept.
func (r *KitchenReconciler) ensureSigningKeySecret(ctx context.Context) error {
	err := r.Get(ctx, client.ObjectKey{Namespace: r.SigningNamespace, Name: deliveryv1alpha1.SigningKeySecretName}, &corev1.Secret{})
	if err == nil || !apierrors.IsNotFound(err) {
		return err
	}

	key, err := signing.GenerateKey()
	if err != nil {
		return err
	}
	if err := r.Create(ctx, resources.SigningKeySecret(r.SigningNamespace, key)); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	log.FromContext(ctx).Info("Generated signing key", "namespace", r.SigningNamespace, "secret", deliveryv1alpha1.SigningKeySecretName)
	return nil
}

// ensureTokenSigningKeySecret makes sure the token signing-key Secret exists.
func (r *KitchenReconciler) ensureTokenSigningKeySecret(ctx context.Context, kitchen *deliveryv1alpha1.Kitchen) error {
	name := deliveryv1alpha1.DefaultTokenSigningKeySecretName
	if ref := kitchen.Spec.Auth.TokenSigningKeySecretRef; ref != nil && ref.Name != "" {
		name = ref.Name
	}

	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: kitchen.Namespace, Name: name}
	if err := r.Get(ctx, key, secret); err == nil {
		if len(secret.Data[resources.TokenSigningKey]) < minTokenSigningKeyLen {
			return fmt.Errorf("token signing-key Secret %q must contain a %q of at least %d bytes", name, resources.TokenSigningKey, minTokenSigningKeyLen)
		}
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	generated, err := generateTokenSigningKey()
	if err != nil {
		return fmt.Errorf("generating token signing key: %w", err)
	}
	if err := r.Create(ctx, resources.TokenSigningKeySecret(kitchen.Namespace, name, generated)); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		if rerr := r.Get(ctx, key, secret); rerr != nil {
			return rerr
		}
		if len(secret.Data[resources.TokenSigningKey]) < minTokenSigningKeyLen {
			return fmt.Errorf("token signing-key Secret %q created concurrently but lacks a valid %q", name, resources.TokenSigningKey)
		}
	}
	return nil
}

// generateTokenSigningKey returns a random 32-byte base64-encoded key.
func generateTokenSigningKey() ([]byte, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	out := make([]byte, base64.StdEncoding.EncodedLen(len(buf)))
	base64.StdEncoding.Encode(out, buf)
	return out, nil
}

// createKitchen creates the singleton Kitchen with every setting at its
// default.
func (r *KitchenReconciler) createKitchen(ctx context.Context) error {
	if err := r.Create(ctx, resources.Kitchen(r.Namespace)); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create Kitchen: %w", err)
	}
	return nil
}

// patchStatus writes the status kitchen took on in this reconcile, unless it
// is unchanged, and returns how to requeue: as the reconcile decided, or soon
// when the patch conflicts. The patch is locked to the version this reconcile
// read or last wrote, so that a reconcile from a stale cache never overwrites
// a newer status.
func (r *KitchenReconciler) patchStatus(
	ctx context.Context, kitchen *deliveryv1alpha1.Kitchen, before *deliveryv1alpha1.KitchenStatus, result ctrl.Result, err error,
) (ctrl.Result, error) {
	kitchen.Status.ObservedGeneration = kitchen.Generation
	if equality.Semantic.DeepEqual(*before, kitchen.Status) {
		return result, err
	}

	base := kitchen.DeepCopy()
	base.Status = *before
	perr := r.Status().Patch(ctx, kitchen, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	switch {
	case perr == nil, apierrors.IsNotFound(perr):
		return result, err
	case apierrors.IsConflict(perr) && err == nil:
		log.FromContext(ctx).V(1).Info("Kitchen changed during the reconcile, reconciling it again")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	default:
		return result, errors.Join(err, fmt.Errorf("failed to patch Kitchen status: %w", perr))
	}
}

// ensureKitchen returns a Runnable that creates the singleton Kitchen once on
// startup if it does not already exist. reader must work before the
// informer cache has started.
func (r *KitchenReconciler) ensureKitchen(reader client.Reader) manager.RunnableFunc {
	return func(ctx context.Context) error {
		err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: deliveryv1alpha1.DefaultKitchenName}, &deliveryv1alpha1.Kitchen{})
		if !apierrors.IsNotFound(err) {
			return err
		}
		return r.createKitchen(ctx)
	}
}

// SigningInputs returns a signing.ReadFunc that reads the signing key Secret
// from keyNamespace and the Kitchen from namespace with reader. Pass an
// uncached reader, so that a Secret the Kitchen controller has just created
// is seen.
func SigningInputs(reader client.Reader, namespace, keyNamespace string) signing.ReadFunc {
	return func(ctx context.Context) (signing.Inputs, error) {
		secret := &corev1.Secret{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: keyNamespace, Name: deliveryv1alpha1.SigningKeySecretName}, secret); err != nil {
			if apierrors.IsNotFound(err) {
				return signing.Inputs{}, fmt.Errorf("%w: Secret %s/%s not found", signing.ErrKeyNotReady, keyNamespace, deliveryv1alpha1.SigningKeySecretName)
			}
			return signing.Inputs{}, fmt.Errorf("reading signing key Secret: %w", err)
		}

		kitchen := &deliveryv1alpha1.Kitchen{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: deliveryv1alpha1.DefaultKitchenName}, kitchen); err != nil {
			if !apierrors.IsNotFound(err) {
				return signing.Inputs{}, fmt.Errorf("reading Kitchen: %w", err)
			}
			kitchen = nil
		}

		return signing.Inputs{KeyData: secret.Data, Kitchen: kitchen}, nil
	}
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
	if err := mgr.Add(r.ensureKitchen(mgr.GetAPIReader())); err != nil {
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
