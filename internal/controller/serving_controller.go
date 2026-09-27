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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/deployer"
	"github.com/kokumi-dev/kokumi/internal/index"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// ServingReconciler reconciles a Serving object
type ServingReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Deployer deployer.Deployer
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=approvals,verbs=get;list;watch
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *ServingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Serving", "namespace", req.Namespace, "name", req.Name)

	serving := &deliveryv1alpha1.Serving{}
	if err := r.Get(ctx, req.NamespacedName, serving); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Serving resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Serving")
		return ctrl.Result{}, fmt.Errorf("failed to get Serving: %w", err)
	}

	if !serving.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, serving)
	}

	if !controllerutil.ContainsFinalizer(serving, deliveryv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(serving, deliveryv1alpha1.Finalizer)
		if err := r.Update(ctx, serving); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcileServing(ctx, serving)
}

// reconcileServing handles the serving by driving the deployment through the
// configured Deployer.
func (r *ServingReconciler) reconcileServing(ctx context.Context, serving *deliveryv1alpha1.Serving) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	statusUpdater := status.NewServingUpdater(r.Client)

	preparationName, result, err := r.resolveTarget(ctx, serving, statusUpdater)
	if result != nil || err != nil {
		return *result, err
	}

	preparation := &deliveryv1alpha1.Preparation{}
	preparationKey := client.ObjectKey{Namespace: serving.Namespace, Name: preparationName}
	if err := r.Get(ctx, preparationKey, preparation); err != nil {
		logger.Error(err, "Failed to get Preparation", "preparation", preparationName)
		if uerr := statusUpdater.Failed(ctx, serving, fmt.Errorf("preparation not found: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return ctrl.Result{}, err
	}

	blocked, err := r.gate(ctx, serving, preparation, statusUpdater)
	if blocked || err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Found Preparation", "preparation", preparation.Name, "digest", preparation.Spec.Artifact.Digest)

	desiredDigest := preparation.Spec.Artifact.Digest

	if serving.Status.ObservedPreparationName == preparationName &&
		serving.Status.DeployedDigest == desiredDigest &&
		apimeta.IsStatusConditionTrue(serving.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) {
		deploymentStatus, err := r.Deployer.Status(ctx, serving)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get deployment status: %w", err)
		}

		if deploymentStatus.Phase == deployer.PhaseHealthy && deploymentStatus.Revision == desiredDigest {
			logger.Info("Deployment is up-to-date", "preparation", preparationName)
			return ctrl.Result{}, nil
		}
		logger.Info("Deployment drifted from desired state, redeploying", "preparation", preparationName, "phase", deploymentStatus.Phase)
	}

	// Validate the opt-in on any pre-existing deployment BEFORE transitioning
	// the status to "Deploying". This avoids flapping between "Deploying" and
	// "DeploymentFailed" on every reconcile pass when the opt-in is missing.
	if err := r.Deployer.VerifyOptIn(ctx, serving); err != nil {
		if errors.Is(err, deployer.ErrOptInRequired) {
			logger.Info("Cannot update deployment, opt-in required", "error", err.Error())
			if uerr := statusUpdater.Failed(ctx, serving, err); uerr != nil {
				logger.Error(uerr, "Failed to update Serving status")
			}
			// Terminal for this generation: do not requeue. A change to the
			// deployment (opt-in) or Serving will trigger a fresh event.
			return ctrl.Result{}, nil
		}
		if uerr := statusUpdater.Failed(ctx, serving, fmt.Errorf("failed to check deployment opt-in: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return ctrl.Result{}, err
	}

	if err := statusUpdater.Deploying(ctx, serving, preparationName); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Deployer.Deploy(ctx, serving, preparation); err != nil {
		logger.Error(err, "Failed to deploy")
		if uerr := statusUpdater.Failed(ctx, serving, fmt.Errorf("failed to deploy: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return ctrl.Result{}, err
	}

	logger.Info("Successfully created/updated deployment", "preparation", preparationName)

	deploymentStatus, err := r.Deployer.Status(ctx, serving)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get deployment status: %w", err)
	}

	if deploymentStatus.Phase == deployer.PhaseDegraded {
		if uerr := statusUpdater.Failed(ctx, serving, fmt.Errorf("deployment degraded: %s", deploymentStatus.Message)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return ctrl.Result{}, nil
	}

	if deploymentStatus.Phase == deployer.PhaseHealthy && deploymentStatus.Revision == desiredDigest {
		if err := statusUpdater.Deployed(ctx, serving, preparationName, desiredDigest, "Successfully deployed component"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	logger.Info("Deployment not yet healthy at desired revision, staying in Deploying",
		"preparation", preparationName, "phase", deploymentStatus.Phase, "revision", deploymentStatus.Revision)
	return ctrl.Result{}, nil
}

// gate enforces the approval gate of the target Preparation for both
// promotion modes. It returns true when deployment must not proceed; the
// currently deployed Preparation is left untouched.
func (r *ServingReconciler) gate(ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation, statusUpdater *status.ServingUpdater) (bool, error) {
	approvals, err := index.ApprovalsForPreparation(ctx, r.Client, preparation)
	if err != nil {
		return true, fmt.Errorf("failed to list Approvals: %w", err)
	}

	gate := approval.Gate(preparation, approvals)
	if gate.Blocked {
		log.FromContext(ctx).V(4).Info("Approval gate blocks deployment", "preparation", preparation.Name, "reason", gate.Reason)
	}
	if err := statusUpdater.Gate(ctx, serving, preparation.Name, gate.Status, gate.Reason, gate.Message); err != nil {
		return true, fmt.Errorf("failed to update Serving approval status: %w", err)
	}
	return gate.Blocked, nil
}

// resolveTarget determines the Preparation the Serving converges to. The
// promotion mode is read from the Order: Manual uses spec.preparationName,
// Automatic uses the newest Ready Preparation of the Order. The Serving spec
// is never modified.
func (r *ServingReconciler) resolveTarget(ctx context.Context, serving *deliveryv1alpha1.Serving, statusUpdater *status.ServingUpdater) (string, *ctrl.Result, error) {
	logger := log.FromContext(ctx)

	order := &deliveryv1alpha1.Order{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: serving.Spec.OrderName}, order); err != nil {
		if !apierrors.IsNotFound(err) {
			return "", &ctrl.Result{}, fmt.Errorf("failed to get Order: %w", err)
		}
		if uerr := statusUpdater.Pending(ctx, serving, fmt.Sprintf("Waiting for Order %q", serving.Spec.OrderName)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return "", &ctrl.Result{}, nil
	}

	if order.EffectivePromotionMode() != deliveryv1alpha1.PromotionModeAutomatic {
		if serving.Spec.PreparationName == "" {
			if uerr := statusUpdater.Pending(ctx, serving, "Waiting for a Preparation to be promoted"); uerr != nil {
				logger.Error(uerr, "Failed to update Serving status")
			}
			return "", &ctrl.Result{}, nil
		}
		return serving.Spec.PreparationName, nil, nil
	}

	preparations, err := index.PreparationsForOrder(ctx, r.Client, serving.Namespace, serving.Spec.OrderName)
	if err != nil {
		logger.Error(err, "Failed to list Preparations")
		if uerr := statusUpdater.Failed(ctx, serving, fmt.Errorf("failed to list preparations: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return "", &ctrl.Result{}, err
	}

	if len(preparations) == 0 {
		logger.Info("No preparations found for order", "order", serving.Spec.OrderName)
		if uerr := statusUpdater.Pending(ctx, serving, "Waiting for preparations"); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		result := ctrl.Result{RequeueAfter: 30 * time.Second}
		return "", &result, nil
	}

	var latestPreparation *deliveryv1alpha1.Preparation
	for i := range preparations {
		prep := &preparations[i]
		if !apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) {
			continue
		}
		if latestPreparation == nil || prep.CreationTimestamp.After(latestPreparation.CreationTimestamp.Time) {
			latestPreparation = prep
		}
	}

	if latestPreparation == nil {
		logger.Info("No ready preparations found for order", "order", serving.Spec.OrderName)
		if uerr := statusUpdater.Pending(ctx, serving, "Waiting for ready preparation"); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		result := ctrl.Result{RequeueAfter: 30 * time.Second}
		return "", &result, nil
	}

	preparationName := latestPreparation.Name
	logger.Info("Selected latest preparation", "preparation", preparationName)

	return preparationName, nil, nil
}

// reconcileDelete handles the deletion of a Serving
func (r *ServingReconciler) reconcileDelete(ctx context.Context, serving *deliveryv1alpha1.Serving) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Serving")

	if controllerutil.ContainsFinalizer(serving, deliveryv1alpha1.Finalizer) {
		logger.Info("Cleaning up deployment")
		if err := r.Deployer.Remove(ctx, serving); err != nil {
			return ctrl.Result{}, err
		}

		controllerutil.RemoveFinalizer(serving, deliveryv1alpha1.Finalizer)
		if err := r.Update(ctx, serving); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// enqueueServingForOrderName maps an Order or Preparation to the Order's
// Serving, which always carries the Order's name.
func enqueueServingForOrderName(_ context.Context, obj client.Object) []ctrl.Request {
	var orderName string
	switch o := obj.(type) {
	case *deliveryv1alpha1.Order:
		orderName = o.Name
	case *deliveryv1alpha1.Preparation:
		orderName = o.Spec.OrderName
	default:
		return nil
	}
	return []ctrl.Request{{Namespace: obj.GetNamespace(), Name: orderName}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Serving{}).
		Watches(&deliveryv1alpha1.Preparation{}, handler.EnqueueRequestsFromMapFunc(enqueueServingForOrderName)).
		Watches(&deliveryv1alpha1.Order{},
			handler.EnqueueRequestsFromMapFunc(enqueueServingForOrderName),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(r.Deployer.WatchObject(),
			handler.EnqueueRequestsFromMapFunc(r.Deployer.EnqueueRequests),
			builder.WithPredicates(r.Deployer.WatchPredicate())).
		Named("serving").
		Complete(r)
}
