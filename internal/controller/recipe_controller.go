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
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/renderer"
	"github.com/kokumi-dev/kokumi/internal/service"
	"github.com/kokumi-dev/kokumi/internal/status"
)

const finalizerName = "delivery.kokumi.dev/finalizer"

// RecipeReconciler reconciles a Recipe object.
type RecipeReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Service service.RecipeService
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=recipes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=recipes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=recipes/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/status,verbs=get;update;patch

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

	if !controllerutil.ContainsFinalizer(recipe, finalizerName) {
		controllerutil.AddFinalizer(recipe, finalizerName)

		if err := r.Update(ctx, recipe); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcileRender(ctx, recipe)
}

// reconcileRender delegates FS/OCI work to the service and then handles CRD concerns:
// updating status and creating the Preparation resource.
func (r *RecipeReconciler) reconcileRender(ctx context.Context, recipe *deliveryv1alpha1.Recipe) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	statusUpdater := status.NewRecipeUpdater(r.Client)

	specHash, err := renderer.CalculateSpecHash(recipe.Spec)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate spec hash: %w", err)
	}

	if recipe.Status.LatestConfigHash == specHash {
		logger.Info("Configuration is up-to-date, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	if err := statusUpdater.Processing(ctx, recipe, specHash); err != nil {
		return ctrl.Result{}, err
	}

	result, err := r.Service.ProcessRecipe(ctx, recipe)
	if err != nil {
		logger.Error(err, "Failed to process Recipe")
		_ = statusUpdater.Failed(ctx, recipe, err)

		return ctrl.Result{}, err
	}

	preparation, err := r.createPreparation(ctx, recipe, result.SourceRef, result.SourceDigest, recipe.Spec.Source.Version, result.DestRef, result.DestDigest)
	if err != nil {
		logger.Error(err, "Failed to create Preparation")
		_ = statusUpdater.Failed(ctx, recipe, fmt.Errorf("failed to create revision: %w", err))

		return ctrl.Result{}, err
	}

	logger.Info("Created Preparation", "revision", preparation.Name)

	recipe.Status.LatestRevision = preparation.Name

	if err := statusUpdater.Ready(ctx, recipe, specHash, fmt.Sprintf("Successfully pushed to %s", result.DestRef)); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileDelete removes the finalizer from the Recipe, allowing garbage collection.
func (r *RecipeReconciler) reconcileDelete(ctx context.Context, recipe *deliveryv1alpha1.Recipe) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Recipe")

	if controllerutil.ContainsFinalizer(recipe, finalizerName) {
		logger.Info("Cleaning up Recipe resources")

		controllerutil.RemoveFinalizer(recipe, finalizerName)

		if err := r.Update(ctx, recipe); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// createPreparation creates a Preparation for the rendered artifact.
// If a Preparation with the same name already exists it is returned unchanged.
func (r *RecipeReconciler) createPreparation(
	ctx context.Context,
	recipe *deliveryv1alpha1.Recipe,
	sourceRef, sourceDigest, sourceVersion, destRef, destDigest string,
) (*deliveryv1alpha1.Preparation, error) {
	logger := log.FromContext(ctx)

	shortDigest := destDigest[len("sha256:") : len("sha256:")+12]
	revisionName := fmt.Sprintf("%s-%s", recipe.Name, shortDigest)

	existing := &deliveryv1alpha1.Preparation{}

	err := r.Get(ctx, client.ObjectKey{Namespace: recipe.Namespace, Name: revisionName}, existing)
	if err == nil {
		logger.Info("Preparation already exists", "revision", revisionName)
		return existing, nil
	}

	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to check for existing revision: %w", err)
	}

	configHash, err := renderer.CalculateConfigHash(recipe.Spec.Patches)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate config hash: %w", err)
	}

	now := metav1.Time{Time: time.Now()}

	preparation := &deliveryv1alpha1.Preparation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      revisionName,
			Namespace: recipe.Namespace,
			Labels: map[string]string{
				"delivery.kokumi.dev/recipe":      recipe.Name,
				"delivery.kokumi.dev/version":     sourceVersion,
				"delivery.kokumi.dev/auto-deploy": strconv.FormatBool(recipe.Spec.AutoDeploy),
			},
		},
		Spec: deliveryv1alpha1.PreparationSpec{
			Recipe: recipe.Name,
			Source: deliveryv1alpha1.RecipeSource{
				OCI:        fmt.Sprintf("oci://%s", sourceRef),
				BaseDigest: sourceDigest,
			},
			Renderer: deliveryv1alpha1.Renderer{
				Version: "v1.0.0",
				Digest:  destDigest,
			},
			ConfigHash: configHash,
			Artifact: deliveryv1alpha1.Artifact{
				OCIRef: fmt.Sprintf("oci://%s@%s", destRef, destDigest),
				Digest: destDigest,
				Signed: false,
			},
		},
		Status: deliveryv1alpha1.PreparationStatus{
			Phase:     deliveryv1alpha1.PreparationPhaseReady,
			CreatedAt: &now,
		},
	}

	if err := controllerutil.SetControllerReference(recipe, preparation, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set owner reference: %w", err)
	}

	if err := r.Create(ctx, preparation); err != nil {
		return nil, fmt.Errorf("failed to create Preparation: %w", err)
	}

	preparation.Status.Phase = deliveryv1alpha1.PreparationPhaseReady
	preparation.Status.CreatedAt = &metav1.Time{Time: time.Now()}

	if err := r.Status().Update(ctx, preparation); err != nil {
		logger.Error(err, "Failed to update Preparation status")
	}

	return preparation, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RecipeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Recipe{}).
		Owns(&deliveryv1alpha1.Preparation{}).
		Named("recipe").
		Complete(r)
}
