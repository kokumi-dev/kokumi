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
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/status"
)

const (
	argoNamespace = "argocd"
)

// ServingReconciler reconciles a Serving object
type ServingReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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

// reconcileServing handles the serving by creating/updating an Argo CD Application
func (r *ServingReconciler) reconcileServing(ctx context.Context, serving *deliveryv1alpha1.Serving) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	statusUpdater := status.NewServingUpdater(r.Client)

	preparationName := serving.Spec.Preparation
	if serving.Spec.PreparationPolicy.Type == deliveryv1alpha1.PreparationPolicyAutomatic {
		logger.Info("Automatic preparation policy, finding latest preparation", "order", serving.Spec.Order)

		preparationList := &deliveryv1alpha1.PreparationList{}
		if err := r.List(ctx, preparationList,
			client.InNamespace(serving.Namespace),
			client.MatchingLabels{deliveryv1alpha1.LabelOrder: serving.Spec.Order},
		); err != nil {
			logger.Error(err, "Failed to list Preparations")
			_ = statusUpdater.Failed(ctx, serving, fmt.Errorf("failed to list preparations: %w", err))
			return ctrl.Result{}, err
		}

		if len(preparationList.Items) == 0 {
			logger.Info("No preparations found for order", "order", serving.Spec.Order)
			_ = statusUpdater.Pending(ctx, serving, "Waiting for preparations")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		var latestPreparation *deliveryv1alpha1.Preparation
		for i := range preparationList.Items {
			prep := &preparationList.Items[i]
			if prep.Status.Phase != deliveryv1alpha1.PreparationPhaseReady {
				continue
			}
			if latestPreparation == nil || prep.CreationTimestamp.After(latestPreparation.CreationTimestamp.Time) {
				latestPreparation = prep
			}
		}

		if latestPreparation == nil {
			logger.Info("No ready preparations found for order", "order", serving.Spec.Order)
			_ = statusUpdater.Pending(ctx, serving, "Waiting for ready preparation")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		preparationName = latestPreparation.Name
		logger.Info("Selected latest preparation", "preparation", preparationName)

		if serving.Spec.Preparation != preparationName {
			serving.Spec.Preparation = preparationName
			if err := r.Update(ctx, serving); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}

	preparation := &deliveryv1alpha1.Preparation{}
	preparationKey := client.ObjectKey{Namespace: serving.Namespace, Name: preparationName}
	if err := r.Get(ctx, preparationKey, preparation); err != nil {
		logger.Error(err, "Failed to get Preparation", "preparation", preparationName)
		_ = statusUpdater.Failed(ctx, serving, fmt.Errorf("preparation not found: %w", err))
		return ctrl.Result{}, err
	}

	logger.Info("Found Preparation", "preparation", preparation.Name, "digest", preparation.Spec.Artifact.Digest)

	if serving.Status.ObservedPreparation == preparationName &&
		serving.Status.DeployedDigest == preparation.Spec.Artifact.Digest &&
		serving.Status.Phase == deliveryv1alpha1.ServingPhaseDeployed {
		logger.Info("Deployment is up-to-date", "preparation", preparationName)
		return ctrl.Result{}, nil
	}

	if err := statusUpdater.Deploying(ctx, serving); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileArgoApplication(ctx, serving, preparation); err != nil {
		logger.Error(err, "Failed to reconcile Argo CD Application")
		_ = statusUpdater.Failed(ctx, serving, fmt.Errorf("failed to create Argo CD Application: %w", err))
		return ctrl.Result{}, err
	}

	logger.Info("Successfully created/updated Argo CD Application", "preparation", preparationName)

	serving.Status.ObservedPreparation = preparationName
	serving.Status.DeployedDigest = preparation.Spec.Artifact.Digest
	if err := statusUpdater.Deployed(ctx, serving, "Successfully deployed component"); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileArgoApplication creates or updates an Argo CD Application resource
func (r *ServingReconciler) reconcileArgoApplication(ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation) error {
	logger := log.FromContext(ctx)

	ociRef := strings.TrimPrefix(preparation.Spec.Artifact.OCIRef, "oci://")
	parts := strings.Split(ociRef, "@")
	if len(parts) != 2 {
		return fmt.Errorf("invalid OCI reference format: %s", ociRef)
	}
	repoURL := "oci://" + parts[0]
	targetRevision := preparation.Spec.Artifact.Digest

	appName := serving.Name

	app := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]any{
				"name":      appName,
				"namespace": argoNamespace,
				"labels": map[string]any{
					deliveryv1alpha1.LabelOrder:   serving.Spec.Order,
					deliveryv1alpha1.LabelServing: serving.Name,
				},
			},
			"spec": map[string]any{
				"project": "default",
				"source": map[string]any{
					"repoURL":        repoURL,
					"targetRevision": targetRevision,
					"path":           ".",
				},
				"destination": map[string]any{
					"server":    "https://kubernetes.default.svc",
					"namespace": serving.Namespace,
				},
			},
		},
	}

	app.Object["spec"].(map[string]any)["syncPolicy"] = map[string]any{
		"automated": map[string]any{
			"prune":    true,
			"selfHeal": true,
		},
		"syncOptions": []any{
			"ServerSideApply=true",
		},
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(app.GroupVersionKind())
	err := r.Get(ctx, client.ObjectKey{Namespace: argoNamespace, Name: appName}, existing)

	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Creating Argo CD Application", "name", appName, "namespace", argoNamespace, "revision", targetRevision)
			if err := r.Create(ctx, app); err != nil {
				return fmt.Errorf("failed to create Application: %w", err)
			}
			logger.Info("Created Argo CD Application", "name", appName)
		} else {
			return fmt.Errorf("failed to get existing Application: %w", err)
		}
	} else {
		app.SetResourceVersion(existing.GetResourceVersion())
		logger.Info("Updating Argo CD Application", "name", appName, "namespace", argoNamespace, "revision", targetRevision)
		if err := r.Update(ctx, app); err != nil {
			return fmt.Errorf("failed to update Application: %w", err)
		}
		logger.Info("Updated Argo CD Application", "name", appName)
	}

	return nil
}

// reconcileDelete handles the deletion of a Serving
func (r *ServingReconciler) reconcileDelete(ctx context.Context, serving *deliveryv1alpha1.Serving) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Serving")

	if controllerutil.ContainsFinalizer(serving, deliveryv1alpha1.Finalizer) {
		logger.Info("Cleaning up Argo CD Application")

		argoNamespace := "argocd"

		app := &unstructured.Unstructured{}
		app.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "argoproj.io",
			Version: "v1alpha1",
			Kind:    "Application",
		})
		app.SetNamespace(argoNamespace)
		app.SetName(serving.Name)

		if err := r.Delete(ctx, app); err != nil {
			if !apierrors.IsNotFound(err) {
				logger.Error(err, "Failed to delete Argo CD Application")
				return ctrl.Result{}, err
			}
			logger.Info("Argo CD Application already deleted")
		} else {
			logger.Info("Deleted Argo CD Application", "name", serving.Name)
		}

		controllerutil.RemoveFinalizer(serving, deliveryv1alpha1.Finalizer)
		if err := r.Update(ctx, serving); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// enqueueServingForPreparation triggers reconciliation for Servings that reference a serving
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
			if serving.Spec.Preparation == preparation.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Namespace: serving.Namespace,
						Name:      serving.Name,
					},
				})
			} else if serving.Spec.PreparationPolicy.Type == deliveryv1alpha1.PreparationPolicyAutomatic {
				if preparation.Labels[deliveryv1alpha1.LabelOrder] == serving.Spec.Order {
					requests = append(requests, ctrl.Request{
						NamespacedName: client.ObjectKey{
							Namespace: serving.Namespace,
							Name:      serving.Name,
						},
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
		Named("serving").
		Complete(r)
}
