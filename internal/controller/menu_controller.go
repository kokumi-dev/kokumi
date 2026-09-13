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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/service"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// MenuReconciler reconciles a Menu object.
type MenuReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Service        *service.MenuService
	PantryResolver credential.PantryResolver
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=menus,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=menus/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=menus/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *MenuReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Menu", "namespace", req.Namespace, "name", req.Name)

	menu := &deliveryv1alpha1.Menu{}

	if err := r.Get(ctx, req.NamespacedName, menu); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Menu resource not found, ignoring")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Menu")

		return ctrl.Result{}, fmt.Errorf("failed to get Menu: %w", err)
	}

	if !menu.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, menu)
	}

	if !controllerutil.ContainsFinalizer(menu, deliveryv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(menu, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, menu); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcileMenu(ctx, menu)
}

// reconcileMenu delegates FS/OCI work to the service and then handles CRD concerns:
// updating status.
func (r *MenuReconciler) reconcileMenu(ctx context.Context, menu *deliveryv1alpha1.Menu) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	statusUpdater := status.NewMenuUpdater(r.Client)

	source, err := r.Service.ResolveSource(ctx, menu, r.PantryResolver)
	if err != nil {
		logger.Error(err, "Failed to resolve Menu source")
		if uerr := statusUpdater.Failed(ctx, menu, err); uerr != nil {
			logger.Error(uerr, "Failed to update Menu status")
		}
		return ctrl.Result{}, err
	}

	if err := statusUpdater.Ready(ctx, menu, source); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Menu source published", "name", menu.Name, "source", source.OCI)
	return ctrl.Result{}, nil
}

// reconcileDelete removes the finalizer from the Menu, allowing garbage collection.
func (r *MenuReconciler) reconcileDelete(ctx context.Context, menu *deliveryv1alpha1.Menu) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Menu")

	if controllerutil.ContainsFinalizer(menu, deliveryv1alpha1.Finalizer) {
		logger.Info("Cleaning up Menu resources")

		controllerutil.RemoveFinalizer(menu, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, menu); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MenuReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Menu{}).
		Named("menu").
		Complete(r)
}
