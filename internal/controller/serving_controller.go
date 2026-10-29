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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/argocd"
	"github.com/kokumi-dev/kokumi/internal/cluster"
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/evidence"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/promotion"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/vulnerability"
)

// historyPassInterval is how soon a Serving reconciles again after it wrote
// a time to its history that a record pushed in the next pass contains. Its
// own status writes trigger no reconcile.
const historyPassInterval = time.Second

// servingRegistries connects to the artifact of a Preparation, and to the
// vendored images of a Product. *registry.Resolver implements it.
type servingRegistries interface {
	ConnectRecorded(ctx context.Context, namespace string, recorded *corev1.LocalObjectReference, url string,
		fallback func(context.Context) ([]corev1.LocalObjectReference, error)) (registry.Connection, error)
	Scope(ctx context.Context, namespace string, refs []corev1.LocalObjectReference) (registry.Scope, error)
}

// servingSigning provides the trust the evidence of a Preparation verifies
// against and tells when it changes. *signing.Provider implements it.
type servingSigning interface {
	State(ctx context.Context) (signing.State, error)
	OnChange(notify func())
}

// servingDeployer deploys Servings through the engine of their Order's
// Environment. argoCD implements it.
type servingDeployer interface {
	// Admit reports whether Orders of env may deploy through the engine.
	Admit(ctx context.Context, env *deliveryv1alpha1.Environment) (admission, error)
	// Apply points the deployment of serving on target at deployed, and
	// returns what the engine reports there.
	Apply(ctx context.Context, env *deliveryv1alpha1.Environment, serving *deliveryv1alpha1.Serving,
		deployed *promotion.Entry, target deployTarget) (targetState, error)
	// Remove deletes the deployments of serving on the clusters keep does
	// not hold, all of them for nil, and returns those clusters.
	Remove(ctx context.Context, serving *deliveryv1alpha1.Serving, keep map[string]bool) ([]string, error)
}

// targetState is what an engine reports on the deployment of a Serving to
// one cluster.
type targetState struct {
	// health is how the deployment stands at revision.
	health promotion.Health
	// revision is the artifact digest the engine reports synced there.
	revision string
	message  string
	// held is the reason the engine does not deploy there, "" when it
	// does; message says why.
	held string
}

// ServingReconciler reconciles a Serving object. It promotes the Preparation
// the Serving converges to once it passes its gates, deploys it to every
// cluster of the Order's Environment through the Environment's engine, and
// records every promotion and every healthy deployment in the Serving's
// history and, signed, in the registry.
type ServingReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Registries connects to the destination holding the artifacts.
	Registries servingRegistries
	// Signing holds the keys the evidence of a Preparation must verify
	// against before it is deployed.
	Signing servingSigning
	// Recorder records Events on Servings.
	Recorder events.EventRecorder
	// InstallNamespace holds the Kitchen, whose scanning interval sets the
	// default age of the scans a check accepts.
	InstallNamespace string
	// BundledRegistryHost is the host of the bundled registry, which only the
	// cluster Kokumi runs in reaches.
	BundledRegistryHost string

	// artifacts remembers the Preparations whose evidence verified.
	artifacts evidence.Verifier
	// records remembers the upstream deployment records that verified.
	records promotion.RecordVerifier
	// reports remembers the findings of verified vulnerability reports.
	reports vulnerability.Reports

	// deployers deploy through the engines of Environments, by deployer
	// type. Tests set them; Argo CD's by default.
	deployers map[deliveryv1alpha1.DeployerType]servingDeployer

	// now returns the current time. Tests set it.
	now func() time.Time
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=approvals,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=environments,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=argoproj.io,resources=appprojects,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile deploys the Preparation the Serving converges to and reports on
// its deployment.
func (r *ServingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
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

	before := serving.Status.DeepCopy()
	defer func() { result, err = r.patchStatus(ctx, serving, before, result, err) }()

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

// reconcileServing promotes the Preparation the Serving converges to once it
// passes its gates, and deploys the promoted one. A Preparation that does not
// pass leaves the deployed one untouched, which keeps being deployed and
// observed meanwhile.
func (r *ServingReconciler) reconcileServing(ctx context.Context, serving *deliveryv1alpha1.Serving) (ctrl.Result, error) {
	var wait time.Duration
	order, candidate, held, err := r.targetPreparation(ctx, serving)
	if candidate != nil {
		wait, held, err = r.promote(ctx, serving, order, candidate)
	} else if err == nil {
		setHistory(serving, historyOf(serving).Drop())
		serving.Status.TargetPreparationName = ""
	}

	next, derr := r.deploy(ctx, serving, order, held)
	if err = errors.Join(err, derr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: soonest(wait, next)}, nil
}

// promote decides whether the Serving promotes candidate. It does once
// candidate passes its gates, in this order:
//
//  1. It is Ready.
//  2. Its upstream stages deployed its Product revision healthily for their
//     soak time (UpstreamDeployed).
//  3. It passes its checks (ChecksPassed).
//  4. It is approved (Approved). Passing its upstream stages and checks
//     makes it the target, whose votes the Preparation controller seals.
//  5. Its evidence verifies in the registry (Verified).
//
// A promotion takes two passes, because the record's bytes depend on the
// time it is decided at: the first opens a pending entry in the history, the
// second records the promotion in the registry and starts the entry, which
// deploy then deploys. For the deployed Preparation the gates only report;
// they hold new deployments. It returns how soon to reconcile again, and
// what holds candidate, "" when nothing does.
func (r *ServingReconciler) promote(
	ctx context.Context, serving *deliveryv1alpha1.Serving, order *deliveryv1alpha1.Order, candidate *deliveryv1alpha1.Preparation,
) (time.Duration, string, error) {
	logger := log.FromContext(ctx)

	history := historyOf(serving)
	step := history.Next(candidate.Name)
	current := step == promotion.StepDeployed
	hold := func(msg string, wait time.Duration) (time.Duration, string, error) {
		setHistory(serving, history.Drop())
		return wait, msg, nil
	}

	// A Preparation is served only once it is Ready, under both promotion
	// modes. The Preparation watch triggers the next reconcile.
	if !meta.IsStatusConditionTrue(candidate.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) {
		logger.Info("Preparation not ready, waiting", "preparation", candidate.Name)
		serving.Status.TargetPreparationName = ""
		return hold(fmt.Sprintf("Waiting for Preparation %q to become ready", candidate.Name), 0)
	}

	upstream, err := r.upstream(ctx, serving, order, candidate, history.Drop())
	if err != nil {
		return 0, "", err
	}
	checks, err := r.checks(ctx, serving, candidate)
	if err != nil {
		return 0, "", err
	}
	if current && !checks.passed {
		logger.Info("Deployed Preparation no longer passes its checks, keeping it deployed", "preparation", candidate.Name)
	}
	target := current || (upstream.Satisfied() && checks.passed)

	approved, err := r.gate(ctx, serving, candidate, target)
	if err != nil {
		return 0, "", err
	}
	if !target {
		conditions.MarkUnknown(serving, deliveryv1alpha1.ConditionTypeVerified, deliveryv1alpha1.ReasonPending,
			candidate.Name+": verified once its upstream stages and checks pass")
		if !upstream.Satisfied() {
			return hold("Waiting for upstream stages: "+conditionMessage(serving, deliveryv1alpha1.ConditionTypeUpstreamDeployed), upstream.RequeueAfter)
		}
		return hold("Held by its checks: "+conditionMessage(serving, deliveryv1alpha1.ConditionTypeChecksPassed), checks.recheck)
	}
	if !approved && !current {
		return hold("Waiting for approvals: "+conditionMessage(serving, deliveryv1alpha1.ConditionTypeApproved), 0)
	}

	// The registry is the record: the signature and attestations are
	// verified there, not trusted from the Preparation's status.
	verified, err := r.verify(ctx, serving, candidate)
	if err != nil {
		return 0, "", err
	}
	switch {
	case current:
		setHistory(serving, history.Drop())
		return checks.recheck, "", nil
	case !verified:
		return hold("Waiting for its evidence to verify: "+conditionMessage(serving, deliveryv1alpha1.ConditionTypeVerified), 0)
	case step == promotion.StepOpen:
		setHistory(serving, history.Open(entryOf(candidate), r.clock()))
		return historyPassInterval, "Recording the promotion of Preparation " + candidate.Name, nil
	}

	record, err := r.recordPromotion(ctx, serving, order, candidate, history, upstream, checks)
	if err != nil {
		return 0, "", err
	}
	setHistory(serving, history.Start(record))
	logger.Info("Promoted Preparation", "preparation", candidate.Name, "productRevision", candidate.Spec.ProductRevision, "record", record)
	return checks.recheck, "", nil
}

// deploy keeps the deployment of the deployed Preparation current on each
// target cluster of the Serving, through the engine of its Environment,
// and records what the engine reports on each: the targets with their
// Ready conditions, the Serving's Ready condition, its health in the
// history and, once it is healthy, its deployment record and the summary
// of its scans. Ready describes what runs; held, what holds the Preparation
// the Serving converges to, is added to its message, and is its message
// while nothing runs. A deployed Preparation that was deleted holds the
// Serving and leaves what runs as it is. It returns how soon to reconcile
// again.
func (r *ServingReconciler) deploy(
	ctx context.Context, serving *deliveryv1alpha1.Serving, order *deliveryv1alpha1.Order, held string,
) (time.Duration, error) {
	logger := log.FromContext(ctx)

	history := historyOf(serving)
	deployed := history.Deployed()
	if deployed == nil || order == nil {
		if held != "" {
			markPending(serving, held)
		}
		return 0, nil
	}

	preparation := &deliveryv1alpha1.Preparation{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: deployed.Preparation}, preparation); err != nil {
		if !apierrors.IsNotFound(err) {
			return 0, fmt.Errorf("failed to get Preparation %q: %w", deployed.Preparation, err)
		}
		// Argo CD keeps syncing the artifact, which outlives the object,
		// until another Preparation is promoted.
		conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonPreparationNotFound,
			fmt.Sprintf("Preparation %s, which is deployed, was deleted; promote another Preparation", deployed.Preparation))
		markHeld(serving, held)
		return 0, nil
	}

	env, targets, reason, msg, err := r.targets(ctx, serving, order, preparation)
	if err != nil {
		return 0, err
	}
	if reason != "" {
		// The Order may not deploy at all: Kokumi stops deploying, and the
		// Environment, Cluster and AppProject watches reconcile again once
		// that changes.
		if err := r.remove(ctx, serving, map[string]bool{}); err != nil {
			return 0, err
		}
		setTargets(serving, deployed, nil, nil)
		conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeReady, reason, msg)
		markHeld(serving, held)
		return 0, nil
	}
	deployer := r.deployer(env)
	states, err := r.applyTargets(ctx, deployer, env, serving, deployed, targets)
	if err != nil {
		markDeploymentFailed(serving, "failed to deploy: "+err.Error())
		markHeld(serving, held)
		return 0, fmt.Errorf("failed to deploy: %w", err)
	}
	setTargets(serving, deployed, targets, states)
	observation := observationOf(deployed, targets, states)
	markDeployment(serving, deployed, targets, observation)
	markHeld(serving, held)

	// A time goes into the status a pass before the record that contains it
	// goes into the registry, so that a retry pushes equal bytes.
	if observed, changed := history.Observe(observation, r.clock()); changed {
		logger.Info("Recorded deployment state", "preparation", deployed.Preparation, "health", observation.Health)
		if now := observed.Deployed(); deployed.Healthy.IsZero() && !now.Healthy.IsZero() {
			// The summary described what ran before; the next pass
			// describes the Preparation that is healthy now.
			serving.Status.DeployedVulnerabilities = nil
		}
		setHistory(serving, observed)
		return historyPassInterval, nil
	}

	if err := r.recordDeployments(ctx, serving, history, targets, states); err != nil {
		return 0, err
	}
	return 0, r.recordDeployedVulnerabilities(ctx, serving, deployed)
}

// deployTarget is a cluster a Serving deploys to, or, when holdReason is
// set, would deploy to and cannot.
type deployTarget struct {
	// cluster names the Cluster, in the Serving's namespace.
	cluster string
	// server is the URL of the cluster's API server.
	server string
	// namespace is the namespace on the cluster the Preparation deploys
	// into: its target namespace.
	namespace               string
	holdReason, holdMessage string
}

// targets returns the Environment of the Serving's Order and the clusters
// the Serving deploys preparation, the deployed one, to: every Cluster the
// Environment selects, into the Preparation's target namespace. The engine
// of the Environment admits it each time, so that a change of the
// Environment or of what the engine allows counts at once. A target that
// cannot be deployed to is held, with the reason and why. When the Order
// may not deploy at all, it returns no target, with the reason and why.
func (r *ServingReconciler) targets(
	ctx context.Context, serving *deliveryv1alpha1.Serving, order *deliveryv1alpha1.Order, preparation *deliveryv1alpha1.Preparation,
) (*deliveryv1alpha1.Environment, []deployTarget, string, string, error) {
	name := order.Spec.EnvironmentRef.Name
	env := &deliveryv1alpha1.Environment{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: name}, env); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, deliveryv1alpha1.ReasonEnvironmentNotFound,
				fmt.Sprintf("Environment %q does not exist in namespace %s", name, serving.Namespace), nil
		}
		return nil, nil, "", "", fmt.Errorf("failed to get Environment %q: %w", name, err)
	}
	clusters := &deliveryv1alpha1.ClusterList{}
	if err := r.List(ctx, clusters, client.InNamespace(serving.Namespace), client.UnsafeDisableDeepCopy); err != nil {
		return nil, nil, "", "", fmt.Errorf("failed to list Clusters: %w", err)
	}
	// In the order the Environment reports them: its selector, then its
	// engine.
	selected, err := resolve.SelectedClusters(env, clusters.Items)
	switch {
	case err != nil:
		return env, nil, deliveryv1alpha1.ReasonInvalidSelector, fmt.Sprintf("Environment %s: clusterSelector: %v", name, err), nil
	case len(selected) == 0:
		return env, nil, deliveryv1alpha1.ReasonNoClusters,
			fmt.Sprintf("Environment %s selects no Cluster in namespace %s", name, serving.Namespace), nil
	}
	admitted, err := r.deployer(env).Admit(ctx, env)
	if err != nil {
		return nil, nil, "", "", err
	}
	if admitted.reason != "" {
		return env, nil, admitted.reason, fmt.Sprintf("Environment %s: %s", name, admitted.message), nil
	}

	bundled := r.bundledPull(preparation)
	servers := map[string]string{}
	targets := make([]deployTarget, 0, len(selected))
	for _, c := range selected {
		targets = append(targets, targetOf(c, preparation.Spec.TargetNamespace, bundled, servers))
	}
	return env, targets, "", "", nil
}

// targetOf returns the target of the Cluster c, deploying into namespace.
// bundled is an image the kubelet would pull from the bundled registry.
// servers maps the servers of the targets before it, in name order, to
// their Clusters: of two Clusters for one server, only the first deploys.
func targetOf(c *deliveryv1alpha1.Cluster, namespace, bundled string, servers map[string]string) deployTarget {
	inCluster := c.Spec.Connection == deliveryv1alpha1.ClusterConnectionInCluster
	target := deployTarget{cluster: c.Name, server: c.Status.Server, namespace: namespace}
	if inCluster {
		target.server = cluster.InClusterServer
	}

	ready := meta.FindStatusCondition(c.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
	switch first, taken := servers[serverKey(target.server)]; {
	case ready == nil || ready.Status != metav1.ConditionTrue:
		msg := "Cluster " + c.Name + " has not been checked yet"
		if ready != nil {
			msg = fmt.Sprintf("Cluster %s is not ready: %s", c.Name, ready.Message)
		}
		target.holdReason, target.holdMessage = deliveryv1alpha1.ReasonClusterNotReady, msg
		return target
	case taken:
		target.holdReason, target.holdMessage = deliveryv1alpha1.ReasonClusterConflict, fmt.Sprintf(
			"Cluster %s connects to %s, as Cluster %s does, which deploys there; one cluster is one Cluster", c.Name, target.server, first)
	case !inCluster && bundled != "":
		target.holdReason, target.holdMessage = deliveryv1alpha1.ReasonRegistryUnreachable, fmt.Sprintf(
			"The kubelet would pull %s from the bundled registry, which only the cluster Kokumi runs in reaches; vendor the images to a registry every cluster reaches",
			bundled)
	}
	if _, taken := servers[serverKey(target.server)]; !taken {
		servers[serverKey(target.server)] = c.Name
	}
	return target
}

// deployer returns the engine of env.
func (r *ServingReconciler) deployer(env *deliveryv1alpha1.Environment) servingDeployer {
	if d, ok := r.deployers[env.Spec.Deployer.Type]; ok {
		return d
	}
	return &argoCD{Client: r.Client, namespace: r.InstallNamespace}
}

// remove stops the Serving's deployments on the clusters keep does not
// hold, through every engine, all of them for nil, and records an Event for
// each cluster it stops deploying to while keep is set. What runs there
// keeps running.
func (r *ServingReconciler) remove(ctx context.Context, serving *deliveryv1alpha1.Serving, keep map[string]bool) error {
	deployers := slices.Collect(maps.Values(r.deployers))
	if len(deployers) == 0 {
		deployers = []servingDeployer{&argoCD{Client: r.Client, namespace: r.InstallNamespace}}
	}
	for _, d := range deployers {
		removed, err := d.Remove(ctx, serving, keep)
		if keep != nil && r.Recorder != nil {
			for _, c := range removed {
				r.Recorder.Eventf(serving, nil, corev1.EventTypeNormal, "Undeployed", "Undeploy",
					"Stopped deploying to cluster %s; what runs there keeps running", c)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// bundledPull returns an image the kubelet pulls from the bundled registry
// to run preparation, "" when it pulls none from there.
func (r *ServingReconciler) bundledPull(preparation *deliveryv1alpha1.Preparation) string {
	for _, img := range preparation.Spec.Images {
		pulled := cmp.Or(img.Vendored, img.Name, img.Ref)
		if r.BundledRegistryHost != "" && strings.HasPrefix(strings.TrimPrefix(pulled, "oci://"), r.BundledRegistryHost+"/") {
			return pulled
		}
	}
	return ""
}

// applyTargets keeps the deployment of every target that is not held
// pointed at the deployed Preparation, and stops deploying to the clusters
// that are no target any more. It returns what the engine reports on each
// target, keyed by cluster; a target the engine does not deploy to gets
// held with the engine's reason.
func (r *ServingReconciler) applyTargets(
	ctx context.Context, deployer servingDeployer, env *deliveryv1alpha1.Environment,
	serving *deliveryv1alpha1.Serving, deployed *promotion.Entry, targets []deployTarget,
) (map[string]targetState, error) {
	states := map[string]targetState{}
	keep := map[string]bool{}
	for i := range targets {
		target := &targets[i]
		keep[target.cluster] = true
		if target.holdReason != "" {
			continue
		}
		state, err := deployer.Apply(ctx, env, serving, deployed, *target)
		if err != nil {
			return nil, err
		}
		if state.held != "" {
			// Holds until the engine reports a change, which its watch
			// passes on, or the spec changes.
			target.holdReason, target.holdMessage = state.held, state.message
			continue
		}
		states[target.cluster] = state
	}
	return states, r.remove(ctx, serving, keep)
}

// maxListedFailures is how many failing clusters the Ready message of a
// Serving describes; it counts the others.
const maxListedFailures = 5

// maxListedTargets is how many targets that do not run the deployed
// Preparation healthily a Serving's status lists; it counts them all.
const maxListedTargets = 64

// maxTargetMessageLength is the most a target's Ready message holds, so that
// the status of a Serving with many targets stays small.
const maxTargetMessageLength = 1024

// setTargets counts the targets of the Serving by the state of their
// deployment of the deployed Preparation, and records each target that does
// not run it healthily with what the engine reports there, keeping the
// transition times of the targets it recorded before: held targets first,
// then degraded, then progressing ones, at most maxListedTargets.
func setTargets(serving *deliveryv1alpha1.Serving, deployed *promotion.Entry, targets []deployTarget, states map[string]targetState) {
	previous := map[string]deliveryv1alpha1.DeploymentTarget{}
	for _, t := range serving.Status.Targets {
		previous[t.Cluster] = t
	}
	counts := &deliveryv1alpha1.DeploymentClusters{Selected: int32(len(targets))}
	listed := make([]deliveryv1alpha1.DeploymentTarget, 0, len(targets))
	for _, target := range targets {
		entry := deliveryv1alpha1.DeploymentTarget{Cluster: target.cluster}
		entry.Conditions = slices.Clone(previous[target.cluster].Conditions)
		ready := targetConditions{&entry, serving.Generation}
		state, applied := states[target.cluster]
		atTarget := applied && state.revision == deployed.Digest
		if applied {
			entry.Revision = state.revision
		}
		switch {
		case target.holdReason != "":
			counts.Held++
			conditions.MarkFalse(ready, deliveryv1alpha1.ConditionTypeReady, target.holdReason,
				conditions.Truncate(target.holdMessage, maxTargetMessageLength))
		case atTarget && state.health == promotion.HealthDegraded:
			counts.Degraded++
			conditions.MarkFalse(ready, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDegraded,
				conditions.Truncate(cmp.Or(state.message, "The engine reports the deployment degraded"), maxTargetMessageLength))
		case atTarget && state.health == promotion.HealthHealthy:
			counts.Healthy++
			continue
		default:
			counts.Progressing++
			conditions.MarkUnknown(ready, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProgressing,
				"Deploying Preparation "+deployed.Preparation)
		}
		listed = append(listed, entry)
	}
	slices.SortStableFunc(listed, func(a, b deliveryv1alpha1.DeploymentTarget) int {
		return cmp.Compare(targetRank(a), targetRank(b))
	})
	serving.Status.Clusters = counts
	serving.Status.Targets = listed[:min(len(listed), maxListedTargets)]
}

// targetRank orders the targets a Serving lists: held, then degraded, then
// progressing.
func targetRank(t deliveryv1alpha1.DeploymentTarget) int {
	ready := meta.FindStatusCondition(t.Conditions, deliveryv1alpha1.ConditionTypeReady)
	switch {
	case ready == nil || ready.Status == metav1.ConditionUnknown:
		return 2
	case ready.Reason == deliveryv1alpha1.ReasonDegraded:
		return 1
	default:
		return 0
	}
}

// targetConditions sets the conditions of a target of a Serving, observed at
// the Serving's generation.
type targetConditions struct {
	*deliveryv1alpha1.DeploymentTarget
	generation int64
}

func (t targetConditions) GetGeneration() int64              { return t.generation }
func (t targetConditions) GetConditions() []metav1.Condition { return t.Conditions }
func (t targetConditions) SetConditions(c []metav1.Condition) {
	t.Conditions = c
}

// targetPreparation returns the Order of the Serving and the Preparation
// the Serving converges to, nil while there is none, with what it waits for
// then. The promotion mode is read from the Order: Manual serves
// spec.preparationName, Automatic the newest Ready Preparation of the Order.
// The Serving spec is never modified.
func (r *ServingReconciler) targetPreparation(
	ctx context.Context, serving *deliveryv1alpha1.Serving,
) (*deliveryv1alpha1.Order, *deliveryv1alpha1.Preparation, string, error) {
	order := &deliveryv1alpha1.Order{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: serving.Spec.OrderName}, order); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, nil, "", fmt.Errorf("failed to get Order: %w", err)
		}
		return nil, nil, fmt.Sprintf("Waiting for Order %q", serving.Spec.OrderName), nil
	}

	if order.EffectivePromotionMode() != deliveryv1alpha1.PromotionModeAutomatic {
		if serving.Spec.PreparationName == "" {
			return order, nil, "Waiting for a Preparation to be promoted", nil
		}
		preparation := &deliveryv1alpha1.Preparation{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: serving.Spec.PreparationName}, preparation); err != nil {
			if !apierrors.IsNotFound(err) {
				return order, nil, "", fmt.Errorf("failed to get Preparation %q: %w", serving.Spec.PreparationName, err)
			}
			// The Preparation watch reconciles again once it is created.
			return order, nil, fmt.Sprintf("Waiting for Preparation %q", serving.Spec.PreparationName), nil
		}
		return order, preparation, "", nil
	}

	preparations := &deliveryv1alpha1.PreparationList{}
	if err := r.List(ctx, preparations,
		client.InNamespace(serving.Namespace),
		client.MatchingFields{deliveryv1alpha1.FieldOrderName: serving.Spec.OrderName},
	); err != nil {
		return order, nil, "", fmt.Errorf("failed to list Preparations: %w", err)
	}

	// The newest Ready Preparation, picked as the scanner keeps it scanned.
	releases := make([]promotion.Release, 0, len(preparations.Items))
	for i := range preparations.Items {
		prep := &preparations.Items[i]
		releases = append(releases, promotion.Release{
			Name:    prep.Name,
			Created: prep.CreationTimestamp.Time,
			Ready:   meta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady),
		})
	}
	newest, ok := promotion.NewestReady(releases)
	if !ok {
		// The Preparation watch reconciles again once one is ready.
		msg := "Waiting for ready preparation"
		if len(preparations.Items) == 0 {
			msg = "Waiting for preparations"
		}
		return order, nil, msg, nil
	}
	i := slices.IndexFunc(preparations.Items, func(p deliveryv1alpha1.Preparation) bool { return p.Name == newest })
	return order, &preparations.Items[i], "", nil
}

// gate reports whether preparation passes its approval gate, under both
// promotion modes, and records it in the Approved condition. As the target,
// which it becomes once it passes the gates before, its approved votes are
// sealed, and it passes once they are archived.
func (r *ServingReconciler) gate(
	ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation, target bool,
) (bool, error) {
	approvals := &deliveryv1alpha1.ApprovalList{}
	if err := r.List(ctx, approvals,
		client.InNamespace(preparation.Namespace),
		client.MatchingFields{deliveryv1alpha1.FieldPreparationRefName: preparation.Name},
	); err != nil {
		return false, fmt.Errorf("failed to list Approvals: %w", err)
	}

	if !target {
		// Only the target gets its votes sealed: a Preparation that was the
		// target before and no longer passes is not.
		serving.Status.TargetPreparationName = ""
		res := approval.Evaluate(preparation, approvals.Items)
		status := metav1.ConditionFalse
		if res.Approved {
			status = metav1.ConditionTrue
		}
		conditions.Set(serving, deliveryv1alpha1.ConditionTypeApproved, status, res.Reason, preparation.Name+": "+res.Message)
		return res.Approved, nil
	}

	gate := approval.Gate(preparation, approvals.Items)
	if gate.Blocked {
		log.FromContext(ctx).V(4).Info("Approval gate blocks deployment", "preparation", preparation.Name, "reason", gate.Reason)
	}
	serving.Status.TargetPreparationName = preparation.Name
	conditions.Set(serving, deliveryv1alpha1.ConditionTypeApproved, gate.Status, gate.Reason, gate.Message)
	return !gate.Blocked, nil
}

// upstreamReasons maps the outcome of the upstream gate to the reason of the
// UpstreamDeployed condition.
var upstreamReasons = map[promotion.Outcome]string{
	promotion.OutcomeSatisfied:           deliveryv1alpha1.ReasonSatisfied,
	promotion.OutcomeSoaking:             deliveryv1alpha1.ReasonSoaking,
	promotion.OutcomeNotDeployedUpstream: deliveryv1alpha1.ReasonNotDeployedUpstream,
	promotion.OutcomeInvalidPipeline:     deliveryv1alpha1.ReasonInvalidPipeline,
}

// upstream evaluates the upstream gate of preparation: the stages it names
// must include an Order of every Environment the Order's Environment
// requires, every upstream stage must have deployed its own Preparation of
// the same Product revision healthily for its soak time, as the stage's
// Serving records it, and the deployment record that qualifies it must
// verify in the registry. A Preparation the Serving promoted before passes.
// It records the result in the UpstreamDeployed condition.
func (r *ServingReconciler) upstream(
	ctx context.Context, serving *deliveryv1alpha1.Serving, order *deliveryv1alpha1.Order,
	preparation *deliveryv1alpha1.Preparation, history promotion.History,
) (promotion.Result, error) {
	stages := stagesOf(preparation)
	var requirements []promotion.Requirement
	if history.PromotedBefore(preparation.Name) == nil {
		var err error
		if requirements, err = r.requirements(ctx, order); err != nil {
			return promotion.Result{}, err
		}
	}
	if len(stages) == 0 && len(requirements) == 0 {
		conditions.Remove(serving, deliveryv1alpha1.ConditionTypeUpstreamDeployed)
		return promotion.Result{Outcome: promotion.OutcomeSatisfied}, nil
	}

	req := promotion.Request{
		Preparation: preparation.Name,
		Product:     order.Spec.ProductRef.Name,
		Revision:    preparation.Spec.ProductRevision,
		Stages:      stages,
		Self:        history,
		Upstreams:   map[string]promotion.Upstream{},
		Now:         r.clock(),
	}
	environments := map[string]string{}
	for _, stage := range stages {
		upstreamOrder := &deliveryv1alpha1.Order{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: stage.Order}, upstreamOrder); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return promotion.Result{}, fmt.Errorf("failed to get upstream Order %q: %w", stage.Order, err)
		}
		environments[stage.Order] = upstreamOrder.Spec.EnvironmentRef.Name
		upstream := promotion.Upstream{Product: upstreamOrder.Spec.ProductRef.Name}
		upstreamServing := &deliveryv1alpha1.Serving{}
		switch err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: stage.Order}, upstreamServing); {
		case err == nil:
			upstream.History = historyOf(upstreamServing)
		case !apierrors.IsNotFound(err):
			return promotion.Result{}, fmt.Errorf("failed to get the Serving of upstream Order %q: %w", stage.Order, err)
		}
		req.Upstreams[stage.Order] = upstream
	}

	if msg := promotion.Required(stages, environments, requirements); msg != "" {
		conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeUpstreamDeployed, deliveryv1alpha1.ReasonPipelineRequired,
			preparation.Name+": "+msg)
		return promotion.Result{Outcome: promotion.OutcomeInvalidPipeline, Message: msg}, nil
	}

	result := promotion.Evaluate(req)
	if !result.Satisfied() {
		log.FromContext(ctx).V(4).Info("Upstream stages hold deployment", "preparation", preparation.Name, "outcome", result.Outcome)
		conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeUpstreamDeployed, upstreamReasons[result.Outcome],
			preparation.Name+": "+result.Message)
		return result, nil
	}

	for _, qualified := range result.Qualified {
		reason, err := r.verifyDeployment(ctx, serving.Namespace, qualified)
		if err == nil {
			continue
		}
		msg := fmt.Sprintf("%s: the deployment record %s of Order %s did not verify: %v",
			preparation.Name, qualified.Entry.DeploymentRecord, qualified.Stage.Order, err)
		held := promotion.Result{Outcome: promotion.OutcomeNotDeployedUpstream, Message: msg}
		if reason == "" {
			// A record the registry cannot serve is retried.
			conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeUpstreamDeployed, deliveryv1alpha1.ReasonSignatureMissing, msg)
			return held, err
		}
		conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeUpstreamDeployed, reason, msg)
		return held, nil
	}

	conditions.MarkTrue(serving, deliveryv1alpha1.ConditionTypeUpstreamDeployed, deliveryv1alpha1.ReasonSatisfied,
		preparation.Name+": "+result.Message)
	return result, nil
}

// requirements returns the Environments the Environment of order requires
// its stages to run after, none when it does not exist, which deploy
// reports.
func (r *ServingReconciler) requirements(ctx context.Context, order *deliveryv1alpha1.Order) ([]promotion.Requirement, error) {
	env := &deliveryv1alpha1.Environment{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: order.Spec.EnvironmentRef.Name}, env); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if env.Spec.Promotion == nil {
		return nil, nil
	}
	requirements := make([]promotion.Requirement, 0, len(env.Spec.Promotion.RequireAfter))
	for _, required := range env.Spec.Promotion.RequireAfter {
		requirements = append(requirements, promotion.Requirement{Environment: required.Environment, MinSoakSeconds: required.MinSoakSeconds})
	}
	return requirements, nil
}

// verifyDeployment verifies, in the registry, the deployment record of the
// upstream deployment that qualifies a revision. For a record that does not
// verify it returns the condition reason that describes it, with why; for a
// registry it could not read, only the error.
func (r *ServingReconciler) verifyDeployment(ctx context.Context, namespace string, qualified promotion.Qualified) (string, error) {
	state, err := r.Signing.State(ctx)
	if err != nil {
		// The provider notifies the watch once the key is loaded.
		return deliveryv1alpha1.ReasonSigningKeyNotReady, err
	}
	e := qualified.Entry
	err = r.records.Verify(ctx, state.Trust, namespace+"/"+qualified.Stage.Order, e, func(ctx context.Context) (oci.Client, error) {
		return r.connect(ctx, namespace, qualified.Stage.Order, registryRef(e.Registry), e.OCIRef)
	})
	switch {
	case err == nil:
		return "", nil
	case errors.Is(err, promotion.ErrRecordInvalid):
		return deliveryv1alpha1.ReasonSignatureInvalid, err
	default:
		return registry.Reason(err, ""), err
	}
}

// verify checks the evidence of preparation's artifact in the registry
// before it is deployed: its signature, its provenance for the Serving's
// Order, and, when an approval policy applies, its sealed approvals. It
// reports whether the evidence verified.
func (r *ServingReconciler) verify(ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation) (bool, error) {
	logger := log.FromContext(ctx)

	state, err := r.Signing.State(ctx)
	if err != nil {
		// The provider notifies the watch once the key is loaded.
		logger.Info("Waiting for the signing key to verify Preparation", "preparation", preparation.Name, "reason", err.Error())
		conditions.MarkUnknown(serving, deliveryv1alpha1.ConditionTypeVerified, deliveryv1alpha1.ReasonSigningKeyNotReady,
			preparation.Name+": waiting for the signing key: "+err.Error())
		return false, nil
	}

	artifact := preparation.Spec.Artifact
	err = r.artifacts.Verify(ctx, state.Trust, preparation, serving.Namespace+"/"+serving.Spec.OrderName,
		func(ctx context.Context) (oci.Client, error) {
			return r.connect(ctx, preparation.Namespace, preparation.Spec.OrderName, artifact.RegistryRef, artifact.OCIRef)
		})
	var rejected *evidence.Rejected
	switch {
	case errors.As(err, &rejected):
		logger.Info("Evidence of Preparation did not verify, keeping the current deployment",
			"preparation", preparation.Name, "reason", rejected.Reason, "error", err.Error())
		conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeVerified, rejected.Reason, preparation.Name+": "+err.Error())
		return false, nil
	case registry.Reason(err, "") != "":
		// The Registry watch reconciles again once the Registries
		// change.
		conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeVerified, registry.Reason(err, ""), preparation.Name+": "+err.Error())
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to verify Preparation %q: %w", preparation.Name, err)
	}

	conditions.MarkTrue(serving, deliveryv1alpha1.ConditionTypeVerified, deliveryv1alpha1.ReasonSignaturesVerified,
		preparation.Name+": signature and attestations verified")
	return true, nil
}

// checkReasons maps the outcome of a vulnerability check to the reason of
// the ChecksPassed condition.
var checkReasons = map[vulnerability.Outcome]string{
	vulnerability.OutcomePassed:               deliveryv1alpha1.ReasonPassed,
	vulnerability.OutcomeVulnerabilitiesFound: deliveryv1alpha1.ReasonVulnerabilitiesFound,
	vulnerability.OutcomeScanMissing:          deliveryv1alpha1.ReasonScanMissing,
	vulnerability.OutcomeScanStale:            deliveryv1alpha1.ReasonScanStale,
}

// checksVerdict is the result of the promotion checks of a Preparation.
type checksVerdict struct {
	passed bool
	// recheck is how soon to check again, as the scans age.
	recheck time.Duration
	// evidence names the reports a Preparation that passes passed with.
	evidence *promotion.ChecksEvidence
}

// checks evaluates the promotion checks preparation was rendered under
// against the verified scan reports of its images, and records the result
// in the ChecksPassed condition.
func (r *ServingReconciler) checks(
	ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation,
) (checksVerdict, error) {
	var check *deliveryv1alpha1.VulnerabilityCheck
	if checks := preparation.PromotionChecks(); checks != nil {
		check = checks.Vulnerabilities
	}
	if check == nil {
		conditions.Remove(serving, deliveryv1alpha1.ConditionTypeChecksPassed)
		return checksVerdict{passed: true}, nil
	}

	verdict, assessments, err := r.evaluateVulnerabilities(ctx, serving, preparation, check)
	if err != nil {
		return checksVerdict{}, err
	}
	reason := checkReasons[verdict.Outcome]
	if verdict.Outcome != vulnerability.OutcomePassed {
		log.FromContext(ctx).V(4).Info("Checks block deployment", "preparation", preparation.Name, "reason", reason)
		conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeChecksPassed, reason, preparation.Name+": "+verdict.Message)
		return checksVerdict{}, nil
	}

	conditions.MarkTrue(serving, deliveryv1alpha1.ConditionTypeChecksPassed, reason, preparation.Name+": "+verdict.Message)
	scanned := promotion.VulnerabilitiesEvidenceOf(string(check.MaxSeverity), assessments)
	return checksVerdict{passed: true, recheck: verdict.Recheck, evidence: &promotion.ChecksEvidence{Vulnerabilities: scanned}}, nil
}

// evaluateVulnerabilities evaluates check against the latest scan of every
// image of preparation, as the Product records it and the signed report in
// the registry holds it. It returns the images it assessed.
func (r *ServingReconciler) evaluateVulnerabilities(
	ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation, check *deliveryv1alpha1.VulnerabilityCheck,
) (vulnerability.Verdict, []vulnerability.Assessment, error) {
	product, err := r.productOf(ctx, serving)
	if err != nil {
		return vulnerability.Verdict{}, nil, err
	}
	if product == nil {
		return vulnerability.Verdict{Outcome: vulnerability.OutcomeScanMissing, Message: "the Order or its Product was not found"}, nil, nil
	}
	interval, err := r.scanInterval(ctx)
	if err != nil {
		return vulnerability.Verdict{}, nil, err
	}

	assessments := vulnerability.Read(ctx, releaseImagesOf(preparation, product), r.reports.Reader(r.reportAccess(product)), 1)
	verdict, err := vulnerability.Evaluate(assessments, vulnerability.Check{
		MaxSeverity:   vulnerability.Severity(check.MaxSeverity),
		IgnoreUnfixed: check.IgnoreUnfixed,
		MaxAge:        vulnerability.MaxAge(time.Duration(check.MaxAgeSeconds)*time.Second, interval),
	}, r.clock())
	return verdict, assessments, err
}

// reportAccess returns the access to the signed reports of the Product's
// scans: through its Registries, verified against the Kitchen's keys.
// Registries that cannot be used leave the images without scans.
func (r *ServingReconciler) reportAccess(product *deliveryv1alpha1.Product) func(context.Context) (vulnerability.Access, error) {
	return func(ctx context.Context) (vulnerability.Access, error) {
		state, err := r.Signing.State(ctx)
		if err != nil {
			return vulnerability.Access{}, fmt.Errorf("waiting for the signing key to verify the reports: %w", err)
		}
		scope, err := r.Registries.Scope(ctx, product.Namespace, product.Spec.RegistryRefs)
		if err != nil {
			return vulnerability.Access{}, fmt.Errorf("connecting to the vendored images: %w", err)
		}
		return vulnerability.Access{Trust: state.Trust, Connect: func(repository string) (oci.Client, error) {
			conn, err := scope.Connect(repository)
			if registry.Reason(err, "") != "" {
				return nil, vulnerability.Unreadable(err)
			}
			return conn.Client, err
		}}, nil
	}
}

// recordDeployedVulnerabilities records the summary of the scans of the
// deployed Preparation once it was healthy, and emits a Warning Event when
// the findings at or above the severity its check allows increase while it
// stays deployed. Until it is healthy, the summary keeps describing what ran
// before.
func (r *ServingReconciler) recordDeployedVulnerabilities(ctx context.Context, serving *deliveryv1alpha1.Serving, deployed *promotion.Entry) error {
	if deployed.Healthy.IsZero() {
		return nil
	}

	preparation := &deliveryv1alpha1.Preparation{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: deployed.Preparation}, preparation); err != nil {
		return client.IgnoreNotFound(err)
	}
	product, err := r.productOf(ctx, serving)
	if err != nil {
		return err
	}
	var total vulnerability.Summary
	scanned := false
	if product != nil {
		total, scanned = vulnerability.Total(releaseImagesOf(preparation, product))
	}
	previous := serving.Status.DeployedVulnerabilities
	serving.Status.DeployedVulnerabilities = nil
	if scanned {
		serving.Status.DeployedVulnerabilities = &deliveryv1alpha1.VulnerabilitySummary{
			Critical: total.Critical, High: total.High, Medium: total.Medium,
			Low: total.Low, Unknown: total.Unknown, Fixable: total.Fixable,
		}
	}

	checks := preparation.PromotionChecks()
	if previous == nil || !scanned || checks == nil || checks.Vulnerabilities == nil || r.Recorder == nil {
		return nil
	}
	maxSeverity := vulnerability.Severity(checks.Vulnerabilities.MaxSeverity)
	was := vulnerability.Summary{Critical: previous.Critical, High: previous.High, Medium: previous.Medium, Low: previous.Low}
	if before, after, increased := vulnerability.Increased(was, total, maxSeverity); increased {
		r.Recorder.Eventf(serving, preparation, corev1.EventTypeWarning, deliveryv1alpha1.ReasonVulnerabilitiesIncreased, "Scan",
			"Deployed Preparation %s has %d vulnerabilities at or above %s, up from %d", preparation.Name, after, maxSeverity, before)
	}
	return nil
}

// recordPromotion records the promotion of the pending entry of history,
// the Serving's promotion of candidate, in the registry: a signed statement
// of the evidence of every gate it passed, attached to its artifact. It
// returns the digest of the record.
func (r *ServingReconciler) recordPromotion(
	ctx context.Context, serving *deliveryv1alpha1.Serving, order *deliveryv1alpha1.Order, candidate *deliveryv1alpha1.Preparation,
	history promotion.History, upstream promotion.Result, checks checksVerdict,
) (string, error) {
	state, err := r.Signing.State(ctx)
	if err != nil {
		return "", fmt.Errorf("waiting for the signing key to record the promotion: %w", err)
	}
	gates := promotion.Gates{After: promotion.StageEvidenceOf(serving.Namespace, stagesOf(candidate), upstream), Checks: checks.evidence}
	if a := candidate.Status.Approval; candidate.ApprovalPolicy() != nil && a != nil && a.Attestation != nil {
		gates.Approvals = &promotion.ApprovalsEvidence{Attestation: a.Attestation.Digest}
	}
	registryClient, err := r.connect(ctx, serving.Namespace, serving.Spec.OrderName, candidate.Spec.Artifact.RegistryRef, candidate.Spec.Artifact.OCIRef)
	if err != nil {
		return "", err
	}
	return promotion.PushPromotion(ctx, registryClient, state.Signer, serving.Namespace+"/"+serving.Spec.OrderName,
		string(order.EffectivePromotionMode()), history, gates)
}

// recordDeployments records every deployment of history that was healthy
// and is not recorded yet in the registry: a signed statement that it was
// healthy, on the clusters that run it healthily, attached to its artifact.
func (r *ServingReconciler) recordDeployments(
	ctx context.Context, serving *deliveryv1alpha1.Serving, history promotion.History, deployedTo []deployTarget, states map[string]targetState,
) error {
	unrecorded := history.Unrecorded()
	if len(unrecorded) == 0 {
		return nil
	}
	state, err := r.Signing.State(ctx)
	if err != nil {
		return fmt.Errorf("waiting for the signing key to record the deployment: %w", err)
	}
	for _, e := range unrecorded {
		// Every cluster runs the Preparation in its target namespace. A
		// Preparation that is gone leaves nothing to attach the record to.
		preparation := &deliveryv1alpha1.Preparation{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: e.Preparation}, preparation); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed to get Preparation %q: %w", e.Preparation, err)
		}
		targets := make([]promotion.Target, 0, len(deployedTo))
		for _, t := range deployedTo {
			if state, ok := states[t.cluster]; ok && t.holdReason == "" && state.revision == e.Digest && state.health == promotion.HealthHealthy {
				targets = append(targets, promotion.Target{Cluster: t.cluster, Server: t.server, Namespace: preparation.Spec.TargetNamespace})
			}
		}
		registryClient, err := r.connect(ctx, serving.Namespace, serving.Spec.OrderName, registryRef(e.Registry), e.OCIRef)
		if err != nil {
			return err
		}
		record, err := promotion.PushDeployment(ctx, registryClient, state.Signer, serving.Namespace+"/"+serving.Spec.OrderName, e, targets)
		if err != nil {
			return err
		}
		history = history.RecordDeployment(e.Digest, e.Healthy, record)
		log.FromContext(ctx).Info("Recorded deployment", "preparation", e.Preparation, "record", record)
	}
	setHistory(serving, history)
	return nil
}

// releaseImagesOf maps the images of preparation, with the latest scans the
// Product records of their digests, to the images a check evaluates.
func releaseImagesOf(preparation *deliveryv1alpha1.Preparation, product *deliveryv1alpha1.Product) []vulnerability.Image {
	scans := map[string]deliveryv1alpha1.ImageScan{}
	if product.Status.Vulnerabilities != nil {
		for _, s := range product.Status.Vulnerabilities.Images {
			scans[s.Digest] = s
		}
	}
	images := make([]vulnerability.Image, 0, len(preparation.Spec.Images))
	for _, img := range preparation.Spec.Images {
		image := vulnerability.Image{Name: cmp.Or(img.Name, img.Ref), Digest: img.Digest, Vendored: img.Vendored != ""}
		if s, ok := scans[img.Digest]; ok {
			image.Scan = &vulnerability.Scan{
				Repository: s.Repository,
				Digest:     s.Digest,
				Report:     s.Report,
				ScannedAt:  s.ScannedTime.Time,
				Summary: vulnerability.Summary{
					Critical: s.Summary.Critical, High: s.Summary.High, Medium: s.Summary.Medium,
					Low: s.Summary.Low, Unknown: s.Summary.Unknown, Fixable: s.Summary.Fixable,
				},
			}
		}
		images = append(images, image)
	}
	return images
}

// productOf returns the Product of the Serving's Order, nil when either
// does not exist.
func (r *ServingReconciler) productOf(ctx context.Context, serving *deliveryv1alpha1.Serving) (*deliveryv1alpha1.Product, error) {
	order := &deliveryv1alpha1.Order{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: serving.Spec.OrderName}, order); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get Order: %w", err)
	}
	product := &deliveryv1alpha1.Product{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: order.Spec.ProductRef.Name}, product); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get Product: %w", err)
	}
	return product, nil
}

// scanInterval returns the Kitchen's scanning interval, its default without
// a Kitchen.
func (r *ServingReconciler) scanInterval(ctx context.Context) (time.Duration, error) {
	var scanning *deliveryv1alpha1.KitchenScanning
	kitchen := &deliveryv1alpha1.Kitchen{}
	switch err := r.Get(ctx, client.ObjectKey{Namespace: r.InstallNamespace, Name: deliveryv1alpha1.DefaultKitchenName}, kitchen); {
	case err == nil:
		scanning = kitchen.Spec.Scanning
	case !apierrors.IsNotFound(err):
		return 0, fmt.Errorf("failed to get Kitchen: %w", err)
	}
	return time.Duration(scanning.EffectiveIntervalSeconds()) * time.Second, nil
}

// clock returns the current time.
func (r *ServingReconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// reconcileDelete stops the Serving's deployments on every cluster, which
// garbage collection cannot reach in the engines' namespaces, then removes
// the finalizer. What runs there keeps running.
func (r *ServingReconciler) reconcileDelete(ctx context.Context, serving *deliveryv1alpha1.Serving) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Serving")

	if controllerutil.ContainsFinalizer(serving, deliveryv1alpha1.Finalizer) {
		logger.Info("Cleaning up Serving resources")

		if err := r.remove(ctx, serving, nil); err != nil {
			return ctrl.Result{}, err
		}

		controllerutil.RemoveFinalizer(serving, deliveryv1alpha1.Finalizer)

		// Deleting an Order deletes its Serving, Preparations and Application
		// at once, and their events can reconcile a Serving whose finalizer is
		// gone again from a cache that still holds it.
		if err := r.Update(ctx, serving); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	return ctrl.Result{}, nil
}

// connect returns a client for the registry holding the artifact at ociRef,
// rendered for the Order orderName: through the Registry recorded with it,
// while it exists and still covers the artifact, and through the Order's
// registryRefs otherwise.
func (r *ServingReconciler) connect(
	ctx context.Context, namespace, orderName string, recorded *corev1.LocalObjectReference, ociRef string,
) (oci.Client, error) {
	conn, err := r.Registries.ConnectRecorded(ctx, namespace, recorded, ociRef,
		func(ctx context.Context) ([]corev1.LocalObjectReference, error) {
			order := &deliveryv1alpha1.Order{}
			if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: orderName}, order); err != nil && !apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("failed to get Order %q: %w", orderName, err)
			}
			return order.Spec.RegistryRefs, nil
		})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", ociRef, err)
	}
	return conn.Client, nil
}

// patchStatus writes the status serving took on in this reconcile, unless it
// is unchanged, and returns how to requeue: as the reconcile decided, or soon
// when the patch conflicts. The patch is locked to the version this reconcile
// read or last wrote, so that a reconcile from a stale cache never overwrites
// the recorded gate verdict.
func (r *ServingReconciler) patchStatus(
	ctx context.Context, serving *deliveryv1alpha1.Serving, before *deliveryv1alpha1.ServingStatus, result ctrl.Result, err error,
) (ctrl.Result, error) {
	serving.Status.ObservedGeneration = serving.Generation
	if equality.Semantic.DeepEqual(*before, serving.Status) {
		return result, err
	}

	base := serving.DeepCopy()
	base.Status = *before
	perr := r.Status().Patch(ctx, serving, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	switch {
	case perr == nil, apierrors.IsNotFound(perr):
		return result, err
	case apierrors.IsConflict(perr) && err == nil:
		log.FromContext(ctx).V(1).Info("Serving changed during the reconcile, reconciling it again")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	default:
		return ctrl.Result{}, errors.Join(err, fmt.Errorf("failed to patch Serving status: %w", perr))
	}
}

// markDeployment derives the Ready condition of the Serving from its
// targets: Deployed once the deployment is healthy, failed when a target is
// degraded or held for another reason than its Cluster not being Ready, or
// when no Cluster is Ready, and Deploying until then. Clusters that are not
// Ready are named, and do not hold the Serving back. The health of another
// revision says nothing about the deployed Preparation. An unchanged state
// changes nothing, so a Serving that stays degraded or progressing settles
// until a deployment changes.
func markDeployment(serving *deliveryv1alpha1.Serving, deployed *promotion.Entry, targets []deployTarget, observation promotion.Observation) {
	counts := serving.Status.Clusters
	where := ""
	if len(targets) > 1 || (len(targets) == 1 && targets[0].cluster != cluster.InCluster) {
		names := make([]string, 0, len(targets))
		for _, t := range targets {
			if t.holdReason != deliveryv1alpha1.ReasonClusterNotReady {
				names = append(names, t.cluster)
			}
		}
		where = " to " + conditions.List(names, ", ", maxListedClusters)
	}
	var unready []string
	for _, t := range targets {
		if t.holdReason == deliveryv1alpha1.ReasonClusterNotReady {
			unready = append(unready, t.cluster)
		}
	}
	if len(unready) > 0 {
		where += "; not ready: " + conditions.List(unready, ", ", maxListedClusters)
	}

	failing := int(counts.Held) - len(unready) + int(counts.Degraded)
	switch {
	case failing > 0:
		var failed []string
		for _, target := range serving.Status.Targets {
			ready := meta.FindStatusCondition(target.Conditions, deliveryv1alpha1.ConditionTypeReady)
			if ready != nil && ready.Status == metav1.ConditionFalse && ready.Reason != deliveryv1alpha1.ReasonClusterNotReady {
				failed = append(failed, target.Cluster+": "+ready.Message)
			}
		}
		listed := failed[:min(len(failed), maxListedFailures)]
		msg := "deployment of Preparation " + deployed.Preparation + " failed on " + strings.Join(listed, "; ")
		if counts.Selected > 1 {
			msg = fmt.Sprintf("deployment of Preparation %s failed on %d of %d clusters: %s",
				deployed.Preparation, failing, counts.Selected, strings.Join(listed, "; "))
		}
		if more := failing - len(listed); more > 0 {
			msg += fmt.Sprintf("; and %d more", more)
		}
		markDeploymentFailed(serving, msg)
	case len(unready) == len(targets):
		markDeploymentFailed(serving, "no Cluster of the Environment is ready: "+conditions.List(unready, ", ", maxListedClusters))
	case observation.Health == promotion.HealthHealthy:
		conditions.MarkTrue(serving, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeployed, "Deployed Preparation "+deployed.Preparation+where)
	default:
		conditions.MarkUnknown(serving, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeploying, "Deploying Preparation "+deployed.Preparation+where)
	}
}

// markPending marks the Serving as waiting for a prerequisite.
func markPending(serving *deliveryv1alpha1.Serving, msg string) {
	conditions.MarkUnknown(serving, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonPending, msg)
}

// markHeld adds held, what holds the Preparation the Serving converges to,
// to the Ready condition this pass set. Only a fresh condition gets it: the
// one an earlier pass left carries it already.
func markHeld(serving *deliveryv1alpha1.Serving, held string) {
	if held == "" {
		return
	}
	if ready := meta.FindStatusCondition(serving.Status.Conditions, deliveryv1alpha1.ConditionTypeReady); ready != nil {
		conditions.Set(serving, ready.Type, ready.Status, ready.Reason, ready.Message+". "+held)
	}
}

// markDeploymentFailed marks the Serving as not deployed, for msg.
func markDeploymentFailed(serving *deliveryv1alpha1.Serving, msg string) {
	conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeploymentFailed, msg)
}

// conditionMessage returns the message of the condition of conditionType on
// serving.
func conditionMessage(serving *deliveryv1alpha1.Serving, conditionType string) string {
	if c := meta.FindStatusCondition(serving.Status.Conditions, conditionType); c != nil {
		return c.Message
	}
	return ""
}

// soonest returns the shortest of the waits that are set, 0 when none is.
func soonest(waits ...time.Duration) time.Duration {
	var shortest time.Duration
	for _, wait := range waits {
		if wait > 0 && (shortest == 0 || wait < shortest) {
			shortest = wait
		}
	}
	return shortest
}

// historyOf maps the history of serving to its domain form.
func historyOf(serving *deliveryv1alpha1.Serving) promotion.History {
	if len(serving.Status.History) == 0 {
		return nil
	}
	history := make(promotion.History, 0, len(serving.Status.History))
	for _, e := range serving.Status.History {
		entry := promotion.Entry{
			Preparation:      e.PreparationName,
			Digest:           e.Digest,
			OCIRef:           e.OCIRef,
			ProductRevision:  e.ProductRevision,
			Started:          e.StartedTime.UTC(),
			EndReason:        promotion.EndReason(e.EndReason),
			PromotionRecord:  e.PromotionRecord,
			DeploymentRecord: e.DeploymentRecord,
		}
		if e.RegistryRef != nil {
			entry.Registry = e.RegistryRef.Name
		}
		if e.HealthyTime != nil {
			entry.Healthy = e.HealthyTime.UTC()
		}
		if e.EndedTime != nil {
			entry.Ended = e.EndedTime.UTC()
		}
		history = append(history, entry)
	}
	return history
}

// setHistory records history as the history of serving.
func setHistory(serving *deliveryv1alpha1.Serving, history promotion.History) {
	if len(history) == 0 {
		serving.Status.History = nil
		return
	}
	entries := make([]deliveryv1alpha1.DeploymentEntry, 0, len(history))
	for _, e := range history {
		entry := deliveryv1alpha1.DeploymentEntry{
			PreparationName:  e.Preparation,
			Digest:           e.Digest,
			OCIRef:           e.OCIRef,
			RegistryRef:      registryRef(e.Registry),
			ProductRevision:  e.ProductRevision,
			StartedTime:      metav1.NewTime(e.Started),
			HealthyTime:      optionalTime(e.Healthy),
			EndedTime:        optionalTime(e.Ended),
			EndReason:        deliveryv1alpha1.DeploymentEndReason(e.EndReason),
			PromotionRecord:  e.PromotionRecord,
			DeploymentRecord: e.DeploymentRecord,
		}
		entries = append(entries, entry)
	}
	serving.Status.History = entries
}

// optionalTime returns t as an optional status time, nil when it is zero.
func optionalTime(t time.Time) *metav1.Time {
	if t.IsZero() {
		return nil
	}
	return new(metav1.NewTime(t))
}

// registryRef references the Registry name, nil when it is "".
func registryRef(name string) *corev1.LocalObjectReference {
	if name == "" {
		return nil
	}
	return &corev1.LocalObjectReference{Name: name}
}

// entryOf returns the history entry of a deployment of preparation, without
// its times and records.
func entryOf(preparation *deliveryv1alpha1.Preparation) promotion.Entry {
	entry := promotion.Entry{
		Preparation:     preparation.Name,
		Digest:          preparation.Spec.Artifact.Digest,
		OCIRef:          preparation.Spec.Artifact.OCIRef,
		ProductRevision: preparation.Spec.ProductRevision,
	}
	if ref := preparation.Spec.Artifact.RegistryRef; ref != nil {
		entry.Registry = ref.Name
	}
	return entry
}

// stagesOf returns the upstream stages preparation names.
func stagesOf(preparation *deliveryv1alpha1.Preparation) []promotion.Stage {
	upstreams := preparation.UpstreamStages()
	if len(upstreams) == 0 {
		return nil
	}
	stages := make([]promotion.Stage, 0, len(upstreams))
	for _, u := range upstreams {
		stages = append(stages, promotion.Stage{Order: u.Order, SoakSeconds: u.SoakSeconds})
	}
	return stages
}

// observationOf reduces what the engine reports on the targets to one
// observation of the deployed entry: degraded at its digest once any target
// runs it degraded, healthy at its digest once every target whose Cluster
// is Ready runs it healthily, and progressing otherwise. Clusters that are
// not Ready are left out: nothing deploys there until they are back, and
// the others, and the stages after them, do not wait for them.
func observationOf(deployed *promotion.Entry, targets []deployTarget, states map[string]targetState) promotion.Observation {
	counted, healthy := 0, 0
	for _, t := range targets {
		if t.holdReason == deliveryv1alpha1.ReasonClusterNotReady {
			continue
		}
		counted++
		state, ok := states[t.cluster]
		if t.holdReason != "" || !ok || state.revision != deployed.Digest {
			continue
		}
		switch state.health {
		case promotion.HealthDegraded:
			return promotion.Observation{Revision: deployed.Digest, Health: promotion.HealthDegraded}
		case promotion.HealthHealthy:
			healthy++
		}
	}
	if counted > 0 && healthy == counted {
		return promotion.Observation{Revision: deployed.Digest, Health: promotion.HealthHealthy}
	}
	return promotion.Observation{Health: promotion.HealthProgressing}
}

// enqueueServingForOrderName maps an Order or Preparation to the Order's
// Serving, which always carries the Order's name.
func enqueueServingForOrderName(_ context.Context, obj client.Object) []ctrl.Request {
	var orderName string
	switch o := obj.(type) {
	case *deliveryv1alpha1.Order:
		orderName = o.Name
	case *deliveryv1alpha1.Preparation:
		orderName = o.Spec.OrderName
	default:
		return nil
	}
	return []ctrl.Request{{Namespace: obj.GetNamespace(), Name: orderName}}
}

// enqueueServingsOfProduct maps a Product to the Servings of its Orders,
// which carry the Orders' names. It lists Orders by the index the Order
// controller registers.
func (r *ServingReconciler) enqueueServingsOfProduct(ctx context.Context, obj client.Object) []ctrl.Request {
	orders := &deliveryv1alpha1.OrderList{}
	if err := r.List(ctx, orders, client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{deliveryv1alpha1.FieldProductRefName: obj.GetName()}); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Orders of Product", "product", obj.GetName())
		return nil
	}
	reqs := make([]ctrl.Request, 0, len(orders.Items))
	for i := range orders.Items {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&orders.Items[i])})
	}
	return reqs
}

// enqueueDownstreamServings maps the Serving of an Order to the Servings of
// the Orders that run after it, which carry their Orders' names. It lists
// Orders by the index the Order controller registers.
func (r *ServingReconciler) enqueueDownstreamServings(ctx context.Context, obj client.Object) []ctrl.Request {
	serving, ok := obj.(*deliveryv1alpha1.Serving)
	if !ok {
		return nil
	}
	orders := &deliveryv1alpha1.OrderList{}
	if err := r.List(ctx, orders, client.InNamespace(serving.Namespace),
		client.MatchingFields{orderUpstreamsField: serving.Spec.OrderName}); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list downstream Orders", "order", serving.Spec.OrderName)
		return nil
	}
	reqs := make([]ctrl.Request, 0, len(orders.Items))
	for i := range orders.Items {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&orders.Items[i])})
	}
	return reqs
}

// historyChanged passes the Serving events that change what its downstream
// stages read from it: creates, deletes, and changes of its history.
var historyChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldServing, okOld := e.ObjectOld.(*deliveryv1alpha1.Serving)
		newServing, okNew := e.ObjectNew.(*deliveryv1alpha1.Serving)
		return okOld && okNew && !equality.Semantic.DeepEqual(oldServing.Status.History, newServing.Status.History)
	},
	GenericFunc: func(event.GenericEvent) bool { return false },
}

// productScansChanged passes the Product updates that change its scans,
// which the checks and the deployed summary of its Servings read.
var productScansChanged = predicate.Funcs{
	CreateFunc: func(event.CreateEvent) bool { return false },
	DeleteFunc: func(event.DeleteEvent) bool { return false },
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldProduct, okOld := e.ObjectOld.(*deliveryv1alpha1.Product)
		newProduct, okNew := e.ObjectNew.(*deliveryv1alpha1.Product)
		return okOld && okNew && !equality.Semantic.DeepEqual(oldProduct.Status.Vulnerabilities, newProduct.Status.Vulnerabilities)
	},
	GenericFunc: func(event.GenericEvent) bool { return false },
}

// enqueueServingsOfRegistry enqueues every Serving in the namespace of a
// Registry. A Serving reads through the Registry recorded with its
// Preparation, its Order's, its Product's and those of its upstream stages'
// records, so no index covers them all; references stay in one namespace.
// A fixed Secret reaches the Serving here too, through the Registry's Ready.
func (r *ServingReconciler) enqueueServingsOfRegistry(ctx context.Context, obj client.Object) []ctrl.Request {
	servings := &deliveryv1alpha1.ServingList{}
	if err := r.List(ctx, servings, client.InNamespace(obj.GetNamespace())); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Servings of Registry", "registry", obj.GetName())
		return nil
	}

	reqs := make([]ctrl.Request, 0, len(servings.Items))
	for i := range servings.Items {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&servings.Items[i])})
	}
	return reqs
}

// enqueueAllServings enqueues every Serving, so that their targets are
// verified again against changed trust.
func (r *ServingReconciler) enqueueAllServings(ctx context.Context, _ client.Object) []ctrl.Request {
	servings := &deliveryv1alpha1.ServingList{}
	if err := r.List(ctx, servings); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Servings")
		return nil
	}

	reqs := make([]ctrl.Request, 0, len(servings.Items))
	for i := range servings.Items {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&servings.Items[i])})
	}
	return reqs
}

// signingChanges returns a channel that receives an event whenever the
// trust of the signing state changes.
func (r *ServingReconciler) signingChanges() <-chan event.GenericEvent {
	ch := make(chan event.GenericEvent, 1)
	r.Signing.OnChange(func() {
		select {
		case ch <- event.GenericEvent{Object: &deliveryv1alpha1.Kitchen{}}:
		default:
			// A notification is already pending.
		}
	})
	return ch
}

// enqueueServingsOfEnvironment maps an Environment to the Servings of the
// Orders of its namespace that deploy to it, which carry their Orders'
// names. It lists Orders by the index the Order controller registers.
func (r *ServingReconciler) enqueueServingsOfEnvironment(ctx context.Context, obj client.Object) []ctrl.Request {
	return r.servingsOfEnvironments(ctx, obj.GetNamespace(), obj.GetName())
}

// enqueueServingsOfCluster maps a Cluster to the Servings that deploy to it:
// those of the Orders of the Environments of its namespace that select it.
// An update maps both the old and the new Cluster, so that the Servings of
// an Environment it left reconcile too.
func (r *ServingReconciler) enqueueServingsOfCluster(ctx context.Context, obj client.Object) []ctrl.Request {
	c, ok := obj.(*deliveryv1alpha1.Cluster)
	if !ok {
		return nil
	}
	environments := &deliveryv1alpha1.EnvironmentList{}
	if err := r.List(ctx, environments, client.InNamespace(c.Namespace), client.UnsafeDisableDeepCopy); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Environments of Cluster", "cluster", c.Name)
		return nil
	}
	var names []string
	for i := range environments.Items {
		if selection, err := resolve.ClusterSelectionOf(&environments.Items[i]); err == nil && selection.Matches(c) {
			names = append(names, environments.Items[i].Name)
		}
	}
	return r.servingsOfEnvironments(ctx, c.Namespace, names...)
}

// servingsOfProject maps an Argo CD AppProject to the Servings of the Orders
// of the Environments that deploy through it.
func (r *ServingReconciler) servingsOfProject(ctx context.Context, obj client.Object) []ctrl.Request {
	environments := &deliveryv1alpha1.EnvironmentList{}
	if err := r.List(ctx, environments, client.UnsafeDisableDeepCopy); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Environments of AppProject", "project", obj.GetName())
		return nil
	}
	var reqs []ctrl.Request
	for i := range environments.Items {
		if env := &environments.Items[i]; env.Spec.Deployer.ArgoCD != nil && env.Spec.Deployer.ArgoCD.Project == obj.GetName() {
			reqs = append(reqs, r.servingsOfEnvironments(ctx, env.Namespace, env.Name)...)
		}
	}
	return reqs
}

// servingsOfEnvironments returns the Servings of the Orders of namespace
// that deploy to the Environments names, which carry their Orders' names.
// It lists Orders by the index the Order controller registers.
func (r *ServingReconciler) servingsOfEnvironments(ctx context.Context, namespace string, names ...string) []ctrl.Request {
	var reqs []ctrl.Request
	for _, name := range names {
		orders := &deliveryv1alpha1.OrderList{}
		if err := r.List(ctx, orders, client.InNamespace(namespace),
			client.MatchingFields{deliveryv1alpha1.FieldEnvironmentName: name}, client.UnsafeDisableDeepCopy); err != nil {
			log.FromContext(ctx).Error(err, "Failed to list Orders of Environment", "environment", name)
			continue
		}
		for i := range orders.Items {
			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&orders.Items[i])})
		}
	}
	return reqs
}

// SetupWithManager sets up the controller with the Manager. The Application
// and AppProject watches start once Argo CD's CRDs are installed.
func (r *ServingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	applications, err := newOptionalWatch(mgr, argocd.New())
	if err != nil {
		return err
	}
	appProjects, err := newOptionalWatch(mgr, newAppProject())
	if err != nil {
		return err
	}
	c, err := ctrl.NewControllerManagedBy(mgr).
		// Status writes of the controller itself need no reconcile.
		For(&deliveryv1alpha1.Serving{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&deliveryv1alpha1.Serving{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueDownstreamServings),
			builder.WithPredicates(historyChanged)).
		Watches(&deliveryv1alpha1.Preparation{}, handler.EnqueueRequestsFromMapFunc(enqueueServingForOrderName)).
		Watches(&deliveryv1alpha1.Order{},
			handler.EnqueueRequestsFromMapFunc(enqueueServingForOrderName),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&deliveryv1alpha1.Product{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueServingsOfProduct),
			builder.WithPredicates(productScansChanged)).
		// The Serving decides from the Environment's spec, not its status.
		Watches(&deliveryv1alpha1.Environment{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueServingsOfEnvironment),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&deliveryv1alpha1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueServingsOfCluster),
			builder.WithPredicates(clusterChanged)).
		// A Registry that comes, goes or turns Ready may release a hold.
		Watches(&deliveryv1alpha1.Registry{}, handler.EnqueueRequestsFromMapFunc(r.enqueueServingsOfRegistry)).
		WatchesRawSource(source.Channel(r.signingChanges(), handler.EnqueueRequestsFromMapFunc(r.enqueueAllServings))).
		WatchesRawSource(source.Channel(applications.Started(), handler.EnqueueRequestsFromMapFunc(r.enqueueAllServings))).
		WatchesRawSource(source.Channel(appProjects.Started(), handler.EnqueueRequestsFromMapFunc(r.enqueueAllServings))).
		Named("serving").
		Build(r)
	if err != nil {
		return err
	}
	if err := appProjects.Add(mgr, c, handler.EnqueueRequestsFromMapFunc(r.servingsOfProject),
		predicate.GenerationChangedPredicate{}); err != nil {
		return err
	}
	return applications.Add(mgr, c, handler.EnqueueRequestsFromMapFunc(servingOfApplication), applicationChanged)
}
