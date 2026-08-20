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
	"os"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

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
		Named("kitchen").
		Complete(r)
}
