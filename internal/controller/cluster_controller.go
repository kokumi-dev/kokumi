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
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/cluster"
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/resources"
)

const (
	// clusterProbeInterval is how often Kokumi checks every Cluster, so that
	// a cluster that goes down or rejects its credential shows up. Each check
	// waits up to clusterProbeJitter of it longer, so that the checks of many
	// Clusters spread out.
	clusterProbeInterval = time.Minute
	clusterProbeJitter   = 0.2

	// clusterConcurrency is how many Clusters are checked at once: a probe
	// waits on the network, up to cluster.ProbeTimeout.
	clusterConcurrency = 8
)

// Reasons of the Events on Clusters.
const (
	reasonRegistered   = "Registered"
	reasonUnregistered = "Unregistered"
)

// clusterProber checks the control planes of clusters. Tests set one;
// internal/cluster's probe by default.
type clusterProber interface {
	Probe(ctx context.Context, cfg *rest.Config) cluster.Health
}

// registration is what registering a Cluster with an engine came to: what
// the Ready message adds about it, the reason when the Cluster cannot be
// registered, and what an Event says when the registration changed.
type registration struct {
	reason, message, event string
}

// clusterRegistrar registers the clusters an engine deploys to with it, and
// unregisters them. argoCD implements it.
type clusterRegistrar interface {
	// Register registers c, reached with kubeconfig, nil for the cluster
	// Kokumi runs in.
	Register(ctx context.Context, c *deliveryv1alpha1.Cluster, kubeconfig *cluster.Kubeconfig) (registration, error)
	// Unregister removes the registration of c, and returns what an Event
	// says about it, "" when there was none.
	Unregister(ctx context.Context, c *deliveryv1alpha1.Cluster) (string, error)
}

// ClusterReconciler reports whether Orders can deploy to a Cluster: whether
// its kubeconfig is usable, its control plane ready, and it is registered
// with the engine of every Environment of its namespace that selects it.
// It keeps the Cluster in-cluster in Kokumi's own namespace.
type ClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Recorder records Events on Clusters.
	Recorder events.EventRecorder
	// Namespace is Kokumi's own namespace, which holds the Cluster
	// in-cluster.
	Namespace string
	// LocalConfig connects to the cluster Kokumi runs in.
	LocalConfig *rest.Config

	// clusters probes clusters. Tests set it; without it, probes go over
	// the network, at most one per Cluster per half probe interval.
	clusters clusterProber
	// probes holds the last probe of every Cluster.
	probes probeCache
	// registrars register Clusters with the engines of the Environments
	// that select them, by deployer type. Tests set them; Argo CD's by
	// default.
	registrars map[deliveryv1alpha1.DeployerType]clusterRegistrar
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=clusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=clusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=environments,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile checks the Cluster and keeps its registration with Argo CD.
func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Cluster", "namespace", req.Namespace, "name", req.Name)

	c := &deliveryv1alpha1.Cluster{}
	if err := r.Get(ctx, req.NamespacedName, c); err != nil {
		if apierrors.IsNotFound(err) {
			if req.NamespacedName == r.inCluster() {
				// Kokumi keeps it: deleting it brings it back.
				logger.Info("Cluster of the cluster Kokumi runs in not found, creating it")
				return ctrl.Result{}, r.createInCluster(ctx)
			}
			logger.Info("Cluster resource not found, ignoring")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Cluster")

		return ctrl.Result{}, fmt.Errorf("failed to get Cluster: %w", err)
	}

	before := c.Status.DeepCopy()
	defer func() { result, err = r.patchStatus(ctx, c, before, result, err) }()

	if !c.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, c)
	}

	if !controllerutil.ContainsFinalizer(c, deliveryv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(c, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, c); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcileCluster(ctx, c)
}

// reconcileCluster connects to the cluster, keeps its registrations with
// the engines that deploy there, checks its control plane, and records in
// Ready whether Orders can deploy to it. It checks again after
// clusterProbeInterval.
func (r *ClusterReconciler) reconcileCluster(ctx context.Context, c *deliveryv1alpha1.Cluster) (ctrl.Result, error) {
	retry := ctrl.Result{RequeueAfter: wait.Jitter(clusterProbeInterval, clusterProbeJitter)}
	cfg, kubeconfig, version, ok := r.connect(ctx, c)
	if !ok {
		return retry, r.unregister(ctx, c, "its kubeconfig cannot be used")
	}

	registered, ok, err := r.register(ctx, c, kubeconfig)
	if err != nil || !ok {
		return retry, err
	}

	health := r.probe(ctx, c, cfg, version)
	c.Status.KubernetesVersion = cmp.Or(health.Version, c.Status.KubernetesVersion)
	switch {
	case health.Unauthorized:
		conditions.MarkFalse(c, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonUnauthorized, health.Message)
	case !health.Healthy:
		conditions.MarkFalse(c, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonUnreachable, health.Message)
	default:
		conditions.MarkTrue(c, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonReady,
			strings.Join(append([]string{health.Message}, registered...), "; "))
	}
	return retry, nil
}

// connect returns the client configuration of the cluster, for one reached
// with a kubeconfig the kubeconfig and the resource version of its Secret,
// and records the cluster's server. When the kubeconfig is missing or
// unusable it marks Ready False and returns false.
func (r *ClusterReconciler) connect(ctx context.Context, c *deliveryv1alpha1.Cluster) (*rest.Config, *cluster.Kubeconfig, string, bool) {
	if c.Spec.Connection == deliveryv1alpha1.ClusterConnectionInCluster || c.Spec.Kubeconfig == nil {
		c.Status.Server = cluster.InClusterServer
		return r.LocalConfig, nil, "", true
	}

	ref := c.Spec.Kubeconfig
	key := cmp.Or(ref.SecretRef.Key, cluster.KubeconfigKey)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: ref.SecretRef.Name}, secret); err != nil {
		msg := fmt.Sprintf("Failed to read the Secret %s: %v", ref.SecretRef.Name, err)
		if apierrors.IsNotFound(err) {
			msg = fmt.Sprintf("The Secret %s does not exist: create it with the cluster's kubeconfig under the key %q", ref.SecretRef.Name, key)
		}
		conditions.MarkFalse(c, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonSecretNotFound, msg)
		return nil, nil, "", false
	}
	kubeconfig, err := cluster.ParseKubeconfig(secret.Data[key])
	if err == nil {
		var cfg *rest.Config
		if cfg, err = kubeconfig.Config(); err == nil {
			c.Status.Server = kubeconfig.Endpoint.Server
			return cfg, &kubeconfig, secret.ResourceVersion, true
		}
	}
	conditions.MarkFalse(c, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonSecretInvalid,
		fmt.Sprintf("The Secret %s holds no self-contained kubeconfig under the key %q: %v", ref.SecretRef.Name, key, err))
	return nil, nil, "", false
}

// register keeps the cluster registered with the engine of every
// Environment of its namespace that selects it, and unregisters it from the
// others. It returns what the Ready message adds about the registrations,
// and false when the cluster cannot be registered, with Ready False.
func (r *ClusterReconciler) register(ctx context.Context, c *deliveryv1alpha1.Cluster, kubeconfig *cluster.Kubeconfig) ([]string, bool, error) {
	selecting, err := r.selectingDeployers(ctx, c)
	if err != nil {
		return nil, false, err
	}
	var messages []string
	registrars := r.registrarsByType()
	for _, deployerType := range slices.Sorted(maps.Keys(registrars)) {
		registrar := registrars[deployerType]
		if !selecting[deployerType] {
			if err := r.unregisterFrom(ctx, c, registrar, "no Environment of its namespace deploys there through it"); err != nil {
				return nil, false, err
			}
			continue
		}
		registered, err := registrar.Register(ctx, c, kubeconfig)
		if registered.event != "" {
			reason := reasonRegistered
			if registered.reason != "" {
				reason = reasonUnregistered
			}
			r.event(c, corev1.EventTypeNormal, reason, "%s", registered.event)
		}
		if registered.reason != "" {
			conditions.MarkFalse(c, deliveryv1alpha1.ConditionTypeReady, registered.reason, registered.message)
			return nil, false, err
		}
		if err != nil {
			return nil, false, err
		}
		messages = append(messages, registered.message)
	}
	return messages, true, nil
}

// selectingDeployers returns the deployer types of the Environments of the
// Cluster's namespace that select it.
func (r *ClusterReconciler) selectingDeployers(ctx context.Context, c *deliveryv1alpha1.Cluster) (map[deliveryv1alpha1.DeployerType]bool, error) {
	environments := &deliveryv1alpha1.EnvironmentList{}
	if err := r.List(ctx, environments, client.InNamespace(c.Namespace), client.UnsafeDisableDeepCopy); err != nil {
		return nil, fmt.Errorf("failed to list Environments: %w", err)
	}
	selecting := map[deliveryv1alpha1.DeployerType]bool{}
	for i := range environments.Items {
		env := &environments.Items[i]
		if !env.DeletionTimestamp.IsZero() {
			continue
		}
		if selection, err := resolve.ClusterSelectionOf(env); err == nil && selection.Selects(c) {
			selecting[env.Spec.Deployer.Type] = true
		}
	}
	return selecting, nil
}

// unregister removes the Cluster's registrations with every engine; why
// goes into the Events.
func (r *ClusterReconciler) unregister(ctx context.Context, c *deliveryv1alpha1.Cluster, why string) error {
	for _, registrar := range r.registrarsByType() {
		if err := r.unregisterFrom(ctx, c, registrar, why); err != nil {
			return err
		}
	}
	return nil
}

// unregisterFrom removes the Cluster's registration with registrar, and
// records an Event that says why when there was one.
func (r *ClusterReconciler) unregisterFrom(ctx context.Context, c *deliveryv1alpha1.Cluster, registrar clusterRegistrar, why string) error {
	unregistered, err := registrar.Unregister(ctx, c)
	if unregistered != "" {
		r.event(c, corev1.EventTypeNormal, reasonUnregistered, "%s: %s", unregistered, why)
	}
	return err
}

// registrarsByType returns the engines Clusters are registered with, by
// deployer type.
func (r *ClusterReconciler) registrarsByType() map[deliveryv1alpha1.DeployerType]clusterRegistrar {
	if r.registrars != nil {
		return r.registrars
	}
	return map[deliveryv1alpha1.DeployerType]clusterRegistrar{
		deliveryv1alpha1.DeployerTypeArgoCD: &argoCD{Client: r.Client, namespace: r.Namespace},
	}
}

// reconcileDelete unregisters the cluster from every engine and removes
// the finalizer. What runs on the cluster keeps running.
func (r *ClusterReconciler) reconcileDelete(ctx context.Context, c *deliveryv1alpha1.Cluster) (ctrl.Result, error) {
	log.FromContext(ctx).Info("Handling deletion of Cluster")
	if err := r.unregister(ctx, c, "the Cluster is being deleted"); err != nil {
		return ctrl.Result{}, err
	}
	r.probes.forget(client.ObjectKeyFromObject(c).String())
	if controllerutil.RemoveFinalizer(c, deliveryv1alpha1.Finalizer) {
		if err := r.Update(ctx, c); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}
	return ctrl.Result{}, nil
}

// event records an Event on the Cluster.
func (r *ClusterReconciler) event(c *deliveryv1alpha1.Cluster, eventType, reason, msg string, args ...any) {
	if r.Recorder != nil {
		r.Recorder.Eventf(c, nil, eventType, reason, "Register", msg, args...)
	}
}

// probe checks the control plane of the cluster c connects to with cfg, at
// most once per half probe interval: a Cluster reconciled for another reason
// in between reuses the last probe, unless its kubeconfig Secret changed
// (version). Every in-cluster Cluster shares one probe of the cluster Kokumi
// runs in.
func (r *ClusterReconciler) probe(ctx context.Context, c *deliveryv1alpha1.Cluster, cfg *rest.Config, version string) cluster.Health {
	if r.clusters != nil {
		return r.clusters.Probe(ctx, cfg)
	}
	key := client.ObjectKeyFromObject(c).String()
	if c.Spec.Connection == deliveryv1alpha1.ClusterConnectionInCluster {
		key = cluster.InClusterServer
	}
	return r.probes.probe(key, version, time.Now(), func() cluster.Health { return cluster.Probe(ctx, cfg) })
}

// probeCache holds the last probe of every cluster, so that the reconciles
// a Cluster gets for other reasons than its timer do not probe it again.
// Its zero value is empty and ready to use.
type probeCache struct {
	mu      sync.Mutex
	entries map[string]probeEntry
}

// probeEntry is a probe of a cluster, of the version of its kubeconfig, and
// when it ran.
type probeEntry struct {
	version string
	health  cluster.Health
	at      time.Time
}

// probe returns the probe of key when one of version ran less than half a
// probe interval before now, and otherwise probes again with probe. The
// probe runs outside the lock: probes of other clusters do not wait for it.
func (p *probeCache) probe(key, version string, now time.Time, probe func() cluster.Health) cluster.Health {
	p.mu.Lock()
	entry, ok := p.entries[key]
	p.mu.Unlock()
	if ok && entry.version == version && now.Sub(entry.at) < clusterProbeInterval/2 {
		return entry.health
	}
	health := probe()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries == nil {
		p.entries = map[string]probeEntry{}
	}
	p.entries[key] = probeEntry{version: version, health: health, at: now}
	return health
}

// forget drops the probe of key, of a Cluster that is gone.
func (p *probeCache) forget(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.entries, key)
}

// patchStatus writes the status c took on in this reconcile, unless it is
// unchanged. A conflict reconciles again soon.
func (r *ClusterReconciler) patchStatus(
	ctx context.Context, c *deliveryv1alpha1.Cluster, before *deliveryv1alpha1.ClusterStatus, result ctrl.Result, err error,
) (ctrl.Result, error) {
	c.Status.ObservedGeneration = c.Generation
	if equality.Semantic.DeepEqual(*before, c.Status) {
		return result, err
	}

	base := c.DeepCopy()
	base.Status = *before
	perr := r.Status().Patch(ctx, c, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	switch {
	case perr == nil, apierrors.IsNotFound(perr):
		return result, err
	case apierrors.IsConflict(perr) && err == nil:
		log.FromContext(ctx).V(1).Info("Cluster changed during the reconcile, reconciling it again")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	default:
		return ctrl.Result{}, errors.Join(err, fmt.Errorf("failed to patch Cluster status: %w", perr))
	}
}

// inCluster returns the key of the Cluster in-cluster Kokumi keeps.
func (r *ClusterReconciler) inCluster() client.ObjectKey {
	return client.ObjectKey{Namespace: r.Namespace, Name: cluster.InCluster}
}

// createInCluster creates the Cluster of the cluster Kokumi runs in.
func (r *ClusterReconciler) createInCluster(ctx context.Context) error {
	if err := r.Create(ctx, resources.InClusterCluster(r.Namespace)); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create the Cluster of the cluster Kokumi runs in: %w", err)
	}
	return nil
}

// clusterKubeconfigSecret returns the Secret that holds a Cluster's
// kubeconfig, for FieldKubeconfigSecretName.
func clusterKubeconfigSecret(obj client.Object) []string {
	c, ok := obj.(*deliveryv1alpha1.Cluster)
	if !ok || c.Spec.Kubeconfig == nil {
		return nil
	}
	return []string{c.Spec.Kubeconfig.SecretRef.Name}
}

// clusterServer returns the API server of a Cluster without a trailing
// slash, for FieldClusterServer.
func clusterServer(obj client.Object) []string {
	c, ok := obj.(*deliveryv1alpha1.Cluster)
	if !ok || c.Status.Server == "" {
		return nil
	}
	return []string{serverKey(c.Status.Server)}
}

// clustersOfSecret maps a kubeconfig Secret to the Clusters that name it,
// and a cluster Secret Kokumi manages in Argo CD's namespace to its Cluster
// and the Clusters that wait for its server.
func (r *ClusterReconciler) clustersOfSecret(ctx context.Context, obj client.Object) []ctrl.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}
	if reqs, registration := clustersOfArgoCDSecret(ctx, r.Client, secret); registration {
		return reqs
	}
	clusters := &deliveryv1alpha1.ClusterList{}
	if err := r.List(ctx, clusters, client.InNamespace(secret.Namespace),
		client.MatchingFields{deliveryv1alpha1.FieldKubeconfigSecretName: secret.Name}, client.UnsafeDisableDeepCopy); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Clusters of Secret", "secret", secret.Name)
		return nil
	}
	return clusterRequests(clusters.Items)
}

// clustersOfEnvironment maps an Environment to the Clusters of its
// namespace it selects, whose registration it may change. An update maps
// the Environment before and after, so that the Clusters it no longer
// selects reconcile too.
func (r *ClusterReconciler) clustersOfEnvironment(ctx context.Context, obj client.Object) []ctrl.Request {
	env, ok := obj.(*deliveryv1alpha1.Environment)
	if !ok {
		return nil
	}
	selection, err := resolve.ClusterSelectionOf(env)
	if err != nil {
		return nil
	}
	clusters := &deliveryv1alpha1.ClusterList{}
	if err := r.List(ctx, clusters, client.InNamespace(env.Namespace), client.UnsafeDisableDeepCopy); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Clusters of Environment", "environment", env.Name)
		return nil
	}
	return clusterRequests(slices.DeleteFunc(clusters.Items, func(c deliveryv1alpha1.Cluster) bool { return !selection.Matches(&c) }))
}

// clusterRequests returns the requests of clusters.
func clusterRequests(clusters []deliveryv1alpha1.Cluster) []ctrl.Request {
	reqs := make([]ctrl.Request, 0, len(clusters))
	for i := range clusters {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&clusters[i])})
	}
	return reqs
}

// clusterChanged passes the Cluster updates that change where Orders deploy
// or whether they can: its labels, which Environments select it by, its
// connection, its server, its readiness, and the start of its deletion. The
// probes that find a cluster as it was pass nothing. Creates and deletes
// always pass.
var clusterChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		before, okOld := e.ObjectOld.(*deliveryv1alpha1.Cluster)
		after, okNew := e.ObjectNew.(*deliveryv1alpha1.Cluster)
		if !okOld || !okNew {
			return false
		}
		readyBefore := meta.FindStatusCondition(before.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
		readyAfter := meta.FindStatusCondition(after.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
		return !maps.Equal(before.Labels, after.Labels) ||
			before.Spec.Connection != after.Spec.Connection ||
			before.Status.Server != after.Status.Server ||
			before.DeletionTimestamp.IsZero() != after.DeletionTimestamp.IsZero() ||
			(readyBefore == nil) != (readyAfter == nil) ||
			(readyBefore != nil && (readyBefore.Status != readyAfter.Status || readyBefore.Reason != readyAfter.Reason))
	},
}

// enqueueAtStart returns a source that enqueues the object key once, when
// the controller starts: the reconcile of an object Kokumi keeps creates it
// when it is missing.
func enqueueAtStart(key client.ObjectKey) source.Source {
	return source.Func(func(_ context.Context, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
		queue.Add(reconcile.Request{NamespacedName: key})
		return nil
	})
}

// SetupWithManager sets up the controller with the Manager. A Cluster
// reconciles when its spec or labels change, when its kubeconfig Secret or
// a registration with Argo CD of its server changes, and when an
// Environment that selects it does. The Cluster in-cluster is reconciled at
// start, which creates it when it is missing.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	indexer := mgr.GetFieldIndexer()
	if err := indexer.IndexField(context.Background(), &deliveryv1alpha1.Cluster{},
		deliveryv1alpha1.FieldKubeconfigSecretName, clusterKubeconfigSecret); err != nil {
		return err
	}
	if err := indexer.IndexField(context.Background(), &deliveryv1alpha1.Cluster{},
		deliveryv1alpha1.FieldClusterServer, clusterServer); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Cluster{}, builder.WithPredicates(predicate.Or[client.Object](
			predicate.GenerationChangedPredicate{}, predicate.LabelChangedPredicate{}))).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.clustersOfSecret),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{})).
		Watches(&deliveryv1alpha1.Environment{}, handler.EnqueueRequestsFromMapFunc(r.clustersOfEnvironment),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WatchesRawSource(enqueueAtStart(r.inCluster())).
		WithOptions(controller.Options{MaxConcurrentReconciles: clusterConcurrency}).
		Named("cluster").
		Complete(r)
}
