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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/index"
	"github.com/kokumi-dev/kokumi/internal/registry"
)

const (
	// registryPingTimeout bounds the connectivity check of a Registry.
	registryPingTimeout = 30 * time.Second
	// registryRecheckInterval is how often a Registry is checked again
	// although neither it nor its Secrets changed, so that credentials
	// that expire or are revoked show up.
	registryRecheckInterval = 10 * time.Minute
)

// RegistryReconciler checks that Registries connect: that their Secrets are
// usable and that the registry accepts their credentials and TLS settings.
type RegistryReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Registries registryConnector
	// Namespace is the install namespace, where the Registry of the bundled
	// registry is created.
	Namespace string
	// BundledHost is the host of the bundled registry.
	BundledHost string
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// untrustedCertificate reports whether err is a TLS certificate the client
// does not trust, which is a matter of configuration, not of availability.
func untrustedCertificate(err error) bool {
	var verification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &verification) || errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid)
}

// Reconcile connects to the Registry's registry and records the outcome in
// its Ready condition.
func (r *RegistryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reg := &deliveryv1alpha1.Registry{}
	if err := r.Get(ctx, req.NamespacedName, reg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ready, result, checkErr := r.check(ctx, reg)

	before := reg.DeepCopy()
	if ready != nil {
		reg.Status.ObservedGeneration = reg.Generation
		meta.SetStatusCondition(&reg.Status.Conditions, *ready)
	}
	if !equality.Semantic.DeepEqual(before.Status, reg.Status) {
		if err := r.Status().Patch(ctx, reg, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
	}
	return result, checkErr
}

// check connects through reg and returns its Ready condition, nil when the
// check could not be made, with how to requeue.
func (r *RegistryReconciler) check(ctx context.Context, reg *deliveryv1alpha1.Registry) (*metav1.Condition, ctrl.Result, error) {
	condition := func(conditionStatus metav1.ConditionStatus, reason, msg string) *metav1.Condition {
		return &metav1.Condition{
			Type: deliveryv1alpha1.ConditionTypeReady, Status: conditionStatus, Reason: reason, Message: msg, ObservedGeneration: reg.Generation,
		}
	}

	conn, err := r.Registries.Connect(index.RegistryEntry(ctx, r.Client, reg))
	if err != nil {
		reason := registry.Reason(err, "")
		if reason == "" {
			return nil, ctrl.Result{}, err
		}
		// The Secret watch reconciles again once the Secret is fixed.
		return condition(metav1.ConditionFalse, reason, err.Error()), ctrl.Result{}, nil
	}

	pingCtx, cancel := context.WithTimeout(ctx, registryPingTimeout)
	defer cancel()
	if err := conn.Client.Ping(pingCtx, conn.Host); err != nil {
		reason := registry.PingReason(err)
		logf.FromContext(ctx).Info("Registry connectivity check failed", "url", reg.Spec.URL, "reason", reason, "error", err.Error())
		ready := condition(metav1.ConditionFalse, reason, err.Error())
		if reason == deliveryv1alpha1.ReasonUnreachable && !untrustedCertificate(err) {
			// Retried with backoff: the registry may be down only briefly.
			return ready, ctrl.Result{}, err
		}
		// Rejected credentials and untrusted certificates stay as they are
		// until the Registry or its Secrets change, which the watches see.
		return ready, ctrl.Result{RequeueAfter: registryRecheckInterval}, nil
	}

	msg := conn.Host + " answered; Kokumi connects without credentials"
	if conn.Authenticated() {
		msg = "Connected to " + conn.Host + " with the credentials of the Registry"
	}
	return condition(metav1.ConditionTrue, deliveryv1alpha1.ReasonRegistryConnected, msg), ctrl.Result{RequeueAfter: registryRecheckInterval}, nil
}

// registryConnector connects through the Registries an object lists, from
// the Registries and Secrets its reconciler read. *registry.Clients
// implements it; it keeps a client per Registry version.
type registryConnector interface {
	Connect(entry registry.Entry) (registry.Connection, error)
	Scope(entries []registry.Entry) registry.Scope
}

// registryScope reads the Registries refs name in namespace, with their
// Secrets, and returns the Scope they form.
func registryScope(
	ctx context.Context, reader client.Reader, registries registryConnector, namespace string, refs []corev1.LocalObjectReference,
) (registry.Scope, error) {
	entries, err := index.RegistryEntries(ctx, reader, namespace, refs)
	if err != nil {
		return registry.Scope{}, err
	}
	return registries.Scope(entries), nil
}

// connectRecorded returns the connection to url, an artifact Kokumi pushed:
// through the recorded Registry that authenticated the push while it exists
// and still covers url, and through the Registries fallback names
// otherwise, for example when the Registry was deleted or the artifact
// predates it.
func connectRecorded(
	ctx context.Context,
	reader client.Reader,
	registries registryConnector,
	namespace string,
	recorded *corev1.LocalObjectReference,
	url string,
	fallback func(context.Context) ([]corev1.LocalObjectReference, error),
) (registry.Connection, error) {
	if recorded != nil {
		scope, err := registryScope(ctx, reader, registries, namespace, []corev1.LocalObjectReference{*recorded})
		if err == nil {
			conn, err := scope.Connect(url)
			if err != nil || conn.Registry != nil {
				return conn, err
			}
		} else if registry.Reason(err, "") != deliveryv1alpha1.ReasonRegistryNotFound {
			return registry.Connection{}, err
		}
	}

	refs, err := fallback(ctx)
	if err != nil {
		return registry.Connection{}, err
	}
	scope, err := registryScope(ctx, reader, registries, namespace, refs)
	if err != nil {
		return registry.Connection{}, err
	}
	return scope.Connect(url)
}

// registriesForSecret enqueues the Registries that name the Secret.
func (r *RegistryReconciler) registriesForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	registries, err := index.RegistriesForSecret(ctx, r.Client, obj.GetNamespace(), obj.GetName())
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list Registries for Secret", "secret", obj.GetName())
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(registries))
	for _, reg := range registries {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&reg)})
	}
	return reqs
}

// ensureDefaultRunner returns a Runnable that creates the Registry of the
// bundled registry in the install namespace on startup when it is missing,
// so that it is listed and checked like any other. An existing one is left
// as it is. reader must work before the informer cache has started.
func (r *RegistryReconciler) ensureDefaultRunner(reader client.Reader) manager.RunnableFunc {
	return func(ctx context.Context) error {
		key := client.ObjectKey{Namespace: r.Namespace, Name: deliveryv1alpha1.DefaultRegistryName}
		if err := reader.Get(ctx, key, &deliveryv1alpha1.Registry{}); !apierrors.IsNotFound(err) {
			return err
		}
		reg := &deliveryv1alpha1.Registry{
			Name:      key.Name,
			Namespace: key.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "kokumi"},
			Spec: deliveryv1alpha1.RegistrySpec{
				URL:    "oci://" + r.BundledHost,
				Scheme: deliveryv1alpha1.RegistrySchemeHTTP,
			},
		}
		if err := r.Create(ctx, reg); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create the Registry of the bundled registry: %w", err)
		}
		logf.FromContext(ctx).Info("Created Registry", "name", key.Name, "namespace", key.Namespace)
		return nil
	}
}

// SetupWithManager sets up the controller with the Manager. Status updates
// of a Registry do not reconcile it again; changes of its spec and its
// Secrets do.
func (r *RegistryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.Add(r.ensureDefaultRunner(mgr.GetAPIReader())); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Registry{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.registriesForSecret),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{})).
		Named("registry").
		Complete(r)
}
