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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/sbom"
	"github.com/kokumi-dev/kokumi/internal/scan"
	"github.com/kokumi-dev/kokumi/internal/scanner"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/vulnerability"
)

const (
	// scanConcurrency is how many Products are scanned at a time. The
	// scanner answers 503 when it is busy, so more would only wait.
	scanConcurrency = 2
	// scanTimeout bounds one call of the scanner.
	scanTimeout = 30 * time.Minute
	// scanRetryInterval is how soon images that failed to scan are tried
	// again.
	scanRetryInterval = 5 * time.Minute
	// scannerRetryInterval is how soon an unreachable scanner is called
	// again, unless it asked for another delay.
	scannerRetryInterval = time.Minute
	// scanCacheRetryInterval is how soon a scan postponed for a lagging
	// cache is tried again.
	scanCacheRetryInterval = time.Second
	// maxImageScans is the MaxItems of Product.status.vulnerabilities.images.
	maxImageScans = 512
)

// scannedReasons maps the state of a Product's scans to the reason of its
// Scanned condition.
var scannedReasons = map[scan.Reason]string{
	scan.ReasonScanned:       deliveryv1alpha1.ReasonScanned,
	scan.ReasonScanFailed:    deliveryv1alpha1.ReasonScanFailed,
	scan.ReasonDatabaseStale: deliveryv1alpha1.ReasonDatabaseStale,
	scan.ReasonStale:         deliveryv1alpha1.ReasonStale,
}

// scanRegistries connects through the Registries of a Product and of the
// Kitchen. *registry.Resolver implements it.
type scanRegistries interface {
	Scope(ctx context.Context, namespace string, refs []corev1.LocalObjectReference) (registry.Scope, error)
}

// scanSigning provides the key scan results are signed with and the trust
// reused SBOMs verify against. *signing.Provider implements it.
type scanSigning interface {
	State(ctx context.Context) (signing.State, error)
}

// ScanReconciler scans the images Products vendor for vulnerabilities, once
// per Product per image digest, with the kokumi-scanner component. It
// attaches the signed SBOMs and reports to the vendored images and records a
// summary in the Product's status. It is a second controller for Products:
// the Product controller writes the rest of their status.
type ScanReconciler struct {
	client.Client
	// APIReader bypasses the cache. Before a scan, it confirms that the
	// cached Product is the latest, so that a cache without the status of
	// the previous scan does not scan the same digests again.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	// Registries connects to the vendored images through the Product's
	// registryRefs, and to the databases through the Kitchen's.
	Registries scanRegistries
	// Signing signs the results and verifies reused SBOMs.
	Signing scanSigning
	// Scanner scans the images. *scanner.Client implements it.
	Scanner scan.Scanner
	// InstallNamespace holds the Kitchen and the Registries its scanning
	// names.
	InstallNamespace string
	// Platform is the platform scanned of multi-platform images, such as
	// "linux/amd64", the first one when an image lacks it.
	Platform string

	// now returns the current time. Tests set it.
	now func() time.Time
}

// errScanBlocked reports a scan that cannot start, for the reason and after
// the delay it carries; it is recorded, not retried at once.
type errScanBlocked struct {
	reason string
	after  time.Duration
	err    error
}

func (e *errScanBlocked) Error() string { return e.err.Error() }
func (e *errScanBlocked) Unwrap() error { return e.err }

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile scans the images of a Product that are due and records the
// results.
func (r *ScanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Reconciling Product scans", "namespace", req.Namespace, "name", req.Name)

	product := &deliveryv1alpha1.Product{}
	if err := r.Get(ctx, req.NamespacedName, product); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get Product: %w", err)
	}

	before := product.Status.DeepCopy()
	defer func() { result, err = r.patchStatus(ctx, product, before, result, err) }()

	if !product.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	return r.reconcileScan(ctx, product)
}

// reconcileScan scans the Product's vendored images in use that were not
// scanned within the Kitchen's interval, and schedules the next scan.
func (r *ScanReconciler) reconcileScan(ctx context.Context, product *deliveryv1alpha1.Product) (ctrl.Result, error) {
	scanning, err := r.scanning(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	if scanning == nil || product.Spec.Images.EffectivePolicy() != deliveryv1alpha1.ImagePolicyVendor {
		// Scanning covers the images Kokumi vendors, while it is enabled.
		product.Status.Vulnerabilities = nil
		conditions.Remove(product, deliveryv1alpha1.ConditionTypeScanned)
		return ctrl.Result{}, nil
	}

	inUse, err := r.imagesInUse(ctx, product)
	if err != nil {
		return ctrl.Result{}, err
	}
	records := scanRecordsOf(product, inUse)
	now := r.clock()
	interval := time.Duration(scanning.EffectiveIntervalSeconds()) * time.Second

	var failures []scan.Failure
	if due := scan.Due(inUse, records, interval, now); len(due) > 0 {
		// A scan attaches referrers, so it must not run on a cache that lags
		// behind the status of the previous scan. Status writes do not
		// reconcile the Product, so it checks again shortly.
		latest, err := r.isLatest(ctx, product)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !latest {
			log.FromContext(ctx).V(1).Info("Postponing scan until the cache holds the latest Product")
			return ctrl.Result{RequeueAfter: scanCacheRetryInterval}, nil
		}

		failures, err = r.scan(ctx, product, scanning, inUse, due, records, now)
		if blocked, ok := errors.AsType[*errScanBlocked](err); ok {
			log.FromContext(ctx).Info("Could not scan Product images", "reason", blocked.reason, "error", blocked.Error())
			setScanRecords(product, records)
			markScanBlocked(product, blocked)
			return ctrl.Result{RequeueAfter: blocked.after}, nil
		}
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	setScanRecords(product, records)
	markScanned(product, scanning, len(inUse), failures, now)
	if len(failures) > 0 {
		return ctrl.Result{RequeueAfter: scanRetryInterval}, nil
	}
	return ctrl.Result{RequeueAfter: scan.Next(records, interval, now)}, nil
}

// isLatest reports whether product, as read from the cache, is the version
// the API server holds. A Product deleted meanwhile is not.
func (r *ScanReconciler) isLatest(ctx context.Context, product *deliveryv1alpha1.Product) (bool, error) {
	live := &deliveryv1alpha1.Product{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(product), live); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get Product: %w", err)
	}
	return live.ResourceVersion == product.ResourceVersion, nil
}

// scanning returns the Kitchen's scanning, nil when scanning is disabled or
// there is no Kitchen yet.
func (r *ScanReconciler) scanning(ctx context.Context) (*deliveryv1alpha1.KitchenScanning, error) {
	kitchen := &deliveryv1alpha1.Kitchen{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.InstallNamespace, Name: deliveryv1alpha1.DefaultKitchenName}, kitchen); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get Kitchen: %w", err)
	}
	return kitchen.Spec.Scanning, nil
}

// imagesInUse returns the vendored images, by digest, that the
// Preparations in use of the Product's Orders run, as scan.InUse picks
// them: the newest Ready ones, and each Serving's promoted, target and
// deployed one. A promoted one is held by its checks before it becomes the
// target, so it must be scanned before.
func (r *ScanReconciler) imagesInUse(ctx context.Context, product *deliveryv1alpha1.Product) (map[string]oci.Reference, error) {
	orders := &deliveryv1alpha1.OrderList{}
	if err := r.List(ctx, orders, client.InNamespace(product.Namespace),
		client.MatchingFields{deliveryv1alpha1.FieldProductRefName: product.Name}); err != nil {
		return nil, fmt.Errorf("failed to list Orders: %w", err)
	}

	inUse := map[string]oci.Reference{}
	for _, order := range orders.Items {
		preparations := &deliveryv1alpha1.PreparationList{}
		if err := r.List(ctx, preparations, client.InNamespace(product.Namespace),
			client.MatchingFields{deliveryv1alpha1.FieldOrderName: order.Name}); err != nil {
			return nil, fmt.Errorf("failed to list Preparations: %w", err)
		}
		var named []string
		serving := &deliveryv1alpha1.Serving{}
		switch err := r.Get(ctx, client.ObjectKey{Namespace: product.Namespace, Name: order.Name}, serving); {
		case err == nil:
			named = append(named, serving.Status.TargetPreparationName, serving.Spec.PreparationName, serving.DeployedPreparationName())
		case !apierrors.IsNotFound(err):
			return nil, fmt.Errorf("failed to get Serving: %w", err)
		}

		releases := make([]scan.Release, 0, len(preparations.Items))
		for _, prep := range preparations.Items {
			releases = append(releases, scanReleaseOf(prep))
		}
		maps.Copy(inUse, scan.InUse(releases, named...))
	}
	return inUse, nil
}

// scan scans the due images of inUse, one call of the scanner per registry
// connection, and records each result in records. It returns the images
// that failed. An error that prevents any scan is an *errScanBlocked.
func (r *ScanReconciler) scan(
	ctx context.Context, product *deliveryv1alpha1.Product, scanning *deliveryv1alpha1.KitchenScanning,
	inUse map[string]oci.Reference, due []string, records map[string]scan.Record, now time.Time,
) ([]scan.Failure, error) {
	state, err := r.Signing.State(ctx)
	if err != nil {
		return nil, &errScanBlocked{reason: deliveryv1alpha1.ReasonSigningKeyNotReady, after: scannerRetryInterval,
			err: fmt.Errorf("waiting for the signing key: %w", err)}
	}
	dbs, err := r.databases(ctx, scanning)
	if err != nil {
		return nil, &errScanBlocked{reason: deliveryv1alpha1.ReasonScanFailed, after: scanRetryInterval, err: err}
	}
	scope, err := r.Registries.Scope(ctx, product.Namespace, product.Spec.RegistryRefs)
	if err != nil {
		return nil, &errScanBlocked{reason: registry.Reason(err, deliveryv1alpha1.ReasonScanFailed), after: scanRetryInterval, err: err}
	}

	// The images of a Product lie below its vendor location, normally one
	// Registry; a call of the scanner covers the images of one connection.
	var failures []scan.Failure
	var targets []*scan.Target
	for _, digest := range due {
		ref := inUse[digest]
		conn, err := scope.Connect(ref.OCIRepositoryReference())
		if err != nil {
			failures = append(failures, scan.Failure{Digest: digest, Err: err})
			continue
		}
		i := slices.IndexFunc(targets, func(t *scan.Target) bool { return t.Client == conn.Client })
		if i < 0 {
			targets = append(targets, &scan.Target{Client: conn.Client, Connection: scannerConnection(conn)})
			i = len(targets) - 1
		}
		targets[i].Images = append(targets[i].Images, ref)
	}

	pipeline := scan.Pipeline{Scanner: r.Scanner, Platform: r.Platform, Signer: state.Signer, Trust: state.Trust, Timeout: scanTimeout}
	logger := log.FromContext(ctx)
	for _, target := range targets {
		logger.Info("Scanning images", "product", product.Name, "images", len(target.Images), "database", dbs.DB.Ref)
		result, err := pipeline.Run(ctx, *target, dbs, now)
		if unavailable, ok := errors.AsType[*scanner.UnavailableError](err); ok {
			return nil, &errScanBlocked{reason: deliveryv1alpha1.ReasonScannerUnavailable,
				after: cmp.Or(unavailable.RetryAfter, scannerRetryInterval), err: err}
		}
		if err != nil {
			return nil, &errScanBlocked{reason: deliveryv1alpha1.ReasonScanFailed, after: scanRetryInterval, err: err}
		}
		logger.Info("Scanned images", "product", product.Name, "images", len(target.Images), "failed", len(result.Failures))

		if len(result.Records) > 0 {
			setScanner(product, result)
		}
		for _, rec := range result.Records {
			records[rec.Digest] = rec
		}
		failures = append(failures, result.Failures...)
	}
	return failures, nil
}

// databases resolves the Kitchen's vulnerability databases to digests,
// through the Registries its scanning names in the install namespace.
func (r *ScanReconciler) databases(ctx context.Context, scanning *deliveryv1alpha1.KitchenScanning) (scan.Databases, error) {
	scope, err := r.Registries.Scope(ctx, r.InstallNamespace, scanning.RegistryRefs)
	if err != nil {
		return scan.Databases{}, fmt.Errorf("connecting to the vulnerability database: %w", err)
	}
	connect := func(repository string) (oci.Client, scanner.Connection, error) {
		conn, err := scope.Connect(repository)
		return conn.Client, scannerConnection(conn), err
	}
	dbs, javaErr, err := scan.ResolveDatabases(ctx, connect, scanning.EffectiveDBRepository(), scanning.EffectiveJavaDBRepository())
	if javaErr != nil {
		log.FromContext(ctx).Info("Scanning without the Java database", "error", javaErr.Error())
	}
	return dbs, err
}

// scannerConnection maps a connection to a registry to how the scanner
// reaches it.
func scannerConnection(conn registry.Connection) scanner.Connection {
	return scanner.Connection{
		Host:                 conn.Host,
		Username:             conn.Credential.Username,
		Password:             conn.Credential.Password,
		RefreshToken:         conn.Credential.RefreshToken,
		AccessToken:          conn.Credential.AccessToken,
		PlainHTTP:            conn.PlainHTTP,
		CertificateAuthority: conn.CertificateAuthority,
		ClientCertificate:    conn.ClientCertificate,
		ClientKey:            conn.ClientKey,
	}
}

// scanReleaseOf maps a Preparation to the release scan.InUse picks from.
func scanReleaseOf(prep deliveryv1alpha1.Preparation) scan.Release {
	release := scan.Release{
		Name:    prep.Name,
		Created: prep.CreationTimestamp.Time,
		Ready:   meta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)}
	for _, img := range prep.Spec.Images {
		if img.Vendored == "" || img.Digest == "" {
			continue
		}
		ref, err := oci.Parse(img.Vendored)
		if err != nil {
			continue
		}
		ref.Tag, ref.Digest = "", img.Digest
		release.Images = append(release.Images, ref)
	}
	return release
}

// scanRecordsOf returns the Product's recorded scans of the images in use,
// by digest.
func scanRecordsOf(product *deliveryv1alpha1.Product, inUse map[string]oci.Reference) map[string]scan.Record {
	records := map[string]scan.Record{}
	if product.Status.Vulnerabilities == nil {
		return records
	}
	for _, s := range product.Status.Vulnerabilities.Images {
		if _, ok := inUse[s.Digest]; !ok {
			continue
		}
		records[s.Digest] = scan.Record{
			Digest:     s.Digest,
			Repository: s.Repository,
			Platform:   s.Platform,
			ScannedAt:  s.ScannedTime.Time,
			SBOM:       scan.SBOM{Digest: s.SBOM.Digest, Origin: sbom.Origin(s.SBOM.Origin)},
			Report:     s.Report,
			Summary: vulnerability.Summary{
				Critical: s.Summary.Critical, High: s.Summary.High, Medium: s.Summary.Medium,
				Low: s.Summary.Low, Unknown: s.Summary.Unknown, Fixable: s.Summary.Fixable,
			},
		}
	}
	return records
}

// setScanRecords writes records to the Product's status, as many as it
// lists.
func setScanRecords(product *deliveryv1alpha1.Product, records map[string]scan.Record) {
	if len(records) == 0 && product.Status.Vulnerabilities == nil {
		return
	}
	if product.Status.Vulnerabilities == nil {
		product.Status.Vulnerabilities = &deliveryv1alpha1.ProductVulnerabilities{}
	}
	latest := scan.Latest(records, maxImageScans)
	images := make([]deliveryv1alpha1.ImageScan, 0, len(latest))
	for _, rec := range latest {
		images = append(images, deliveryv1alpha1.ImageScan{
			Digest:      rec.Digest,
			Repository:  rec.Repository,
			Platform:    rec.Platform,
			ScannedTime: metav1.Time{Time: rec.ScannedAt},
			SBOM:        deliveryv1alpha1.ImageSBOM{Digest: rec.SBOM.Digest, Origin: deliveryv1alpha1.SBOMOrigin(rec.SBOM.Origin)},
			Report:      rec.Report,
			Summary: deliveryv1alpha1.VulnerabilitySummary{
				Critical: rec.Summary.Critical, High: rec.Summary.High, Medium: rec.Summary.Medium,
				Low: rec.Summary.Low, Unknown: rec.Summary.Unknown, Fixable: rec.Summary.Fixable,
			},
		})
	}
	product.Status.Vulnerabilities.Images = images
}

// setScanner records the scanner and the database of the latest scan.
func setScanner(product *deliveryv1alpha1.Product, result scan.Result) {
	if product.Status.Vulnerabilities == nil {
		product.Status.Vulnerabilities = &deliveryv1alpha1.ProductVulnerabilities{}
	}
	product.Status.Vulnerabilities.Scanner = deliveryv1alpha1.VulnerabilityScanner{
		Name:                result.Scanner.Name,
		Version:             result.Scanner.Version,
		Database:            result.Database.Ref,
		DatabaseUpdatedTime: &metav1.Time{Time: result.Database.UpdatedAt},
	}
}

// markScanned sets the Scanned condition of the Product as scan judges its
// scans.
func markScanned(product *deliveryv1alpha1.Product, scanning *deliveryv1alpha1.KitchenScanning, inUse int, failures []scan.Failure, now time.Time) {
	status := scan.Status{InUse: inUse, Failures: failures}
	if v := product.Status.Vulnerabilities; v != nil {
		status.Database = v.Scanner.Database
		if v.Scanner.DatabaseUpdatedTime != nil {
			status.DatabaseBuilt = v.Scanner.DatabaseUpdatedTime.Time
		}
		for _, img := range v.Images {
			status.Records = append(status.Records, scan.Record{Digest: img.Digest, ScannedAt: img.ScannedTime.Time})
		}
	}
	interval := time.Duration(scanning.EffectiveIntervalSeconds()) * time.Second
	maxDatabaseAge := time.Duration(scanning.EffectiveMaxDatabaseAgeSeconds()) * time.Second

	health := status.Health(maxDatabaseAge, 2*interval, now)
	if health.OK() {
		conditions.MarkTrue(product, deliveryv1alpha1.ConditionTypeScanned, scannedReasons[health.Reason], health.Message)
		return
	}
	conditions.MarkFalse(product, deliveryv1alpha1.ConditionTypeScanned, scannedReasons[health.Reason], limitMessage(health.Message))
}

// markScanBlocked records on the Product why no image could be scanned.
func markScanBlocked(product *deliveryv1alpha1.Product, blocked *errScanBlocked) {
	status := metav1.ConditionFalse
	if blocked.reason == deliveryv1alpha1.ReasonSigningKeyNotReady {
		status = metav1.ConditionUnknown
	}
	conditions.Set(product, deliveryv1alpha1.ConditionTypeScanned, status, blocked.reason, limitMessage(blocked.Error()))
}

// limitMessage cuts msg to the length a condition message allows.
func limitMessage(msg string) string {
	const maxMessage = 32768
	if len(msg) <= maxMessage {
		return msg
	}
	return msg[:maxMessage-3] + "..."
}

// clock returns the current time.
func (r *ScanReconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// patchStatus writes the scan status product took on in this reconcile,
// unless it is unchanged, and returns how to requeue: as the reconcile
// decided, or soon when the patch conflicts. The Product controller writes
// the rest of the status; the optimistic lock keeps either from overwriting
// the other's writes.
func (r *ScanReconciler) patchStatus(
	ctx context.Context, product *deliveryv1alpha1.Product, before *deliveryv1alpha1.ProductStatus, result ctrl.Result, err error,
) (ctrl.Result, error) {
	if equality.Semantic.DeepEqual(*before, product.Status) {
		return result, err
	}

	base := product.DeepCopy()
	base.Status = *before
	perr := r.Status().Patch(ctx, product, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	switch {
	case perr == nil, apierrors.IsNotFound(perr):
		return result, err
	case apierrors.IsConflict(perr) && err == nil:
		log.FromContext(ctx).V(1).Info("Product changed during the scan, reconciling it again")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	default:
		return ctrl.Result{}, errors.Join(err, fmt.Errorf("failed to patch Product status: %w", perr))
	}
}

// productOfOrder maps an object that names an Order to the Order's Product.
func (r *ScanReconciler) productOfOrder(ctx context.Context, namespace, orderName string) []reconcile.Request {
	order := &deliveryv1alpha1.Order{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: orderName}, order); err != nil {
		return nil
	}
	return []reconcile.Request{{Namespace: namespace, Name: order.Spec.ProductRef.Name}}
}

// enqueueProductOfPreparation maps a Preparation to the Product of its
// Order.
func (r *ScanReconciler) enqueueProductOfPreparation(ctx context.Context, obj client.Object) []reconcile.Request {
	prep, ok := obj.(*deliveryv1alpha1.Preparation)
	if !ok {
		return nil
	}
	return r.productOfOrder(ctx, prep.Namespace, prep.Spec.OrderName)
}

// enqueueProductOfServing maps a Serving to the Product of its Order.
func (r *ScanReconciler) enqueueProductOfServing(ctx context.Context, obj client.Object) []reconcile.Request {
	serving, ok := obj.(*deliveryv1alpha1.Serving)
	if !ok {
		return nil
	}
	return r.productOfOrder(ctx, serving.Namespace, serving.Spec.OrderName)
}

// enqueueVendoringProducts enqueues every Product that vendors its images,
// so that a change of the Kitchen's scanning reaches them.
func (r *ScanReconciler) enqueueVendoringProducts(ctx context.Context, _ client.Object) []reconcile.Request {
	products := &deliveryv1alpha1.ProductList{}
	if err := r.List(ctx, products); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Products")
		return nil
	}
	var reqs []reconcile.Request
	for i := range products.Items {
		if products.Items[i].Spec.Images.EffectivePolicy() == deliveryv1alpha1.ImagePolicyVendor ||
			products.Items[i].Status.Vulnerabilities != nil {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&products.Items[i])})
		}
	}
	return reqs
}

// preparationReadyChanged passes the Preparation events after which the
// images in use may differ: creates, deletes, and a change of Ready.
var preparationReadyChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldPrep, okOld := e.ObjectOld.(*deliveryv1alpha1.Preparation)
		newPrep, okNew := e.ObjectNew.(*deliveryv1alpha1.Preparation)
		return okOld && okNew &&
			meta.IsStatusConditionTrue(oldPrep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) !=
				meta.IsStatusConditionTrue(newPrep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
	},
	GenericFunc: func(event.GenericEvent) bool { return false },
}

// servingPreparationsChanged passes the Serving events after which the
// images in use may differ: creates, deletes, and a change of the promoted,
// target or deployed Preparation.
var servingPreparationsChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldServing, okOld := e.ObjectOld.(*deliveryv1alpha1.Serving)
		newServing, okNew := e.ObjectNew.(*deliveryv1alpha1.Serving)
		return okOld && okNew &&
			(oldServing.Spec.PreparationName != newServing.Spec.PreparationName ||
				oldServing.Status.TargetPreparationName != newServing.Status.TargetPreparationName ||
				oldServing.DeployedPreparationName() != newServing.DeployedPreparationName())
	},
	GenericFunc: func(event.GenericEvent) bool { return false },
}

// SetupWithManager sets up the controller with the Manager. It lists Orders
// and Preparations by the indexes the Order and Preparation controllers
// register.
func (r *ScanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Status writes, its own and the Product controller's, need no scan.
		For(&deliveryv1alpha1.Product{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&deliveryv1alpha1.Preparation{}, handler.EnqueueRequestsFromMapFunc(r.enqueueProductOfPreparation),
			builder.WithPredicates(preparationReadyChanged)).
		Watches(&deliveryv1alpha1.Serving{}, handler.EnqueueRequestsFromMapFunc(r.enqueueProductOfServing),
			builder.WithPredicates(servingPreparationsChanged)).
		Watches(&deliveryv1alpha1.Kitchen{}, handler.EnqueueRequestsFromMapFunc(r.enqueueVendoringProducts),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("scan").
		WithOptions(controller.Options{MaxConcurrentReconciles: scanConcurrency}).
		Complete(r)
}
