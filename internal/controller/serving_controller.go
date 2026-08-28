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

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/deployer"
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

	preparationName, result, err := r.resolvePreparationName(ctx, serving, statusUpdater)
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

// resolvePreparationName determines the Preparation the Serving should
// deploy. Under the Manual policy this is simply spec.preparationName. Under
// the Automatic policy it is the newest Ready Preparation of the Serving's
// Order.
func (r *ServingReconciler) resolvePreparationName(ctx context.Context, serving *deliveryv1alpha1.Serving, statusUpdater *status.ServingUpdater) (string, *ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if serving.Spec.PreparationPolicy.Type != deliveryv1alpha1.PreparationPolicyAutomatic {
		return serving.Spec.PreparationName, nil, nil
	}

	logger.Info("Automatic preparation policy, finding latest preparation", "order", serving.Spec.OrderName)

	preparationList := &deliveryv1alpha1.PreparationList{}
	if err := r.List(ctx, preparationList,
		client.InNamespace(serving.Namespace),
		client.MatchingLabels{deliveryv1alpha1.LabelOrder: serving.Spec.OrderName},
	); err != nil {
		logger.Error(err, "Failed to list Preparations")
		if uerr := statusUpdater.Failed(ctx, serving, fmt.Errorf("failed to list preparations: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return "", &ctrl.Result{}, err
	}

	if len(preparationList.Items) == 0 {
		logger.Info("No preparations found for order", "order", serving.Spec.OrderName)
		if uerr := statusUpdater.Pending(ctx, serving, "Waiting for preparations"); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		result := ctrl.Result{RequeueAfter: 30 * time.Second}
		return "", &result, nil
	}

	var latestPreparation *deliveryv1alpha1.Preparation
	for i := range preparationList.Items {
		prep := &preparationList.Items[i]
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

	if serving.Spec.PreparationName != preparationName {
		serving.Spec.PreparationName = preparationName
		if err := r.Update(ctx, serving); err != nil {
			return "", &ctrl.Result{}, err
		}
		result := ctrl.Result{RequeueAfter: 0}
		return "", &result, nil
	}

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

// enqueueServingForPreparation triggers reconciliation for Servings that
// reference or are associated with the Preparation.
func (r *ServingReconciler) enqueueServingForPreparation() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		logger := log.FromContext(ctx)
		preparation := obj.(*deliveryv1alpha1.Preparation)

		servings := &deliveryv1alpha1.ServingList{}
		if err := r.List(ctx, servings, client.InNamespace(preparation.Namespace)); err != nil {
			logger.Error(err, "Failed to list Servings")
			return []ctrl.Request{}
		}

		requests := []ctrl.Request{}
		for _, serving := range servings.Items {
			if serving.Spec.PreparationName == preparation.Name {
				requests = append(requests, ctrl.Request{
					Namespace: serving.Namespace,
					Name:      serving.Name,
				})
			} else if serving.Spec.PreparationPolicy.Type == deliveryv1alpha1.PreparationPolicyAutomatic {
				if preparation.Labels[deliveryv1alpha1.LabelOrder] == serving.Spec.OrderName {
					requests = append(requests, ctrl.Request{
						Namespace: serving.Namespace,
						Name:      serving.Name,
					})
				}
			}
		}

		logger.Info("Enqueuing Servings for preparation", "preparation", preparation.Name, "count", len(requests))
		return requests
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Serving{}).
		Watches(&deliveryv1alpha1.Preparation{}, r.enqueueServingForPreparation()).
		Watches(r.Deployer.WatchObject(),
			handler.EnqueueRequestsFromMapFunc(r.Deployer.EnqueueRequests),
			builder.WithPredicates(r.Deployer.WatchPredicate())).
		Named("serving").
		Complete(r)
}
