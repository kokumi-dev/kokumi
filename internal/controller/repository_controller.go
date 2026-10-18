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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/registry"
)

// repositoryRegistryField indexes Repositories by spec.registryRef.name.
const repositoryRegistryField = "spec.registryRef.name"

// RepositoryReconciler checks that the Registry a Repository is read through
// exists and covers it. It does not contact the registry: the Registry
// reports on the connection, and the catalog lists the tags when browsed.
type RepositoryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=repositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=repositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries,verbs=get;list;watch

// Reconcile records in the Repository's Ready condition whether its Registry
// exists and covers it. Repositories carry no finalizer: they only report.
func (r *RepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Repository", "namespace", req.Namespace, "name", req.Name)

	repo := &deliveryv1alpha1.Repository{}
	if err := r.Get(ctx, req.NamespacedName, repo); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Repository resource not found, ignoring")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Repository")

		return ctrl.Result{}, fmt.Errorf("failed to get Repository: %w", err)
	}

	before := repo.Status.DeepCopy()
	defer func() { result, err = r.patchStatus(ctx, repo, before, result, err) }()

	if !repo.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	return r.reconcileRepository(ctx, repo)
}

// reconcileRepository checks that the Registry the Repository names exists
// and covers its url. A Repository without one is read anonymously.
func (r *RepositoryReconciler) reconcileRepository(ctx context.Context, repo *deliveryv1alpha1.Repository) (ctrl.Result, error) {
	ref := repo.Spec.RegistryRef
	if ref == nil {
		conditions.MarkTrue(repo, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonRepositoryListed, "Read anonymously")
		return ctrl.Result{}, nil
	}

	reg := &deliveryv1alpha1.Registry{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: repo.Namespace, Name: ref.Name}, reg); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get Registry %q: %w", ref.Name, err)
		}
		// The Registry watch reconciles again once it is created.
		conditions.MarkFalse(repo, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonRegistryNotFound,
			fmt.Sprintf("Registry %q not found", ref.Name))
		return ctrl.Result{}, nil
	}

	covered, err := registry.URLCovers(reg.Spec.URL, repo.Spec.URL)
	switch {
	case err != nil:
		conditions.MarkFalse(repo, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonRegistryNotCovered, err.Error())
	case !covered:
		conditions.MarkFalse(repo, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonRegistryNotCovered,
			fmt.Sprintf("Registry %q covers %s, not %s", reg.Name, reg.Spec.URL, repo.Spec.URL))
	default:
		conditions.MarkTrue(repo, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonRepositoryListed,
			fmt.Sprintf("Read through Registry %q", reg.Name))
	}
	return ctrl.Result{}, nil
}

// patchStatus writes the status repo took on in this reconcile, unless it is
// unchanged, and returns how to requeue: as the reconcile decided, or soon
// when the patch conflicts. The patch is locked to the version this reconcile
// read or last wrote, so that a reconcile from a stale cache never overwrites
// a newer status.
func (r *RepositoryReconciler) patchStatus(
	ctx context.Context, repo *deliveryv1alpha1.Repository, before *deliveryv1alpha1.RepositoryStatus, result ctrl.Result, err error,
) (ctrl.Result, error) {
	repo.Status.ObservedGeneration = repo.Generation
	if equality.Semantic.DeepEqual(*before, repo.Status) {
		return result, err
	}

	base := repo.DeepCopy()
	base.Status = *before
	perr := r.Status().Patch(ctx, repo, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	switch {
	case perr == nil, apierrors.IsNotFound(perr):
		return result, err
	case apierrors.IsConflict(perr) && err == nil:
		log.FromContext(ctx).V(1).Info("Repository changed during the reconcile, reconciling it again")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	default:
		return result, errors.Join(err, fmt.Errorf("failed to patch Repository status: %w", perr))
	}
}

// repositoryRegistry returns the Registry a Repository is read through, for
// repositoryRegistryField.
func repositoryRegistry(obj client.Object) []string {
	repo, ok := obj.(*deliveryv1alpha1.Repository)
	if !ok || repo.Spec.RegistryRef == nil {
		return nil
	}
	return []string{repo.Spec.RegistryRef.Name}
}

// repositoriesForRegistry enqueues the Repositories read through the
// Registry.
func (r *RepositoryReconciler) repositoriesForRegistry(ctx context.Context, obj client.Object) []reconcile.Request {
	repositories := &deliveryv1alpha1.RepositoryList{}
	if err := r.List(ctx, repositories,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{repositoryRegistryField: obj.GetName()},
	); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Repositories for Registry", "registry", obj.GetName())
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(repositories.Items))
	for i := range repositories.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&repositories.Items[i])})
	}
	return reqs
}

// SetupWithManager sets up the controller with the Manager. A Repository
// is reconciled when its spec changes, and when its Registry is created,
// deleted or changes its url.
func (r *RepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &deliveryv1alpha1.Repository{}, repositoryRegistryField, repositoryRegistry); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Repository{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&deliveryv1alpha1.Registry{}, handler.EnqueueRequestsFromMapFunc(r.repositoriesForRegistry),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("repository").
		Complete(r)
}
