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
	"errors"
	"fmt"
	"slices"
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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/resources"
)

const (
	// registryPingTimeout bounds the connectivity check of a Registry.
	registryPingTimeout = 30 * time.Second
	// registryRecheckInterval is how often a Registry is checked again
	// although neither it nor its Secrets changed, so that credentials
	// that expire or are revoked show up.
	registryRecheckInterval = 10 * time.Minute
)

// registrySecretsField indexes Registries by the Secrets their secretRef and
// certSecretRef name.
const registrySecretsField = "registrySecretNames"

// registryConnector connects through a Registry, reading the Secrets it
// names. *registry.Resolver implements it.
type registryConnector interface {
	Connect(ctx context.Context, reg *deliveryv1alpha1.Registry) (registry.Connection, error)
}

// RegistryReconciler checks that Registries connect: that their Secrets are
// usable and that the registry accepts their credentials and TLS settings.
type RegistryReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Registries registryConnector
	// Namespace is the install namespace, where the Registry of the bundled
	// registry is created.
	Namespace string
	// BundledRegistryHost is the host of the bundled registry.
	BundledRegistryHost string
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile connects through the Registry and records in its Ready condition
// whether it connects. Registries carry no finalizer: they only report.
func (r *RegistryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Registry", "namespace", req.Namespace, "name", req.Name)

	reg := &deliveryv1alpha1.Registry{}
	if err := r.Get(ctx, req.NamespacedName, reg); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Registry resource not found, ignoring")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Registry")

		return ctrl.Result{}, fmt.Errorf("failed to get Registry: %w", err)
	}

	before := reg.Status.DeepCopy()
	defer func() { result, err = r.patchStatus(ctx, reg, before, result, err) }()

	if !reg.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	return r.reconcileRegistry(ctx, reg)
}

// reconcileRegistry connects through the Registry and pings its registry.
// A Registry is checked again periodically, so that credentials that expire
// or are revoked show up.
func (r *RegistryReconciler) reconcileRegistry(ctx context.Context, reg *deliveryv1alpha1.Registry) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	conn, err := r.Registries.Connect(ctx, reg)
	if err != nil {
		reason := registry.Reason(err, "")
		if reason == "" {
			return ctrl.Result{}, fmt.Errorf("failed to connect through Registry: %w", err)
		}
		// The Secret watch reconciles again once the Secret is fixed.
		conditions.MarkFalse(reg, deliveryv1alpha1.ConditionTypeReady, reason, err.Error())
		return ctrl.Result{}, nil
	}

	pingCtx, cancel := context.WithTimeout(ctx, registryPingTimeout)
	defer cancel()
	if err := conn.Client.Ping(pingCtx, conn.Host); err != nil {
		reason := registry.PingReason(err)
		logger.Info("Registry connectivity check failed", "url", reg.Spec.URL, "reason", reason, "error", err.Error())
		conditions.MarkFalse(reg, deliveryv1alpha1.ConditionTypeReady, reason, err.Error())
		if registry.Retriable(err) {
			// Retried with backoff: the registry may be down only briefly.
			return ctrl.Result{}, err
		}
		// Rejected credentials and untrusted certificates stay as they are
		// until the Registry or its Secrets change, which the watches see.
		return ctrl.Result{RequeueAfter: registryRecheckInterval}, nil
	}

	msg := conn.Host + " answered; Kokumi connects without credentials"
	if conn.Authenticated() {
		msg = "Connected to " + conn.Host + " with the credentials of the Registry"
	}
	conditions.MarkTrue(reg, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonRegistryConnected, msg)
	return ctrl.Result{RequeueAfter: registryRecheckInterval}, nil
}

// patchStatus writes the status reg took on in this reconcile, unless it is
// unchanged, and returns how to requeue: as the reconcile decided, or soon
// when the patch conflicts. The patch is locked to the version this reconcile
// read or last wrote, so that a reconcile from a stale cache never overwrites
// a newer status.
func (r *RegistryReconciler) patchStatus(
	ctx context.Context, reg *deliveryv1alpha1.Registry, before *deliveryv1alpha1.RegistryStatus, result ctrl.Result, err error,
) (ctrl.Result, error) {
	reg.Status.ObservedGeneration = reg.Generation
	if equality.Semantic.DeepEqual(*before, reg.Status) {
		return result, err
	}

	base := reg.DeepCopy()
	base.Status = *before
	perr := r.Status().Patch(ctx, reg, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	switch {
	case perr == nil, apierrors.IsNotFound(perr):
		return result, err
	case apierrors.IsConflict(perr) && err == nil:
		log.FromContext(ctx).V(1).Info("Registry changed during the reconcile, reconciling it again")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	default:
		return ctrl.Result{}, errors.Join(err, fmt.Errorf("failed to patch Registry status: %w", perr))
	}
}

// RegistryReader returns a registry.Reader that reads Registries and their
// Secrets with reader, for the registry.Resolver the controllers connect
// through.
func RegistryReader(reader client.Reader) registry.Reader {
	return registryReader{reader: reader}
}

type registryReader struct {
	reader client.Reader
}

func (r registryReader) Registry(ctx context.Context, namespace, name string) (*deliveryv1alpha1.Registry, error) {
	reg := &deliveryv1alpha1.Registry{}
	if err := r.reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, reg); err != nil {
		return nil, err
	}
	return reg, nil
}

func (r registryReader) Secret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := r.reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

// ensureBundledRegistry returns a Runnable that creates the Registry of the
// bundled registry in the install namespace on startup when it is missing,
// so that it is listed and checked like any other. An existing one is left
// as it is. reader must work before the informer cache has started.
func (r *RegistryReconciler) ensureBundledRegistry(reader client.Reader) manager.RunnableFunc {
	return func(ctx context.Context) error {
		reg := resources.BundledRegistry(r.Namespace, r.BundledRegistryHost)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(reg), &deliveryv1alpha1.Registry{}); !apierrors.IsNotFound(err) {
			return err
		}
		if err := r.Create(ctx, reg); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create the Registry of the bundled registry: %w", err)
		}
		log.FromContext(ctx).Info("Created Registry", "name", reg.Name, "namespace", reg.Namespace)
		return nil
	}
}

// registrySecrets returns the Secrets a Registry names, for
// registrySecretsField.
func registrySecrets(obj client.Object) []string {
	reg, ok := obj.(*deliveryv1alpha1.Registry)
	if !ok {
		return nil
	}
	var names []string
	if ref := reg.Spec.SecretRef; ref != nil {
		names = append(names, ref.Name)
	}
	if ref := reg.Spec.CertSecretRef; ref != nil && !slices.Contains(names, ref.Name) {
		names = append(names, ref.Name)
	}
	return names
}

// registriesForSecret enqueues the Registries that name the Secret.
func (r *RegistryReconciler) registriesForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	registries := &deliveryv1alpha1.RegistryList{}
	if err := r.List(ctx, registries,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{registrySecretsField: obj.GetName()},
	); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Registries for Secret", "secret", obj.GetName())
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(registries.Items))
	for i := range registries.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&registries.Items[i])})
	}
	return reqs
}

// SetupWithManager sets up the controller with the Manager. Status updates
// of a Registry do not reconcile it again; changes of its spec and its
// Secrets do.
func (r *RegistryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &deliveryv1alpha1.Registry{}, registrySecretsField, registrySecrets); err != nil {
		return err
	}
	if err := mgr.Add(r.ensureBundledRegistry(mgr.GetAPIReader())); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Registry{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.registriesForSecret),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{})).
		Named("registry").
		Complete(r)
}
