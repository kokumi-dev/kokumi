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
	"cmp"
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/index"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// PreparationReconciler reconciles a Preparation object. It aggregates the
// Preparation's Approvals and seals them into an OCI attestation once a
// Serving targets the approved Preparation.
type PreparationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader bypasses the cache so sealing never misses in-flight votes.
	APIReader client.Reader
	// OCIClient is used when the Order's destination has no credentials.
	OCIClient      oci.Client
	PantryResolver credential.PantryResolver
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=approvals,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch

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

	if !apimeta.IsStatusConditionTrue(preparation.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) {
		logger.Info("Preparation not ready, skipping")

		// Temporary no-op to mark the Preparation as ready.
		// This should be replaced with proper validation and readiness
		// conditions once the remaining readiness criteria are defined.
		statusUpdater := status.NewPreparationUpdater(r.Client)
		if uerr := statusUpdater.Ready(ctx, preparation, "Preparation is ready for serving"); uerr != nil {
			logger.Error(uerr, "Failed to update Preparation status")
		}
		return ctrl.Result{}, nil
	}

	approvals, err := index.ApprovalsForPreparation(ctx, r.Client, preparation)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list Approvals: %w", err)
	}

	res := approval.Evaluate(preparation, approvals)
	if err := status.NewPreparationUpdater(r.Client).Approval(ctx, preparation, res); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update Preparation approval status: %w", err)
	}
	if !res.Required {
		return ctrl.Result{}, nil
	}

	if approval.IsSealed(preparation) {
		if preparation.Status.Approval.Attestation == nil {
			return ctrl.Result{}, r.archive(ctx, preparation)
		}
		return ctrl.Result{}, nil
	}

	if !res.Approved {
		return ctrl.Result{}, nil
	}
	targeted, err := r.isPromotionTarget(ctx, preparation)
	if err != nil || !targeted {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, r.seal(ctx, preparation)
}

// isPromotionTarget reports whether the Order's Serving converges to prep.
func (r *PreparationReconciler) isPromotionTarget(ctx context.Context, prep *deliveryv1alpha1.Preparation) (bool, error) {
	serving := &deliveryv1alpha1.Serving{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: prep.Namespace, Name: prep.Spec.OrderName}, serving); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return serving.Status.TargetPreparationName == prep.Name, nil
}

// seal locks the votes of prep using a fresh read from the API server, then
// archives them. Votes submitted later are ignored.
func (r *PreparationReconciler) seal(ctx context.Context, prep *deliveryv1alpha1.Preparation) error {
	updater := status.NewPreparationUpdater(r.Client)

	approvals, err := index.ApprovalsForPreparation(ctx, r.APIReader, prep)
	if err != nil {
		return fmt.Errorf("failed to list Approvals: %w", err)
	}
	res := approval.Evaluate(prep, approvals)
	if !res.Approved {
		return updater.Approval(ctx, prep, res)
	}

	if err := updater.Seal(ctx, prep, res, approval.NewSealTime(time.Now())); err != nil {
		return fmt.Errorf("failed to seal Approvals: %w", err)
	}
	log.FromContext(ctx).Info("Sealed Approvals", "preparation", prep.Name, "approvedCount", res.Status.ApprovedCount)

	return r.archive(ctx, prep)
}

// archive records the sealed votes as an OCI attestation of the Preparation.
func (r *PreparationReconciler) archive(ctx context.Context, prep *deliveryv1alpha1.Preparation) error {
	updater := status.NewPreparationUpdater(r.Client)

	approvals, err := index.ApprovalsForPreparation(ctx, r.APIReader, prep)
	if err != nil {
		return fmt.Errorf("failed to list Approvals: %w", err)
	}

	// The attestation is pushed with the credentials of the Order's
	// destination. A deleted Order falls back to the anonymous client.
	order := &deliveryv1alpha1.Order{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: prep.Namespace, Name: prep.Spec.OrderName}, order); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get Order %q: %w", prep.Spec.OrderName, err)
	}
	_, destClient, err := r.PantryResolver.ResolveDestination(ctx, order.Spec.Destination, "", prep.Namespace)
	if err != nil {
		return fmt.Errorf("failed to resolve destination credentials: %w", err)
	}

	attestation, err := approval.Archive(ctx, cmp.Or(destClient, r.OCIClient), prep, approvals)
	if err != nil {
		if uerr := updater.ArchiveFailed(ctx, prep, err); uerr != nil {
			log.FromContext(ctx).Error(uerr, "Failed to update Preparation status")
		}
		return fmt.Errorf("failed to archive Approvals: %w", err)
	}
	if err := updater.Archived(ctx, prep, attestation); err != nil {
		return fmt.Errorf("failed to record approval attestation: %w", err)
	}
	log.FromContext(ctx).Info("Recorded approval attestation", "preparation", prep.Name, "attestation", attestation.OCIRef)
	return nil
}

func enqueuePreparationForApproval(_ context.Context, obj client.Object) []ctrl.Request {
	a, ok := obj.(*deliveryv1alpha1.Approval)
	if !ok {
		return nil
	}
	return []ctrl.Request{{Namespace: a.Namespace, Name: a.Spec.PreparationRef.Name}}
}

func enqueueTargetPreparation(_ context.Context, obj client.Object) []ctrl.Request {
	serving, ok := obj.(*deliveryv1alpha1.Serving)
	if !ok || serving.Status.TargetPreparationName == "" {
		return nil
	}
	return []ctrl.Request{{Namespace: serving.Namespace, Name: serving.Status.TargetPreparationName}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *PreparationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Preparation{}).
		Watches(&deliveryv1alpha1.Approval{}, handler.EnqueueRequestsFromMapFunc(enqueuePreparationForApproval)).
		Watches(&deliveryv1alpha1.Serving{},
			handler.EnqueueRequestsFromMapFunc(enqueueTargetPreparation),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldServing, okOld := e.ObjectOld.(*deliveryv1alpha1.Serving)
					newServing, okNew := e.ObjectNew.(*deliveryv1alpha1.Serving)
					return okOld && okNew && oldServing.Status.TargetPreparationName != newServing.Status.TargetPreparationName
				},
			})).
		Named("preparation").
		Complete(r)
}
