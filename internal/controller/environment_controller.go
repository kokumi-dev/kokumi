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
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/cluster"
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/resources"
)

// maxListedClusters is how many clusters a condition message names; it
// counts the others.
const maxListedClusters = 10

// admission is whether Orders of an Environment may deploy through its
// engine: admitted when reason is "", and otherwise why not.
type admission struct {
	reason, message string
}

// environmentAdmitter decides for the engine of an Environment whether
// Orders may deploy through it. argoCD implements it.
type environmentAdmitter interface {
	Admit(ctx context.Context, env *deliveryv1alpha1.Environment) (admission, error)
}

// EnvironmentReconciler counts the Clusters of its namespace an Environment
// selects, and reports whether it selects any and its engine admits it. It
// keeps the Environment local in Kokumi's own namespace.
type EnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Namespace is Kokumi's own namespace. Its Environments may use any
	// Argo CD AppProject, and it holds the Environment local.
	Namespace string

	// admitters admit Environments, by their deployer type. Tests set them;
	// Argo CD's by default.
	admitters map[deliveryv1alpha1.DeployerType]environmentAdmitter
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=environments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=environments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=environments/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=argoproj.io,resources=appprojects,verbs=get;list;watch

// Reconcile resolves the Clusters of the Environment and reports whether
// its engine admits it.
func (r *EnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Environment", "namespace", req.Namespace, "name", req.Name)

	env := &deliveryv1alpha1.Environment{}
	if err := r.Get(ctx, req.NamespacedName, env); err != nil {
		if apierrors.IsNotFound(err) {
			if req.NamespacedName == r.local() {
				// Kokumi keeps it: deleting it brings it back.
				logger.Info("Environment local not found, creating it")
				return ctrl.Result{}, r.createLocal(ctx)
			}
			logger.Info("Environment resource not found, ignoring")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Environment")

		return ctrl.Result{}, fmt.Errorf("failed to get Environment: %w", err)
	}

	before := env.Status.DeepCopy()
	defer func() { result, err = r.patchStatus(ctx, env, before, result, err) }()

	if !env.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, env)
	}

	if !controllerutil.ContainsFinalizer(env, deliveryv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(env, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, env); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, r.reconcileEnvironment(ctx, env)
}

// reconcileEnvironment counts the Clusters the Environment selects and
// records in Ready whether it selects any and its engine admits it. Each
// Cluster reports on its own whether Kokumi reaches it.
func (r *EnvironmentReconciler) reconcileEnvironment(ctx context.Context, env *deliveryv1alpha1.Environment) error {
	clusters := &deliveryv1alpha1.ClusterList{}
	if err := r.List(ctx, clusters, client.InNamespace(env.Namespace), client.UnsafeDisableDeepCopy); err != nil {
		return fmt.Errorf("failed to list Clusters: %w", err)
	}
	selected, err := resolve.SelectedClusters(env, clusters.Items)
	if err != nil {
		env.Status.Clusters = nil
		conditions.MarkFalse(env, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonInvalidSelector, "clusterSelector: "+err.Error())
		return nil
	}
	names := make([]string, 0, len(selected))
	counts := &deliveryv1alpha1.EnvironmentClusters{Selected: int32(len(selected))}
	for _, c := range selected {
		names = append(names, c.Name)
		if meta.IsStatusConditionTrue(c.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) {
			counts.Ready++
		}
	}
	env.Status.Clusters = counts

	admitted, err := r.admitter(env).Admit(ctx, env)
	if err != nil {
		return err
	}
	switch {
	case len(selected) == 0:
		conditions.MarkFalse(env, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonNoClusters,
			"The clusterSelector selects no Cluster in namespace "+env.Namespace)
	case admitted.reason != "":
		conditions.MarkFalse(env, deliveryv1alpha1.ConditionTypeReady, admitted.reason, admitted.message)
	default:
		conditions.MarkTrue(env, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonReady,
			"Deploys to "+conditions.List(names, ", ", maxListedClusters))
	}
	return nil
}

// admitter returns the engine that admits env.
func (r *EnvironmentReconciler) admitter(env *deliveryv1alpha1.Environment) environmentAdmitter {
	if a, ok := r.admitters[env.Spec.Deployer.Type]; ok {
		return a
	}
	return &argoCD{Client: r.Client, namespace: r.Namespace}
}

// reconcileDelete removes the finalizer: Environments own nothing outside
// their status.
func (r *EnvironmentReconciler) reconcileDelete(ctx context.Context, env *deliveryv1alpha1.Environment) (ctrl.Result, error) {
	log.FromContext(ctx).Info("Handling deletion of Environment")
	if controllerutil.RemoveFinalizer(env, deliveryv1alpha1.Finalizer) {
		if err := r.Update(ctx, env); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}
	return ctrl.Result{}, nil
}

// patchStatus writes the status env took on in this reconcile, unless it is
// unchanged. A conflict reconciles again soon.
func (r *EnvironmentReconciler) patchStatus(
	ctx context.Context, env *deliveryv1alpha1.Environment, before *deliveryv1alpha1.EnvironmentStatus, result ctrl.Result, err error,
) (ctrl.Result, error) {
	env.Status.ObservedGeneration = env.Generation
	if equality.Semantic.DeepEqual(*before, env.Status) {
		return result, err
	}

	base := env.DeepCopy()
	base.Status = *before
	perr := r.Status().Patch(ctx, env, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	switch {
	case perr == nil, apierrors.IsNotFound(perr):
		return result, err
	case apierrors.IsConflict(perr) && err == nil:
		log.FromContext(ctx).V(1).Info("Environment changed during the reconcile, reconciling it again")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	default:
		return ctrl.Result{}, errors.Join(err, fmt.Errorf("failed to patch Environment status: %w", perr))
	}
}

// local returns the key of the Environment local Kokumi keeps.
func (r *EnvironmentReconciler) local() client.ObjectKey {
	return client.ObjectKey{Namespace: r.Namespace, Name: cluster.LocalEnvironment}
}

// createLocal creates the Environment local.
func (r *EnvironmentReconciler) createLocal(ctx context.Context) error {
	if err := r.Create(ctx, resources.LocalEnvironment(r.Namespace)); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create the Environment local: %w", err)
	}
	return nil
}

// environmentsOfCluster maps a Cluster to the Environments of its namespace
// that select it by its labels. An update maps the Cluster before and after,
// so that an Environment it leaves counts again too.
func (r *EnvironmentReconciler) environmentsOfCluster(ctx context.Context, obj client.Object) []ctrl.Request {
	c, ok := obj.(*deliveryv1alpha1.Cluster)
	if !ok {
		return nil
	}
	environments := &deliveryv1alpha1.EnvironmentList{}
	if err := r.List(ctx, environments, client.InNamespace(c.Namespace), client.UnsafeDisableDeepCopy); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Environments", "namespace", c.Namespace)
		return nil
	}
	return environmentRequests(slices.DeleteFunc(environments.Items, func(env deliveryv1alpha1.Environment) bool {
		selection, err := resolve.ClusterSelectionOf(&env)
		return err != nil || !selection.Matches(c)
	}))
}

// environmentsOfProject maps an Argo CD AppProject to the Environments that
// deploy through it.
func (r *EnvironmentReconciler) environmentsOfProject(ctx context.Context, obj client.Object) []ctrl.Request {
	environments := &deliveryv1alpha1.EnvironmentList{}
	if err := r.List(ctx, environments, client.UnsafeDisableDeepCopy); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Environments")
		return nil
	}
	return environmentRequests(slices.DeleteFunc(environments.Items, func(env deliveryv1alpha1.Environment) bool {
		return env.Spec.Deployer.ArgoCD == nil || env.Spec.Deployer.ArgoCD.Project != obj.GetName()
	}))
}

// allEnvironments enqueues every Environment, once the AppProject watch
// starts.
func (r *EnvironmentReconciler) allEnvironments(ctx context.Context, _ client.Object) []ctrl.Request {
	environments := &deliveryv1alpha1.EnvironmentList{}
	if err := r.List(ctx, environments, client.UnsafeDisableDeepCopy); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Environments")
		return nil
	}
	return environmentRequests(environments.Items)
}

// environmentRequests returns the requests of environments.
func environmentRequests(environments []deliveryv1alpha1.Environment) []ctrl.Request {
	reqs := make([]ctrl.Request, 0, len(environments))
	for i := range environments {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&environments[i])})
	}
	return reqs
}

// SetupWithManager sets up the controller with the Manager. It watches the
// Clusters, whose labels decide which Environments select them, and Argo
// CD's AppProjects once their CRD is installed. The Environment local is
// reconciled at start, which creates it when it is missing.
func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	appProjects, err := newOptionalWatch(mgr, newAppProject())
	if err != nil {
		return err
	}
	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Environment{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&deliveryv1alpha1.Cluster{}, handler.EnqueueRequestsFromMapFunc(r.environmentsOfCluster),
			builder.WithPredicates(clusterChanged)).
		WatchesRawSource(enqueueAtStart(r.local())).
		WatchesRawSource(source.Channel(appProjects.Started(), handler.EnqueueRequestsFromMapFunc(r.allEnvironments))).
		Named("environment").
		Build(r)
	if err != nil {
		return err
	}
	return appProjects.Add(mgr, c, handler.EnqueueRequestsFromMapFunc(r.environmentsOfProject),
		predicate.GenerationChangedPredicate{})
}
