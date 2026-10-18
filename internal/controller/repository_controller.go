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

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/index"
	"github.com/kokumi-dev/kokumi/internal/registry"
)

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
// exists and covers it.
func (r *RepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	repo := &deliveryv1alpha1.Repository{}
	if err := r.Get(ctx, req.NamespacedName, repo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ready, err := r.ready(ctx, repo)
	if err != nil {
		return ctrl.Result{}, err
	}

	before := repo.DeepCopy()
	repo.Status.ObservedGeneration = repo.Generation
	meta.SetStatusCondition(&repo.Status.Conditions, ready)
	if equality.Semantic.DeepEqual(before.Status, repo.Status) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, r.Status().Patch(ctx, repo, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// ready returns the Ready condition of repo: whether its Registry exists
// and covers it.
func (r *RepositoryReconciler) ready(ctx context.Context, repo *deliveryv1alpha1.Repository) (metav1.Condition, error) {
	condition := func(conditionStatus metav1.ConditionStatus, reason, msg string) metav1.Condition {
		return metav1.Condition{
			Type: deliveryv1alpha1.ConditionTypeReady, Status: conditionStatus, Reason: reason, Message: msg, ObservedGeneration: repo.Generation,
		}
	}

	ref := repo.Spec.RegistryRef
	if ref == nil {
		return condition(metav1.ConditionTrue, deliveryv1alpha1.ReasonRepositoryListed, "Read anonymously"), nil
	}

	reg := &deliveryv1alpha1.Registry{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: repo.Namespace, Name: ref.Name}, reg); err != nil {
		if apierrors.IsNotFound(err) {
			return condition(metav1.ConditionFalse, deliveryv1alpha1.ReasonRegistryNotFound, fmt.Sprintf("Registry %q not found", ref.Name)), nil
		}
		return metav1.Condition{}, fmt.Errorf("failed to get Registry %q: %w", ref.Name, err)
	}

	covered, err := covers(reg.Spec.URL, repo.Spec.URL)
	if err != nil {
		return condition(metav1.ConditionFalse, deliveryv1alpha1.ReasonRegistryNotCovered, err.Error()), nil
	}
	if !covered {
		return condition(metav1.ConditionFalse, deliveryv1alpha1.ReasonRegistryNotCovered,
			fmt.Sprintf("Registry %q covers %s, not %s", reg.Name, reg.Spec.URL, repo.Spec.URL)), nil
	}
	return condition(metav1.ConditionTrue, deliveryv1alpha1.ReasonRepositoryListed, fmt.Sprintf("Read through Registry %q", reg.Name)), nil
}

// covers reports whether the Registry url covers the repository url.
func covers(registryURL, repositoryURL string) (bool, error) {
	reg, err := registry.ParseLocation(registryURL)
	if err != nil {
		return false, err
	}
	repo, err := registry.ParseLocation(repositoryURL)
	if err != nil {
		return false, err
	}
	return reg.Covers(repo), nil
}

// repositoriesForRegistry enqueues the Repositories read through the
// Registry.
func (r *RepositoryReconciler) repositoriesForRegistry(ctx context.Context, obj client.Object) []reconcile.Request {
	repositories, err := index.RepositoriesForRegistry(ctx, r.Client, obj.GetNamespace(), obj.GetName())
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list Repositories for Registry", "registry", obj.GetName())
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(repositories))
	for _, repo := range repositories {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&repo)})
	}
	return reqs
}

// SetupWithManager sets up the controller with the Manager. A Repository
// is reconciled when its spec changes, and when its Registry is created,
// deleted or changes its url.
func (r *RepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Repository{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&deliveryv1alpha1.Registry{}, handler.EnqueueRequestsFromMapFunc(r.repositoriesForRegistry),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("repository").
		Complete(r)
}
