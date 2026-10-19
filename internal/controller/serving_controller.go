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
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/lru"
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
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/evidence"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/vulnerability"
)

// verificationCacheSize bounds how many verified artifacts the Serving
// controller remembers.
const verificationCacheSize = 1024

const (
	// reportCacheSize bounds how many verified vulnerability reports the
	// Serving controller remembers.
	reportCacheSize = 256
	// defaultScanIntervalSeconds is the Kitchen's default scanning interval,
	// which a check's default maxAgeSeconds doubles.
	defaultScanIntervalSeconds = 86400
)

// reportKey identifies a verified vulnerability report. Reports are
// immutable, so their findings hold until the trust changes.
type reportKey struct {
	digest string
	trust  string
}

// verificationKey identifies a verification result. Digests are immutable,
// so a result holds until the trust changes; it is specific to the Order the
// provenance must name and to whether approvals are required.
type verificationKey struct {
	digest           string
	trust            string
	order            string
	approvalRequired bool
}

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

// ServingReconciler reconciles a Serving object. It deploys the Preparation
// the Serving converges to through an Argo CD Application, which lives in
// the argocd namespace and is deleted with the Serving.
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

	// verified remembers verified artifacts by verificationKey.
	verified     *lru.Cache
	verifiedOnce sync.Once
	// reports remembers the findings of verified reports by reportKey.
	reports     *lru.Cache
	reportsOnce sync.Once

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

// reconcileServing deploys the Preparation the Serving converges to once it
// is ready, approved, and its evidence verifies, and reports how its
// deployment is doing. A Preparation that does not pass leaves the deployed
// one untouched.
func (r *ServingReconciler) reconcileServing(ctx context.Context, serving *deliveryv1alpha1.Serving) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	preparation, err := r.targetPreparation(ctx, serving)
	if preparation == nil || err != nil {
		return ctrl.Result{}, err
	}

	// A Preparation is served only once it is Ready, under both promotion
	// modes. The Preparation watch triggers the next reconcile.
	if !meta.IsStatusConditionTrue(preparation.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) {
		logger.Info("Preparation not ready, waiting", "preparation", preparation.Name)
		markPending(serving, fmt.Sprintf("Waiting for Preparation %q to become ready", preparation.Name))
		return ctrl.Result{}, nil
	}

	if blocked, err := r.gate(ctx, serving, preparation); blocked || err != nil {
		return ctrl.Result{}, err
	}

	// The registry is the record: the signature and attestations are
	// verified there, not trusted from the Preparation's status.
	if blocked, err := r.verify(ctx, serving, preparation); blocked || err != nil {
		return ctrl.Result{}, err
	}

	recheck, blocked, err := r.checks(ctx, serving, preparation)
	if blocked || err != nil {
		return ctrl.Result{RequeueAfter: recheck}, err
	}

	app, err := r.applyApplication(ctx, serving, preparation)
	if err != nil {
		if errors.Is(err, argocd.ErrOptInRequired) {
			// Holds for this generation: the Application watch reports a
			// change to the opt-in, and a spec change reports itself.
			logger.Info("Could not update deployment, opt-in required", "error", err.Error())
			markDeploymentFailed(serving, err.Error())
			return ctrl.Result{}, nil
		}
		markDeploymentFailed(serving, "failed to deploy: "+err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to deploy: %w", err)
	}

	deployedBefore := serving.Status.DeployedDigest
	markDeployment(serving, preparation, argocd.StateOf(app))
	if err := r.recordDeployedVulnerabilities(ctx, serving, preparation, deployedBefore); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: recheck}, nil
}

// targetPreparation returns the Preparation the Serving converges to, nil
// while there is none. The promotion mode is read from the Order: Manual
// serves spec.preparationName, Automatic the newest Ready Preparation of the
// Order. The Serving spec is never modified.
func (r *ServingReconciler) targetPreparation(ctx context.Context, serving *deliveryv1alpha1.Serving) (*deliveryv1alpha1.Preparation, error) {
	order := &deliveryv1alpha1.Order{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: serving.Spec.OrderName}, order); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get Order: %w", err)
		}
		markPending(serving, fmt.Sprintf("Waiting for Order %q", serving.Spec.OrderName))
		return nil, nil
	}

	if order.EffectivePromotionMode() != deliveryv1alpha1.PromotionModeAutomatic {
		if serving.Spec.PreparationName == "" {
			markPending(serving, "Waiting for a Preparation to be promoted")
			return nil, nil
		}
		preparation := &deliveryv1alpha1.Preparation{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: serving.Spec.PreparationName}, preparation); err != nil {
			markDeploymentFailed(serving, fmt.Sprintf("preparation not found: %v", err))
			return nil, fmt.Errorf("failed to get Preparation %q: %w", serving.Spec.PreparationName, err)
		}
		return preparation, nil
	}

	preparations := &deliveryv1alpha1.PreparationList{}
	if err := r.List(ctx, preparations,
		client.InNamespace(serving.Namespace),
		client.MatchingFields{deliveryv1alpha1.FieldOrderName: serving.Spec.OrderName},
	); err != nil {
		markDeploymentFailed(serving, fmt.Sprintf("failed to list preparations: %v", err))
		return nil, fmt.Errorf("failed to list Preparations: %w", err)
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
		markPending(serving, msg)
		return nil, nil
	}
	return latest, nil
}

// gate records the target Preparation and whether it passes its approval
// gate, under both promotion modes. It reports whether deployment must not
// proceed.
func (r *ServingReconciler) gate(ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation) (bool, error) {
	approvals := &deliveryv1alpha1.ApprovalList{}
	if err := r.List(ctx, approvals,
		client.InNamespace(preparation.Namespace),
		client.MatchingFields{deliveryv1alpha1.FieldPreparationRefName: preparation.Name},
	); err != nil {
		return true, fmt.Errorf("failed to list Approvals: %w", err)
	}

	gate := approval.Gate(preparation, approvals.Items)
	if gate.Blocked {
		log.FromContext(ctx).V(4).Info("Approval gate blocks deployment", "preparation", preparation.Name, "reason", gate.Reason)
	}
	serving.Status.TargetPreparationName = preparation.Name
	conditions.Set(serving, deliveryv1alpha1.ConditionTypeApproved, gate.Status, gate.Reason, gate.Message)
	return gate.Blocked, nil
}

// verify checks the evidence of preparation's artifact in the registry
// before it is deployed: its signature, its provenance for the Serving's
// Order, and, when an approval policy applies, its sealed approvals. It
// reports whether deployment must not proceed.
func (r *ServingReconciler) verify(ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation) (bool, error) {
	logger := log.FromContext(ctx)

	state, err := r.Signing.State(ctx)
	if err != nil {
		// The provider notifies the watch once the key is loaded.
		logger.Info("Waiting for the signing key to verify Preparation", "preparation", preparation.Name, "reason", err.Error())
		conditions.MarkUnknown(serving, deliveryv1alpha1.ConditionTypeVerified, deliveryv1alpha1.ReasonSigningKeyNotReady,
			preparation.Name+": waiting for the signing key: "+err.Error())
		return true, nil
	}

	key := verificationKey{
		digest:           preparation.Spec.Artifact.Digest,
		trust:            state.Trust.ID(),
		order:            serving.Namespace + "/" + serving.Spec.OrderName,
		approvalRequired: preparation.Spec.ApprovalPolicy != nil,
	}

	r.verifiedOnce.Do(func() { r.verified = lru.New(verificationCacheSize) })
	if _, ok := r.verified.Get(key); !ok {
		conn, err := r.artifactConnection(ctx, preparation)
		if err != nil {
			if reason := registry.Reason(err, ""); reason != "" {
				// The Registry watch of the Order reconciles again once
				// the Registries change.
				conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeVerified, reason, preparation.Name+": "+err.Error())
				return true, nil
			}
			return true, err
		}

		if err := evidence.Verify(ctx, conn.Client, state.Trust, preparation, key.order); err != nil {
			var rejected *evidence.Rejected
			if !errors.As(err, &rejected) {
				return true, fmt.Errorf("failed to verify Preparation %q: %w", preparation.Name, err)
			}
			logger.Info("Evidence of Preparation did not verify, keeping the current deployment",
				"preparation", preparation.Name, "reason", rejected.Reason, "error", err.Error())
			conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeVerified, rejected.Reason, preparation.Name+": "+err.Error())
			return true, nil
		}

		r.verified.Add(key, struct{}{})
	}

	conditions.MarkTrue(serving, deliveryv1alpha1.ConditionTypeVerified, deliveryv1alpha1.ReasonSignaturesVerified,
		preparation.Name+": signature and attestations verified")
	return false, nil
}

// checkReasons maps the outcome of a vulnerability check to the reason of
// the ChecksPassed condition.
var checkReasons = map[vulnerability.Outcome]string{
	vulnerability.OutcomePassed:               deliveryv1alpha1.ReasonPassed,
	vulnerability.OutcomeVulnerabilitiesFound: deliveryv1alpha1.ReasonVulnerabilitiesFound,
	vulnerability.OutcomeScanMissing:          deliveryv1alpha1.ReasonScanMissing,
	vulnerability.OutcomeScanStale:            deliveryv1alpha1.ReasonScanStale,
}

// checks evaluates the promotion checks preparation was rendered under
// against the verified scan reports of its images. It returns how soon to
// check again, as the scans age, and whether deployment must not proceed.
// The checks hold new deployments only: the deployed Preparation that stops
// passing them stays deployed.
func (r *ServingReconciler) checks(
	ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation,
) (time.Duration, bool, error) {
	var check *deliveryv1alpha1.VulnerabilityCheck
	if preparation.Spec.Checks != nil {
		check = preparation.Spec.Checks.Vulnerabilities
	}
	if check == nil {
		conditions.Remove(serving, deliveryv1alpha1.ConditionTypeChecksPassed)
		return 0, false, nil
	}

	verdict, err := r.evaluateVulnerabilities(ctx, serving, preparation, check)
	if err != nil {
		return 0, true, err
	}
	reason := checkReasons[verdict.Outcome]
	if verdict.Outcome == vulnerability.OutcomePassed {
		conditions.MarkTrue(serving, deliveryv1alpha1.ConditionTypeChecksPassed, reason, preparation.Name+": "+verdict.Message)
		return verdict.Recheck, false, nil
	}

	conditions.MarkFalse(serving, deliveryv1alpha1.ConditionTypeChecksPassed, reason, preparation.Name+": "+verdict.Message)
	if serving.Status.DeployedDigest == preparation.Spec.Artifact.Digest {
		log.FromContext(ctx).Info("Deployed Preparation no longer passes its checks, keeping it deployed",
			"preparation", preparation.Name, "reason", reason)
		return 0, false, nil
	}
	log.FromContext(ctx).V(4).Info("Checks block deployment", "preparation", preparation.Name, "reason", reason)
	return 0, true, nil
}

// evaluateVulnerabilities evaluates check against the latest scan of every
// image of preparation, as the Product records it and the signed report in
// the registry holds it.
func (r *ServingReconciler) evaluateVulnerabilities(
	ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation, check *deliveryv1alpha1.VulnerabilityCheck,
) (vulnerability.Verdict, error) {
	product, err := r.productOf(ctx, serving)
	if err != nil {
		return vulnerability.Verdict{}, err
	}
	if product == nil {
		return vulnerability.Verdict{Outcome: vulnerability.OutcomeScanMissing, Message: "the Order or its Product was not found"}, nil
	}
	maxAge, err := r.maxScanAge(ctx, check)
	if err != nil {
		return vulnerability.Verdict{}, err
	}

	assessments := vulnerability.Read(ctx, releaseImagesOf(preparation, product), r.reportReader(product), 1)
	return vulnerability.Evaluate(assessments, vulnerability.Check{
		MaxSeverity:   vulnerability.Severity(check.MaxSeverity),
		IgnoreUnfixed: check.IgnoreUnfixed,
		MaxAge:        maxAge,
	}, r.clock())
}

// reportReader returns the reader of the signed reports of the Product's
// scans: through its Registries, verified against the Kitchen's keys, and
// remembered by report digest until the keys change. Reports that do not
// verify, or Registries that cannot be used, are vulnerability.ErrUnreadable.
func (r *ServingReconciler) reportReader(product *deliveryv1alpha1.Product) vulnerability.FindingsReader {
	var (
		once     sync.Once
		scope    registry.Scope
		trust    *signing.TrustSet
		setupErr error
	)
	return func(ctx context.Context, scan vulnerability.Scan) ([]vulnerability.Finding, error) {
		once.Do(func() {
			state, err := r.Signing.State(ctx)
			if err != nil {
				setupErr = fmt.Errorf("waiting for the signing key to verify the reports: %w", err)
				return
			}
			trust = state.Trust
			if scope, err = r.Registries.Scope(ctx, product.Namespace, product.Spec.RegistryRefs); err != nil {
				setupErr = fmt.Errorf("connecting to the vendored images: %w", err)
			}
		})
		if setupErr != nil {
			return nil, vulnerability.Unreadable(setupErr)
		}

		r.reportsOnce.Do(func() { r.reports = lru.New(reportCacheSize) })
		key := reportKey{digest: scan.Report, trust: trust.ID()}
		if cached, ok := r.reports.Get(key); ok {
			return cached.([]vulnerability.Finding), nil
		}
		findings, err := r.fetchReport(ctx, scope, trust, scan)
		if err != nil {
			if errors.Is(err, signing.ErrNotVerified) || errors.Is(err, oci.ErrPayloadTooLarge) || registry.Reason(err, "") != "" {
				return nil, vulnerability.Unreadable(fmt.Errorf("its report did not verify: %w", err))
			}
			return nil, err
		}
		r.reports.Add(key, findings)
		return findings, nil
	}
}

// fetchReport reads the signed report of scan through scope and verifies it
// against trust.
func (r *ServingReconciler) fetchReport(
	ctx context.Context, scope registry.Scope, trust *signing.TrustSet, scan vulnerability.Scan,
) ([]vulnerability.Finding, error) {
	conn, err := scope.Connect(scan.Repository)
	if err != nil {
		return nil, err
	}
	subject, err := oci.Parse(scan.Repository + "@" + scan.Digest)
	if err != nil {
		return nil, err
	}
	return vulnerability.Fetch(ctx, conn.Client, trust, subject, scan.Report)
}

// recordDeployedVulnerabilities records the summary of the deployed
// Preparation's scans, and emits a Warning Event when the findings at or
// above the severity its check allows increase while it stays deployed.
// deployedBefore is the digest that was deployed before this reconcile.
func (r *ServingReconciler) recordDeployedVulnerabilities(
	ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation, deployedBefore string,
) error {
	// Until the target is healthy, the summary keeps describing what runs.
	if serving.Status.DeployedDigest != preparation.Spec.Artifact.Digest {
		return nil
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

	if previous == nil || !scanned || deployedBefore != preparation.Spec.Artifact.Digest ||
		preparation.Spec.Checks == nil || preparation.Spec.Checks.Vulnerabilities == nil || r.Recorder == nil {
		return nil
	}
	maxSeverity := vulnerability.Severity(preparation.Spec.Checks.Vulnerabilities.MaxSeverity)
	before := vulnerability.AtLeast(vulnerability.Summary{
		Critical: previous.Critical, High: previous.High, Medium: previous.Medium, Low: previous.Low,
	}, maxSeverity)
	if after := vulnerability.AtLeast(total, maxSeverity); after > before {
		r.Recorder.Eventf(serving, preparation, corev1.EventTypeWarning, deliveryv1alpha1.ReasonVulnerabilitiesIncreased, "Scan",
			"Deployed Preparation %s has %d vulnerabilities at or above %s, up from %d", preparation.Name, after, maxSeverity, before)
	}
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

// maxScanAge returns how old a scan check accepts: its maxAgeSeconds, or
// twice the Kitchen's scanning interval.
func (r *ServingReconciler) maxScanAge(ctx context.Context, check *deliveryv1alpha1.VulnerabilityCheck) (time.Duration, error) {
	if check.MaxAgeSeconds > 0 {
		return time.Duration(check.MaxAgeSeconds) * time.Second, nil
	}
	interval := int32(defaultScanIntervalSeconds)
	kitchen := &deliveryv1alpha1.Kitchen{}
	switch err := r.Get(ctx, client.ObjectKey{Namespace: r.InstallNamespace, Name: deliveryv1alpha1.DefaultKitchenName}, kitchen); {
	case err == nil:
		interval = kitchen.Spec.Scanning.EffectiveIntervalSeconds()
	case !apierrors.IsNotFound(err):
		return 0, fmt.Errorf("failed to get Kitchen: %w", err)
	}
	return 2 * time.Duration(interval) * time.Second, nil
}

// clock returns the current time.
func (r *ServingReconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// applyApplication points the Serving's Argo CD Application at the artifact
// of preparation, creating it when it is missing, and returns it as it is
// now. An Application that already points there is left untouched, and
// drift is reverted. A pre-existing Application that is not opted in for
// the Serving's Order is never changed: the error wraps
// argocd.ErrOptInRequired.
func (r *ServingReconciler) applyApplication(
	ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation,
) (*unstructured.Unstructured, error) {
	logger := log.FromContext(ctx)

	desired, err := argocd.Application(serving, preparation)
	if err != nil {
		return nil, err
	}

	existing := argocd.New()
	if err := r.Get(ctx, argocd.Key(serving), existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get Application: %w", err)
		}
		if err := r.Create(ctx, desired); err != nil {
			return nil, fmt.Errorf("failed to create Application: %w", err)
		}
		logger.Info("Created Argo CD Application", "name", desired.GetName(), "namespace", desired.GetNamespace(), "preparation", preparation.Name)
		return desired, nil
	}

	if err := argocd.OptedIn(existing, serving.Spec.OrderName); err != nil {
		return nil, err
	}
	if argocd.Matches(existing, desired) {
		return existing, nil
	}

	desired.SetResourceVersion(existing.GetResourceVersion())
	if err := r.Update(ctx, desired); err != nil {
		return nil, fmt.Errorf("failed to update Application: %w", err)
	}
	logger.Info("Updated Argo CD Application", "name", desired.GetName(), "namespace", desired.GetNamespace(), "preparation", preparation.Name)
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
// gone already counts as removed, and one that is not opted in for the
// Serving's Order is kept. The delete is conditional on the UID and
// resource version that passed the opt-in check, so an Application replaced
// or changed in between is never deleted.
func (r *ServingReconciler) removeApplication(ctx context.Context, serving *deliveryv1alpha1.Serving) error {
	logger := log.FromContext(ctx)

	app := argocd.New()
	if err := r.Get(ctx, argocd.Key(serving), app); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Argo CD Application already deleted", "name", serving.Name)
			return nil
		}
		return fmt.Errorf("failed to get Application: %w", err)
	}

	if err := argocd.OptedIn(app, serving.Spec.OrderName); err != nil {
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

// artifactConnection connects to the registry holding prep's artifact:
// through the Registry that authenticated its push, while it exists and
// still covers the artifact, and through the Order's registryRefs
// otherwise.
func (r *ServingReconciler) artifactConnection(ctx context.Context, prep *deliveryv1alpha1.Preparation) (registry.Connection, error) {
	conn, err := r.Registries.ConnectRecorded(ctx, prep.Namespace, prep.Spec.Artifact.RegistryRef, prep.Spec.Artifact.OCIRef,
		func(ctx context.Context) ([]corev1.LocalObjectReference, error) {
			order := &deliveryv1alpha1.Order{}
			if err := r.Get(ctx, client.ObjectKey{Namespace: prep.Namespace, Name: prep.Spec.OrderName}, order); err != nil && !apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("failed to get Order %q: %w", prep.Spec.OrderName, err)
			}
			return order.Spec.RegistryRefs, nil
		})
	if err != nil {
		return registry.Connection{}, fmt.Errorf("failed to connect to the destination of Preparation %q: %w", prep.Name, err)
	}
	return conn, nil
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
		return result, errors.Join(err, fmt.Errorf("failed to patch Serving status: %w", perr))
	}
}

// markDeployment derives the Ready condition of the Serving from the state
// of its Application. The health of another revision says nothing about
// preparation, so until the Application reaches it the Serving is
// Deploying. An unchanged state changes nothing, so a Serving that stays
// degraded or progressing settles until the Application changes.
func markDeployment(serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation, state argocd.State) {
	serving.Status.ObservedPreparationName = preparation.Name

	atTarget := state.Revision == preparation.Spec.Artifact.Digest
	switch {
	case atTarget && state.Phase == argocd.PhaseDegraded:
		msg := "deployment degraded"
		if state.Message != "" {
			msg += ": " + state.Message
		}
		markDeploymentFailed(serving, msg)
	case atTarget && state.Phase == argocd.PhaseHealthy:
		serving.Status.DeployedDigest = preparation.Spec.Artifact.Digest
		conditions.MarkTrue(serving, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeployed, "Successfully deployed component")
	default:
		conditions.MarkUnknown(serving, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeploying, "Deploying preparation "+preparation.Name)
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

// SetupWithManager sets up the controller with the Manager.
func (r *ServingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Status writes of the controller itself need no reconcile.
		For(&deliveryv1alpha1.Serving{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&deliveryv1alpha1.Preparation{}, handler.EnqueueRequestsFromMapFunc(enqueueServingForOrderName)).
		Watches(&deliveryv1alpha1.Order{},
			handler.EnqueueRequestsFromMapFunc(enqueueServingForOrderName),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(argocd.New(),
			handler.EnqueueRequestsFromMapFunc(servingOfApplication),
			builder.WithPredicates(applicationChanged)).
		Watches(&deliveryv1alpha1.Product{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueServingsOfProduct),
			builder.WithPredicates(productScansChanged)).
		WatchesRawSource(source.Channel(r.signingChanges(), handler.EnqueueRequestsFromMapFunc(r.enqueueAllServings))).
		Named("serving").
		Complete(r)
}
