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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// PreparationReconciler reconciles a Preparation object
type PreparationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *PreparationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Preparation", "namespace", req.Namespace, "name", req.Name)

	preparation := &deliveryv1alpha1.Preparation{}
	if err := r.Get(ctx, req.NamespacedName, preparation); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Preparation resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Preparation")
		return ctrl.Result{}, fmt.Errorf("failed to get Preparation: %w", err)
	}

	if preparation.Status.Phase != deliveryv1alpha1.PreparationPhaseReady {
		logger.Info("Preparation not ready, skipping", "phase", preparation.Status.Phase)
		return ctrl.Result{}, nil
	}

	autoDeploy := preparation.Labels["delivery.kokumi.dev/auto-deploy"]
	approveLabel := preparation.Labels["delivery.kokumi.dev/approve-deploy"]

	if autoDeploy == "true" {
		logger.Info("AutoDeploy enabled, reconciling Serving")
		if err := r.reconcileServing(ctx, preparation, true); err != nil {
			logger.Error(err, "Failed to reconcile Serving")
			return ctrl.Result{}, err
		}
	} else if approveLabel == "true" {
		logger.Info("Manual serving approved, reconciling Serving")
		if err := r.reconcileServing(ctx, preparation, false); err != nil {
			logger.Error(err, "Failed to reconcile Serving")
			return ctrl.Result{}, err
		}
	} else {
		logger.Info("AutoDeploy disabled and no approval label, skipping serving")
	}

	return ctrl.Result{}, nil
}

// reconcileServing creates or updates a Serving for this Preparation
func (r *PreparationReconciler) reconcileServing(ctx context.Context, preparation *deliveryv1alpha1.Preparation, automatic bool) error {
	logger := log.FromContext(ctx)

	orderName := preparation.Spec.Order
	servingName := orderName

	serving := &deliveryv1alpha1.Serving{}
	err := r.Get(ctx, client.ObjectKey{Namespace: preparation.Namespace, Name: servingName}, serving)

	preparationPolicyType := deliveryv1alpha1.PreparationPolicyManual
	if automatic {
		preparationPolicyType = deliveryv1alpha1.PreparationPolicyAutomatic
	}

	if err != nil {
		if apierrors.IsNotFound(err) {
			serving = &deliveryv1alpha1.Serving{
				ObjectMeta: metav1.ObjectMeta{
					Name:      servingName,
					Namespace: preparation.Namespace,
					Labels: map[string]string{
						"delivery.kokumi.dev/order":       orderName,
						"delivery.kokumi.dev/auto-deploy": fmt.Sprintf("%v", automatic),
					},
				},
				Spec: deliveryv1alpha1.ServingSpec{
					Order:       orderName,
					Preparation: preparation.Name,
					PreparationPolicy: deliveryv1alpha1.PreparationPolicy{
						Type: preparationPolicyType,
					},
				},
			}

			logger.Info("Creating Serving", "name", servingName, "preparation", preparation.Name, "automatic", automatic)
			if err := r.Create(ctx, serving); err != nil {
				return fmt.Errorf("failed to create Serving: %w", err)
			}
			logger.Info("Created Serving", "name", servingName)
			return nil
		}
		return fmt.Errorf("failed to get Serving: %w", err)
	}

	needsUpdate := false

	if serving.Spec.Preparation != preparation.Name {
		logger.Info("Updating Serving preparation", "from", serving.Spec.Preparation, "to", preparation.Name)
		serving.Spec.Preparation = preparation.Name
		needsUpdate = true
	}

	if serving.Spec.PreparationPolicy.Type != preparationPolicyType {
		logger.Info("Updating Serving policy", "from", serving.Spec.PreparationPolicy.Type, "to", preparationPolicyType)
		serving.Spec.PreparationPolicy.Type = preparationPolicyType
		needsUpdate = true
	}

	if needsUpdate {
		logger.Info("Updating Serving", "name", servingName)
		if err := r.Update(ctx, serving); err != nil {
			return fmt.Errorf("failed to update Serving: %w", err)
		}
		logger.Info("Updated Serving", "name", servingName)
	} else {
		logger.Info("Serving is up-to-date", "name", servingName)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PreparationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Preparation{}).
		Named("preparation").
		Complete(r)
}
