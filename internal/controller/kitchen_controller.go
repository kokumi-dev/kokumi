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
	"os"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/namespace"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// KitchenReconciler reconciles a Kitchen object
type KitchenReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// singletonName is the only Kitchen name the operator manages.
const singletonName = "default"

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/reconcile
func (r *KitchenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Singleton: only the Kitchen named "default" in the install namespace is
	// managed. Others are ignored (no webhook enforcement yet).
	if req.Name != singletonName || req.Namespace != namespace.Current(os.Getenv) {
		return ctrl.Result{}, nil
	}

	kitchen := &deliveryv1alpha1.Kitchen{}
	if err := r.Get(ctx, req.NamespacedName, kitchen); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		// Ensure the singleton exists so the UI always has a resource to read/write.
		kitchen = &deliveryv1alpha1.Kitchen{
			ObjectMeta: metav1.ObjectMeta{
				Name:      singletonName,
				Namespace: namespace.Current(os.Getenv),
			},
		}
		if err := r.Create(ctx, kitchen); err != nil && !apierrors.IsAlreadyExists(err) {
			log.Error(err, "Failed to create default Kitchen")
			return ctrl.Result{}, err
		}
	}

	updater := status.NewKitchenUpdater(r.Client)

	// When the admin user is enabled, the referenced credentials Secret must
	// exist and carry the required keys. Report a non-Ready condition instead
	// of silently disabling auth, so the misconfiguration is visible.
	if kitchen.Spec.AdminUser != nil &&
		kitchen.Spec.AdminUser.Enabled != nil &&
		*kitchen.Spec.AdminUser.Enabled {
		secretName := deliveryv1alpha1.DefaultAdminSecretName
		if kitchen.Spec.AdminUser.SecretRef != nil && kitchen.Spec.AdminUser.SecretRef.Name != "" {
			secretName = kitchen.Spec.AdminUser.SecretRef.Name
		}
		secret := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Namespace: kitchen.Namespace, Name: secretName}, secret)
		if err != nil || secret.Data["password-hash"] == nil || secret.Data["signing-key"] == nil {
			if uerr := updater.Failed(ctx, kitchen, fmt.Errorf("admin credentials Secret %q missing or incomplete", secretName)); uerr != nil {
				return ctrl.Result{}, uerr
			}
			return ctrl.Result{}, nil
		}
	}

	if err := updater.Ready(ctx, kitchen, "Kitchen is valid and available"); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
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
			Namespace: namespace.Current(os.Getenv),
			Name:      singletonName,
		}, kitchen)
		if err == nil {
			return nil
		}
		if !apierrors.IsNotFound(err) {
			return err
		}
		kitchen = &deliveryv1alpha1.Kitchen{
			ObjectMeta: metav1.ObjectMeta{
				Name:      singletonName,
				Namespace: namespace.Current(os.Getenv),
			},
		}
		if err := r.Create(ctx, kitchen); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		return nil
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
			builder.WithPredicates(secretInInstallNamespace(os.Getenv)),
		).
		Named("kitchen").
		Complete(r)
}

// secretInInstallNamespace filters Secret events to the install namespace.
func secretInInstallNamespace(getenv func(string) string) predicate.Predicate {
	ns := namespace.Current(getenv)
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == ns
	})
}

// mapSecretToKitchen maps any Secret in the install namespace to the singleton
// Kitchen so its readiness is re-evaluated when credentials change.
func (r *KitchenReconciler) mapSecretToKitchen(_ context.Context, obj client.Object) []ctrl.Request {
	return []ctrl.Request{
		{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: singletonName}},
	}
}
