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

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	"github.com/kokumi-dev/kokumi/internal/conditions"
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

// Reconcile records in the Approval's Counted condition whether its vote
// counts. Approvals are immutable audit records managed by the admission
// policy; the reconciler only reports on them and therefore adds no
// finalizer.
func (r *ApprovalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
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

	before := a.Status.DeepCopy()
	defer func() { result, err = r.patchStatus(ctx, a, before, result, err) }()

	if !a.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	return r.reconcileApproval(ctx, a)
}

// reconcileApproval evaluates the Approval against its Preparation and its
// siblings.
func (r *ApprovalReconciler) reconcileApproval(ctx context.Context, a *deliveryv1alpha1.Approval) (ctrl.Result, error) {
	prep := &deliveryv1alpha1.Preparation{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: a.Namespace, Name: a.Spec.PreparationRef.Name}, prep); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get Preparation: %w", err)
		}
		markCounted(a, approval.Verdict{
			Reason:  deliveryv1alpha1.ReasonPreparationNotFound,
			Message: fmt.Sprintf("Preparation %q does not exist", a.Spec.PreparationRef.Name),
		})
		return ctrl.Result{}, nil
	}

	approvals, err := r.approvalsOf(ctx, prep)
	if err != nil {
		return ctrl.Result{}, err
	}

	verdict, ok := approval.Evaluate(prep, approvals).Verdicts[a.Name]
	if !ok {
		log.FromContext(ctx).V(4).Info("Approval not yet in cache, waiting for the next event")
		return ctrl.Result{}, nil
	}
	markCounted(a, verdict)
	return ctrl.Result{}, nil
}

// approvalsOf lists the Approvals of prep.
func (r *ApprovalReconciler) approvalsOf(ctx context.Context, prep *deliveryv1alpha1.Preparation) ([]deliveryv1alpha1.Approval, error) {
	approvals := &deliveryv1alpha1.ApprovalList{}
	if err := r.List(ctx, approvals,
		client.InNamespace(prep.Namespace),
		client.MatchingFields{deliveryv1alpha1.FieldPreparationRefName: prep.Name},
	); err != nil {
		return nil, fmt.Errorf("failed to list Approvals: %w", err)
	}
	return approvals.Items, nil
}

// markCounted records verdict in the Counted condition of a.
func markCounted(a *deliveryv1alpha1.Approval, verdict approval.Verdict) {
	condStatus := metav1.ConditionFalse
	if verdict.Counted {
		condStatus = metav1.ConditionTrue
	}
	conditions.Set(a, deliveryv1alpha1.ConditionTypeCounted, condStatus, verdict.Reason, verdict.Message)
}

// patchStatus writes the status a took on in this reconcile, unless it is
// unchanged, and returns how to requeue: as the reconcile decided, or soon
// when the patch conflicts. The patch is locked to the version this reconcile
// read or last wrote, so that a reconcile from a stale cache never overwrites
// a counted verdict.
func (r *ApprovalReconciler) patchStatus(
	ctx context.Context, a *deliveryv1alpha1.Approval, before *deliveryv1alpha1.ApprovalStatus, result ctrl.Result, err error,
) (ctrl.Result, error) {
	a.Status.ObservedGeneration = a.Generation
	if equality.Semantic.DeepEqual(*before, a.Status) {
		return result, err
	}

	base := a.DeepCopy()
	base.Status = *before
	perr := r.Status().Patch(ctx, a, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	switch {
	case perr == nil, apierrors.IsNotFound(perr):
		return result, err
	case apierrors.IsConflict(perr) && err == nil:
		log.FromContext(ctx).V(1).Info("Approval changed during the reconcile, reconciling it again")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	default:
		return result, errors.Join(err, fmt.Errorf("failed to patch Approval status: %w", perr))
	}
}

// approvalPreparation returns the Preparation an Approval votes on, for the
// index on deliveryv1alpha1.FieldPreparationRefName.
func approvalPreparation(obj client.Object) []string {
	a, ok := obj.(*deliveryv1alpha1.Approval)
	if !ok {
		return nil
	}
	return []string{a.Spec.PreparationRef.Name}
}

// enqueueApprovalsForPreparation re-evaluates all votes of a Preparation
// whenever its aggregated approval state changes (e.g. a newer vote
// supersedes an older one, or the votes are sealed).
func (r *ApprovalReconciler) enqueueApprovalsForPreparation(ctx context.Context, obj client.Object) []ctrl.Request {
	prep, ok := obj.(*deliveryv1alpha1.Preparation)
	if !ok {
		return nil
	}
	approvals, err := r.approvalsOf(ctx, prep)
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

// approvalStateChanged passes the Preparation updates that change its
// aggregated approval state.
var approvalStateChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldPrep, okOld := e.ObjectOld.(*deliveryv1alpha1.Preparation)
		newPrep, okNew := e.ObjectNew.(*deliveryv1alpha1.Preparation)
		return okOld && okNew && !equality.Semantic.DeepEqual(oldPrep.Status.Approval, newPrep.Status.Approval)
	},
}

// SetupWithManager sets up the controller with the Manager. It indexes
// Approvals by the Preparation they vote on, which the Preparation and
// Serving controllers list them by as well.
func (r *ApprovalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &deliveryv1alpha1.Approval{},
		deliveryv1alpha1.FieldPreparationRefName, approvalPreparation); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Approval{}).
		Watches(&deliveryv1alpha1.Preparation{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueApprovalsForPreparation),
			builder.WithPredicates(approvalStateChanged)).
		Named("approval").
		Complete(r)
}
