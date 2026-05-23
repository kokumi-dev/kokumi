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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// PantryReconciler reconciles a Pantry object
type PantryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=pantries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=pantries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=pantries/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile validates connectivity to the Pantry's backing registry.
func (r *PantryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Pantry", "namespace", req.Namespace, "name", req.Name)

	pantry := &deliveryv1alpha1.Pantry{}
	if err := r.Get(ctx, req.NamespacedName, pantry); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Pantry resource not found, ignoring")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Pantry")

		return ctrl.Result{}, fmt.Errorf("failed to get Pantry: %w", err)
	}

	if !pantry.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, pantry)
	}

	if !controllerutil.ContainsFinalizer(pantry, deliveryv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(pantry, deliveryv1alpha1.Finalizer)
		if err := r.Update(ctx, pantry); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcilePantry(ctx, pantry)
}

// reconcilePantry validates connectivity to the Pantry's backing registry and
// updates the Pantry's status conditions accordingly.
func (r *PantryReconciler) reconcilePantry(ctx context.Context, pantry *deliveryv1alpha1.Pantry) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx)
	updater := status.NewPantryUpdater(r.Client)

	host := oci.ExtractHost(pantry.Spec.URL)

	var credData []byte
	if pantry.Spec.SecretRef != nil {
		var secret corev1.Secret
		secretKey := types.NamespacedName{
			Namespace: pantry.Namespace,
			Name:      pantry.Spec.SecretRef.Name,
		}
		if err := r.Get(ctx, secretKey, &secret); err != nil {
			if statusErr := updater.Failed(ctx, pantry, err); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		credData = secret.Data[".dockerconfigjson"]
		if len(credData) == 0 {
			logger.Info("Secret does not contain .dockerconfigjson key",
				"secret", secretKey.Name)
		}
	}

	// Ping registry — with credentials when available, anonymous otherwise.
	pingCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var pingErr error
	if len(credData) > 0 {
		cs, err := oci.CredentialsFromDockerConfigJSON(credData)
		if err != nil {
			if statusErr := updater.Failed(ctx, pantry, err); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}
		pingErr = oci.PingRegistry(pingCtx, host, cs)
	} else {
		pingErr = oci.PingRegistry(pingCtx, host, nil)
	}

	if pingErr != nil {
		logger.Info("Registry connectivity check failed", "host", host, "error", pingErr)
		if statusErr := updater.Failed(ctx, pantry, pingErr); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, nil
	}

	logger.Info("Registry connectivity check passed", "host", host)

	if err := updater.Ready(ctx, pantry, "Registry is reachable"); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileDelete removes the finalizer from the Pantry, allowing garbage collection.
func (r *PantryReconciler) reconcileDelete(ctx context.Context, pantry *deliveryv1alpha1.Pantry) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Pantry")

	if controllerutil.ContainsFinalizer(pantry, deliveryv1alpha1.Finalizer) {
		logger.Info("Cleaning up Pantry resources")

		controllerutil.RemoveFinalizer(pantry, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, pantry); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PantryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mapSecretToPantry := handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			secret, ok := obj.(*corev1.Secret)
			if !ok {
				return nil
			}

			var pantryList deliveryv1alpha1.PantryList
			if err := mgr.GetClient().List(ctx, &pantryList,
				client.InNamespace(secret.Namespace)); err != nil {
				return nil
			}

			var reqs []reconcile.Request
			for _, p := range pantryList.Items {
				if p.Spec.SecretRef != nil && p.Spec.SecretRef.Name == secret.Name {
					reqs = append(reqs, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: p.Namespace,
							Name:      p.Name,
						},
					})
				}
			}
			return reqs
		},
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Pantry{}).
		Watches(
			&corev1.Secret{},
			mapSecretToPantry,
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Named("pantry").
		Complete(r)
}
