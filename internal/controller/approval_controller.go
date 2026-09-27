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

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/kokumi-dev/kokumi/internal/index"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// ApprovalReconciler reports on each Approval whether its vote counts
// towards the approval gate of the referenced Preparation.
type ApprovalReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Approvals are created by the kokumi server only; the controller never writes their spec.
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=approvals,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=approvals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// Approvals are immutable audit records managed by the admission policy; the
// reconciler only reports on them and therefore adds no finalizer.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *ApprovalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Approval", "namespace", req.Namespace, "name", req.Name)

	a := &deliveryv1alpha1.Approval{}
	if err := r.Get(ctx, req.NamespacedName, a); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Approval resource not found, ignoring")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Approval")

		return ctrl.Result{}, fmt.Errorf("failed to get Approval: %w", err)
	}

	return r.reconcileApproval(ctx, a)
}

// reconcileApproval evaluates the Approval against its Preparation and siblings
// and records the verdict in the Counted condition.
func (r *ApprovalReconciler) reconcileApproval(ctx context.Context, a *deliveryv1alpha1.Approval) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	prep := &deliveryv1alpha1.Preparation{}
	err := r.Get(ctx, client.ObjectKey{Namespace: a.Namespace, Name: a.Spec.PreparationRef.Name}, prep)
	if apierrors.IsNotFound(err) {
		return ctrl.Result{}, status.NewApprovalUpdater(r.Client).Counted(ctx, a, approval.Verdict{
			Reason:  deliveryv1alpha1.ReasonPreparationNotFound,
			Message: fmt.Sprintf("Preparation %q does not exist", a.Spec.PreparationRef.Name),
		})
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Preparation: %w", err)
	}

	approvals, err := index.ApprovalsForPreparation(ctx, r.Client, prep)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list Approvals: %w", err)
	}

	verdict, ok := approval.Evaluate(prep, approvals).Verdicts[a.Name]
	if !ok {
		logger.Info("Approval not yet in cache, waiting for the next event")
		return ctrl.Result{}, nil
	}
	if err := status.NewApprovalUpdater(r.Client).Counted(ctx, a, verdict); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update Approval status: %w", err)
	}
	return ctrl.Result{}, nil
}

// enqueueApprovalsForPreparation re-evaluates all votes of a Preparation
// whenever its aggregated approval state changes (e.g. a newer vote
// supersedes an older one, or the votes are sealed).
func (r *ApprovalReconciler) enqueueApprovalsForPreparation(ctx context.Context, obj client.Object) []ctrl.Request {
	prep, ok := obj.(*deliveryv1alpha1.Preparation)
	if !ok {
		return nil
	}
	approvals, err := index.ApprovalsForPreparation(ctx, r.Client, prep)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Approvals", "preparation", prep.Name)
		return nil
	}
	requests := make([]ctrl.Request, 0, len(approvals))
	for i := range approvals {
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&approvals[i])})
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApprovalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Approval{}).
		Watches(&deliveryv1alpha1.Preparation{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueApprovalsForPreparation),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldPrep, okOld := e.ObjectOld.(*deliveryv1alpha1.Preparation)
					newPrep, okNew := e.ObjectNew.(*deliveryv1alpha1.Preparation)
					return okOld && okNew && !apiequality.Semantic.DeepEqual(oldPrep.Status.Approval, newPrep.Status.Approval)
				},
			})).
		Named("approval").
		Complete(r)
}
