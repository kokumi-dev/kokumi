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
)

// RecipeReconciler reconciles a Recipe object.
type RecipeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=recipes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=recipes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=recipes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *RecipeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Recipe", "namespace", req.Namespace, "name", req.Name)

	recipe := &deliveryv1alpha1.Recipe{}

	if err := r.Get(ctx, req.NamespacedName, recipe); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Recipe resource not found, ignoring")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Recipe")

		return ctrl.Result{}, fmt.Errorf("failed to get Recipe: %w", err)
	}

	if !recipe.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, recipe)
	}

	if !controllerutil.ContainsFinalizer(recipe, deliveryv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(recipe, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, recipe); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcileRecipe(ctx, recipe)
}

// reconcileRecipe materializes the Recipe. Behavior is reserved for future work.
func (r *RecipeReconciler) reconcileRecipe(ctx context.Context, recipe *deliveryv1alpha1.Recipe) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	_ = recipe

	return ctrl.Result{}, nil
}

// reconcileDelete removes the finalizer from the Recipe, allowing garbage collection.
func (r *RecipeReconciler) reconcileDelete(ctx context.Context, recipe *deliveryv1alpha1.Recipe) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Recipe")

	if controllerutil.ContainsFinalizer(recipe, deliveryv1alpha1.Finalizer) {
		logger.Info("Cleaning up Recipe resources")

		controllerutil.RemoveFinalizer(recipe, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, recipe); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RecipeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Recipe{}).
		Named("recipe").
		Complete(r)
}
