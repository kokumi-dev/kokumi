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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// MenuReconciler reconciles a Menu object.
type MenuReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=menus,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=menus/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=menus/finalizers,verbs=update

// Reconcile sets the Ready status on the Menu.
// Structural validation is handled by kubebuilder CRD markers (CEL rules).
func (r *MenuReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	menu := &deliveryv1alpha1.Menu{}
	if err := r.Get(ctx, req.NamespacedName, menu); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("Reconciling Menu", "name", menu.Name)

	updater := status.NewMenuUpdater(r.Client)

	if err := updater.Ready(ctx, menu, "Menu is valid and available"); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Menu is ready", "name", menu.Name)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MenuReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Menu{}).
		Named("menu").
		Complete(r)
}
