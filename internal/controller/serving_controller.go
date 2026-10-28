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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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

// ServingReconciler reconciles a Serving object. It promotes the Preparation
// the Serving converges to once it passes its gates, deploys it through an
// Argo CD Application, which lives in the argocd namespace and is deleted
// with the Serving, and records every promotion and every healthy
// deployment in the Serving's history and, signed, in the registry.
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

	// artifacts remembers the Preparations whose evidence verified.
	artifacts evidence.Verifier
	// records remembers the upstream deployment records that verified.
	records promotion.RecordVerifier
	// reports remembers the findings of verified vulnerability reports.
	reports vulnerability.Reports

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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;create;update;patch;delete
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
	}

	next, derr := r.deploy(ctx, serving, held)
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

// deploy keeps the Serving's Argo CD Application pointed at the deployed
// Preparation and records what Argo CD reports on it: the Ready condition,
// its health in the history and, once it is healthy, its deployment record
// and the summary of its scans. Ready describes what runs; held, what holds
// the Preparation the Serving converges to, is added to its message, and is
// its message while nothing runs. It returns how soon to reconcile again.
func (r *ServingReconciler) deploy(ctx context.Context, serving *deliveryv1alpha1.Serving, held string) (time.Duration, error) {
	logger := log.FromContext(ctx)

	history := historyOf(serving)
	deployed := history.Deployed()
	if deployed == nil {
		if held != "" {
			markPending(serving, held)
		}
		return 0, nil
	}
	defer func() {
		if ready := meta.FindStatusCondition(serving.Status.Conditions, deliveryv1alpha1.ConditionTypeReady); ready != nil && held != "" {
			conditions.Set(serving, ready.Type, ready.Status, ready.Reason, ready.Message+". "+held)
		}
	}()

	// The Preparation deploys into the namespace it was rendered for.
	preparation := &deliveryv1alpha1.Preparation{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: deployed.Preparation}, preparation); err != nil {
		return 0, fmt.Errorf("failed to get Preparation %q: %w", deployed.Preparation, err)
	}
	target := argocd.Target{
		Cluster: cluster.InCluster, Server: cluster.InClusterServer,
		Namespace: preparation.Spec.TargetNamespace, Project: argocd.DefaultProject,
	}
	app, err := r.applyApplication(ctx, serving, deployed, target)
	if err != nil {
		if meta.IsNoMatchError(err) {
			// The Application watch reconciles every Serving once the CRD
			// is installed.
			conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonArgoCDNotInstalled,
				"Argo CD's Application CRD is not installed; install Argo CD to deploy")
			return 0, nil
		}
		if errors.Is(err, argocd.ErrOptInRequired) {
			// Holds for this generation: the Application watch reports a
			// change to the opt-in, and a spec change reports itself.
			logger.Info("Could not update deployment, opt-in required", "error", err.Error())
			markDeploymentFailed(serving, err.Error())
			return 0, nil
		}
		markDeploymentFailed(serving, "failed to deploy: "+err.Error())
		return 0, fmt.Errorf("failed to deploy: %w", err)
	}

	state := argocd.StateOf(app)
	markDeployment(serving, deployed, state)

	// A time goes into the status a pass before the record that contains it
	// goes into the registry, so that a retry pushes equal bytes.
	if observed, changed := history.Observe(observationOf(state), r.clock()); changed {
		logger.Info("Recorded deployment state", "preparation", deployed.Preparation, "phase", state.Phase)
		if now := observed.Deployed(); deployed.Healthy.IsZero() && !now.Healthy.IsZero() {
			// The summary described what ran before; the next pass
			// describes the Preparation that is healthy now.
			serving.Status.DeployedVulnerabilities = nil
		}
		setHistory(serving, observed)
		return historyPassInterval, nil
	}

	if err := r.recordDeployments(ctx, serving, history); err != nil {
		return 0, err
	}
	return 0, r.recordDeployedVulnerabilities(ctx, serving, deployed)
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
			markDeploymentFailed(serving, fmt.Sprintf("preparation not found: %v", err))
			return order, nil, "", fmt.Errorf("failed to get Preparation %q: %w", serving.Spec.PreparationName, err)
		}
		return order, preparation, "", nil
	}

	preparations := &deliveryv1alpha1.PreparationList{}
	if err := r.List(ctx, preparations,
		client.InNamespace(serving.Namespace),
		client.MatchingFields{deliveryv1alpha1.FieldOrderName: serving.Spec.OrderName},
	); err != nil {
		markDeploymentFailed(serving, fmt.Sprintf("failed to list preparations: %v", err))
		return order, nil, "", fmt.Errorf("failed to list Preparations: %w", err)
	}

	var latest *deliveryv1alpha1.Preparation
	for i := range preparations.Items {
		prep := &preparations.Items[i]
		if !meta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) {
			continue
		}
		if latest == nil || prep.CreationTimestamp.After(latest.CreationTimestamp.Time) {
			latest = prep
		}
	}

	if latest == nil {
		// The Preparation watch reconciles again once one is ready.
		msg := "Waiting for ready preparation"
		if len(preparations.Items) == 0 {
			msg = "Waiting for preparations"
		}
		return order, nil, msg, nil
	}
	return order, latest, "", nil
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

// upstream evaluates the upstream gate of preparation: every upstream stage
// it names must have deployed its own Preparation of the same Product
// revision healthily for its soak time, as the stage's Serving records it,
// and the deployment record that qualifies it must verify in the registry.
// It records the result in the UpstreamDeployed condition.
func (r *ServingReconciler) upstream(
	ctx context.Context, serving *deliveryv1alpha1.Serving, order *deliveryv1alpha1.Order,
	preparation *deliveryv1alpha1.Preparation, history promotion.History,
) (promotion.Result, error) {
	stages := stagesOf(preparation)
	if len(stages) == 0 {
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
	for _, stage := range stages {
		upstreamOrder := &deliveryv1alpha1.Order{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: stage.Order}, upstreamOrder); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return promotion.Result{}, fmt.Errorf("failed to get upstream Order %q: %w", stage.Order, err)
		}
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
		// The Registry watch of the Order reconciles again once the
		// Registries change.
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
// healthy, attached to its artifact.
func (r *ServingReconciler) recordDeployments(ctx context.Context, serving *deliveryv1alpha1.Serving, history promotion.History) error {
	unrecorded := history.Unrecorded()
	if len(unrecorded) == 0 {
		return nil
	}
	state, err := r.Signing.State(ctx)
	if err != nil {
		return fmt.Errorf("waiting for the signing key to record the deployment: %w", err)
	}
	for _, e := range unrecorded {
		// The Preparation deploys into its target namespace. A Preparation
		// that is gone leaves nothing to attach the record to.
		preparation := &deliveryv1alpha1.Preparation{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: e.Preparation}, preparation); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed to get Preparation %q: %w", e.Preparation, err)
		}
		targets := []promotion.Target{{Server: cluster.InClusterServer, Namespace: preparation.Spec.TargetNamespace}}
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

// applyApplication points the Serving's Argo CD Application of target at
// the artifact of the deployed entry, creating it when it is missing, and
// returns it as it is now. An Application that already points there is left untouched,
// and drift is reverted. A pre-existing Application that is not opted in for
// the Serving's Order is never changed: the error wraps
// argocd.ErrOptInRequired.
func (r *ServingReconciler) applyApplication(
	ctx context.Context, serving *deliveryv1alpha1.Serving, deployed *promotion.Entry, target argocd.Target,
) (*unstructured.Unstructured, error) {
	logger := log.FromContext(ctx)

	desired, err := argocd.Application(serving, deployed.OCIRef, target)
	if err != nil {
		return nil, err
	}

	existing := argocd.New()
	if err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get Application: %w", err)
		}
		if err := r.Create(ctx, desired); err != nil {
			return nil, fmt.Errorf("failed to create Application: %w", err)
		}
		logger.Info("Created Argo CD Application", "name", desired.GetName(), "namespace", desired.GetNamespace(), "preparation", deployed.Preparation)
		return desired, nil
	}

	if err := argocd.OptedIn(existing, serving.Namespace, serving.Spec.OrderName); err != nil {
		return nil, err
	}
	if argocd.Matches(existing, desired) {
		return existing, nil
	}

	desired.SetResourceVersion(existing.GetResourceVersion())
	if err := r.Update(ctx, desired); err != nil {
		return nil, fmt.Errorf("failed to update Application: %w", err)
	}
	logger.Info("Updated Argo CD Application", "name", desired.GetName(), "namespace", desired.GetNamespace(), "preparation", deployed.Preparation)
	return desired, nil
}

// reconcileDelete deletes the Serving's Argo CD Application, which garbage
// collection cannot reach in the argocd namespace, then removes the
// finalizer.
func (r *ServingReconciler) reconcileDelete(ctx context.Context, serving *deliveryv1alpha1.Serving) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Serving")

	if controllerutil.ContainsFinalizer(serving, deliveryv1alpha1.Finalizer) {
		logger.Info("Cleaning up Serving resources")

		if err := r.removeApplication(ctx, serving); err != nil {
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

// removeApplication deletes the Serving's Argo CD Application. One that is
// gone already counts as removed, as does any without Argo CD's CRD, and
// one that is not opted in for the Serving's Order is kept. The delete is conditional on the UID and
// resource version that passed the opt-in check, so an Application replaced
// or changed in between is never deleted.
func (r *ServingReconciler) removeApplication(ctx context.Context, serving *deliveryv1alpha1.Serving) error {
	logger := log.FromContext(ctx)

	app := argocd.New()
	if err := r.Get(ctx, argocd.Key(serving, cluster.InCluster), app); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			logger.Info("Argo CD Application already deleted", "name", serving.Name)
			return nil
		}
		return fmt.Errorf("failed to get Application: %w", err)
	}

	if err := argocd.OptedIn(app, serving.Namespace, serving.Spec.OrderName); err != nil {
		logger.Info("Keeping Argo CD Application that is not opted in for the Order", "name", app.GetName(), "error", err.Error())
		return nil
	}

	uid, resourceVersion := app.GetUID(), app.GetResourceVersion()
	if err := r.Delete(ctx, app, client.Preconditions{UID: &uid, ResourceVersion: &resourceVersion}); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to delete Application: %w", err)
	}

	logger.Info("Deleted Argo CD Application", "name", app.GetName())
	return nil
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

// markDeployment derives the Ready condition of the Serving from the state
// of its Application. The health of another revision says nothing about the
// deployed Preparation, so until the Application reaches it the Serving is
// Deploying. An unchanged state changes nothing, so a Serving that stays
// degraded or progressing settles until the Application changes.
func markDeployment(serving *deliveryv1alpha1.Serving, deployed *promotion.Entry, state argocd.State) {
	atTarget := state.Revision == deployed.Digest
	switch {
	case atTarget && state.Phase == argocd.PhaseDegraded:
		msg := "deployment degraded"
		if state.Message != "" {
			msg += ": " + state.Message
		}
		markDeploymentFailed(serving, msg)
	case atTarget && state.Phase == argocd.PhaseHealthy:
		conditions.MarkTrue(serving, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeployed, "Deployed Preparation "+deployed.Preparation)
	default:
		conditions.MarkUnknown(serving, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeploying, "Deploying Preparation "+deployed.Preparation)
	}
}

// markPending marks the Serving as waiting for a prerequisite.
func markPending(serving *deliveryv1alpha1.Serving, msg string) {
	conditions.MarkUnknown(serving, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonPending, msg)
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

// observationOf maps what Argo CD reports on an Application.
func observationOf(state argocd.State) promotion.Observation {
	observation := promotion.Observation{Revision: state.Revision, Health: promotion.HealthProgressing}
	switch state.Phase {
	case argocd.PhaseHealthy:
		observation.Health = promotion.HealthHealthy
	case argocd.PhaseDegraded:
		observation.Health = promotion.HealthDegraded
	}
	return observation
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

// servingOfApplication maps an Argo CD Application to the Serving its labels
// name.
func servingOfApplication(_ context.Context, obj client.Object) []ctrl.Request {
	key, ok := argocd.ServingOf(obj.GetLabels())
	if !ok {
		return nil
	}
	return []ctrl.Request{{NamespacedName: key}}
}

// applicationChanged passes the Application updates that change what the
// Serving status and the opt-in check depend on. Creates and deletes always
// pass.
var applicationChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldApp, okOld := e.ObjectOld.(*unstructured.Unstructured)
		newApp, okNew := e.ObjectNew.(*unstructured.Unstructured)
		return okOld && okNew && argocd.Changed(oldApp, newApp)
	},
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

// crdCheckInterval is how often an optional watch checks whether the CRD of
// its kind is installed.
const crdCheckInterval = 30 * time.Second

// optionalWatch adds the watch of a kind from another project, such as Argo
// CD's Application, to a controller once its CRD is installed and serves
// the version Kokumi reads, so that the manager starts and every controller
// runs without it. It checks at start and then every crdCheckInterval, and
// once the watch runs it sends an event on Started, so that the reconciles
// that found the kind missing run again. Reconciles never ask it whether the
// kind is installed: reads of a kind without its CRD fail with a no-match
// error, which tells them.
type optionalWatch struct {
	obj      client.Object
	gvk      schema.GroupVersionKind
	mapper   meta.RESTMapper
	interval time.Duration
	started  chan event.GenericEvent
}

// newOptionalWatch returns the optional watch of the kind of obj: a type of
// the manager's scheme, or an unstructured object with its kind set.
func newOptionalWatch(mgr ctrl.Manager, obj client.Object) (*optionalWatch, error) {
	gvk, err := apiutil.GVKForObject(obj, mgr.GetScheme())
	if err != nil {
		return nil, err
	}
	return &optionalWatch{obj: obj, gvk: gvk, mapper: mgr.GetRESTMapper(), interval: crdCheckInterval, started: make(chan event.GenericEvent, 1)}, nil
}

// Started returns the channel that receives an event once the watch runs.
func (w *optionalWatch) Started() <-chan event.GenericEvent {
	return w.started
}

// Add adds the runnable to mgr that starts the watch on c, with h and
// predicates, once the CRD is installed.
func (w *optionalWatch) Add(mgr ctrl.Manager, c controller.Controller, h handler.EventHandler, predicates ...predicate.Predicate) error {
	return mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		return w.wait(ctx, func() error {
			return c.Watch(source.Kind[client.Object](mgr.GetCache(), w.obj, h, predicates...))
		})
	}))
}

// wait calls start once the CRD of the kind is installed, then sends an
// event on Started. It returns when the watch started or ctx is done.
func (w *optionalWatch) wait(ctx context.Context, start func() error) error {
	logger := log.FromContext(ctx).WithValues("kind", w.gvk.Kind, "group", w.gvk.Group)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for logged := false; ; {
		_, err := w.mapper.RESTMapping(w.gvk.GroupKind(), w.gvk.Version)
		switch {
		case err == nil:
			if err := start(); err != nil {
				return fmt.Errorf("failed to watch %s: %w", w.gvk.Kind, err)
			}
			w.started <- event.GenericEvent{Object: w.obj}
			logger.Info("Watching optional kind, its CRD is installed")
			return nil
		case !meta.IsNoMatchError(err):
			logger.Error(err, "Failed to check whether the CRD of an optional kind is installed")
		case !logged:
			logger.Info("CRD of optional kind not installed, or not serving the version, checking again",
				"version", w.gvk.Version, "interval", w.interval)
			logged = true
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// SetupWithManager sets up the controller with the Manager. The Application
// watch starts once Argo CD's Application CRD is installed, so that the
// manager runs without Argo CD.
func (r *ServingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	applications, err := newOptionalWatch(mgr, argocd.New())
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
		WatchesRawSource(source.Channel(r.signingChanges(), handler.EnqueueRequestsFromMapFunc(r.enqueueAllServings))).
		WatchesRawSource(source.Channel(applications.Started(), handler.EnqueueRequestsFromMapFunc(r.enqueueAllServings))).
		Named("serving").
		Build(r)
	if err != nil {
		return err
	}
	return applications.Add(mgr, c, handler.EnqueueRequestsFromMapFunc(servingOfApplication), applicationChanged)
}
