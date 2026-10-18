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
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/index"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/status"
	"github.com/kokumi-dev/kokumi/internal/version"
)

const (
	initialCommitMessage   = "Initial commit"
	automatedCommitMessage = "Automatically generated"
)

const (
	// orderConcurrency is how many Orders reconcile at a time. Renders wait
	// on registries more than on the CPU, above all when they vendor images.
	orderConcurrency = 4
	// renderTimeout bounds one render, so a registry that stalls cannot hold
	// a worker forever. A retry resumes vendoring where it stopped, since
	// copies skip what the destination holds already.
	renderTimeout = 30 * time.Minute
)

// OrderReconciler reconciles an Order object.
type OrderReconciler struct {
	client.Client
	// APIReader bypasses the cache. Before a render, it confirms that the
	// cached Order is the latest and reads the Preparation whose images the
	// render locks, which the cache may not hold yet.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Pipeline  *artifact.Pipeline
	// Registries connects to the source, the images and the vendor location
	// through the Product's registryRefs, and to the destination through
	// the Order's.
	Registries registryConnector
	// Recorder records Events on Orders.
	Recorder events.EventRecorder
	// InstallNamespace holds the Kitchen, whose image mirrors renders use.
	InstallNamespace string
	// BuilderID identifies this controller as the builder of the artifacts
	// it renders.
	BuilderID string
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *OrderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Order", "namespace", req.Namespace, "name", req.Name)

	order := &deliveryv1alpha1.Order{}

	if err := r.Get(ctx, req.NamespacedName, order); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Order resource not found, ignoring")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Order")

		return ctrl.Result{}, fmt.Errorf("failed to get Order: %w", err)
	}

	if !order.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, order)
	}

	if !controllerutil.ContainsFinalizer(order, deliveryv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(order, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, order); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcileOrder(ctx, order)
}

// reconcileOrder merges the Order over its Product and delegates artifact
// work to the render pipeline. An Order whose Product is missing or not ready
// waits; the Product watch reconciles it again.
func (r *OrderReconciler) reconcileOrder(ctx context.Context, order *deliveryv1alpha1.Order) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	statusUpdater := status.NewOrderUpdater(r.Client)

	product := &deliveryv1alpha1.Product{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: order.Spec.ProductRef.Name}, product); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get Product %q: %w", order.Spec.ProductRef.Name, err)
		}
		logger.Info("Waiting for Product", "product", order.Spec.ProductRef.Name)
		return ctrl.Result{}, statusUpdater.Waiting(ctx, order, deliveryv1alpha1.ReasonProductNotFound,
			fmt.Sprintf("Product %q not found", order.Spec.ProductRef.Name))
	}

	if !product.DeletionTimestamp.IsZero() {
		logger.Info("Waiting for Product deletion", "product", product.Name)
		return ctrl.Result{}, statusUpdater.Waiting(ctx, order, deliveryv1alpha1.ReasonProductDeleting,
			fmt.Sprintf("Product %q is being deleted", product.Name))
	}

	if !resolve.ProductReady(product) {
		logger.Info("Waiting for Product to become ready", "product", product.Name)
		msg := fmt.Sprintf("Product %q is not ready", product.Name)
		if ready := meta.FindStatusCondition(product.Status.Conditions, deliveryv1alpha1.ConditionTypeReady); ready != nil && ready.Message != "" {
			msg += ": " + ready.Message
		}
		return ctrl.Result{}, statusUpdater.Waiting(ctx, order, deliveryv1alpha1.ReasonProductNotReady, msg)
	}

	effective, err := resolve.ForProduct(product, order)
	if err != nil {
		if uerr := statusUpdater.Failed(ctx, order, err); uerr != nil {
			logger.Error(uerr, "Failed to update Order status")
		}
		// Retrying does not help; a change to the Order or Product does.
		return ctrl.Result{}, reconcile.TerminalError(err)
	}

	return r.reconcileRender(ctx, order, effective)
}

// ensureServing creates the Order's Serving under Automatic promotion so the
// Serving controller can pick up new Preparations. It never changes an
// existing Serving; the promotion mode is read from the Order directly.
func (r *OrderReconciler) ensureServing(ctx context.Context, order *deliveryv1alpha1.Order) error {
	if order.EffectivePromotionMode() != deliveryv1alpha1.PromotionModeAutomatic {
		return nil
	}

	serving := &deliveryv1alpha1.Serving{}
	err := r.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: order.Name}, serving)
	if err == nil || !apierrors.IsNotFound(err) {
		return client.IgnoreNotFound(err)
	}

	serving = &deliveryv1alpha1.Serving{
		Name:      order.Name,
		Namespace: order.Namespace,
		Labels:    map[string]string{deliveryv1alpha1.LabelOrder: order.Name},
		Spec:      deliveryv1alpha1.ServingSpec{OrderName: order.Name},
	}
	if err := r.Create(ctx, serving); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create Serving: %w", err)
	}
	log.FromContext(ctx).Info("Created Serving", "name", serving.Name)
	return nil
}

// reconcileRender delegates artifact work to the pipeline and then handles CRD
func (r *OrderReconciler) reconcileRender(ctx context.Context, order *deliveryv1alpha1.Order, effective *resolve.EffectiveSpec) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	statusUpdater := status.NewOrderUpdater(r.Client)

	destination := artifact.DefaultDestination(order.Namespace, order.Name)
	if order.Spec.Destination != nil && order.Spec.Destination.URL != "" {
		destination = order.Spec.Destination.URL
	}

	// The Product's Registries connect to what the Product owns: the
	// source, the images and their vendor location. The Order's connect to
	// its destination.
	productScope, err := registryScope(ctx, r.Client, r.Registries, order.Namespace, effective.RegistryRefs)
	if err != nil {
		return r.registryFailed(ctx, order, err)
	}
	orderScope, err := registryScope(ctx, r.Client, r.Registries, order.Namespace, order.Spec.RegistryRefs)
	if err != nil {
		return r.registryFailed(ctx, order, err)
	}

	sourceConn, err := productScope.Connect(effective.Source.URL)
	if err != nil {
		return r.registryFailed(ctx, order, err)
	}
	destConn, err := orderScope.Connect(destination)
	if err != nil {
		return r.registryFailed(ctx, order, err)
	}

	intent, err := resolve.NewIntent(order, effective, destination).Canonical()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate spec hash: %w", err)
	}

	specHash := resolve.CalculateSpecHash(intent)

	if order.Status.LatestConfigHash == specHash && order.Status.LatestPreparationName != "" {
		return ctrl.Result{}, r.reconcileUpToDate(ctx, order, effective, specHash, productScope, sourceConn, destination, destConn)
	}

	// A cache that lags behind the status of the previous render would
	// render it again, without its parent and image locks. The watch event
	// of the newer Order reconciles it again.
	latest, err := r.isLatest(ctx, order)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !latest {
		logger.Info("Skipping render until the cache holds the latest Order")
		return ctrl.Result{}, nil
	}

	exposes, err := exposesPrivateSource(ctx, effective, productScope, sourceConn, destConn)
	if err != nil {
		return ctrl.Result{}, err
	}
	if exposes {
		logger.Info("Refusing to push a private source without credentials", "source", effective.Source.URL, "destination", destination)
		return ctrl.Result{}, statusUpdater.Blocked(ctx, order, deliveryv1alpha1.ReasonCredentialsRequired,
			fmt.Sprintf("The source is private, and pushing its render to %s, which Kokumi writes without credentials, would make it public: "+
				"list a Registry with credentials for %s in spec.registryRefs", destination, destination))
	}

	if err := statusUpdater.Processing(ctx, order, specHash); err != nil {
		return ctrl.Result{}, err
	}

	parentDigest := order.Status.LatestArtifactDigest

	userMessage, messageProvided := order.Annotations[deliveryv1alpha1.AnnotationCommitMessage]
	commitMessage := defaultCommitMessage(userMessage, messageProvided, parentDigest == "")

	spec, err := effective.ToArtifactSpec()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to convert effective spec: %w", err)
	}

	extraAnnotations, err := approval.PolicyAnnotations(order.ApprovalPolicy())
	if err != nil {
		return ctrl.Result{}, err
	}

	imageOpts, err := r.imageOptions(ctx, order, effective, spec.Images)
	if err != nil {
		if uerr := statusUpdater.Failed(ctx, order, err); uerr != nil {
			logger.Error(uerr, "Failed to update Order status")
		}
		return ctrl.Result{}, err
	}

	renderer := rendererFor(effective.Render, r.BuilderID)
	config := artifactConfig(order, effective, intent, specHash, renderer)

	renderCtx, cancel := context.WithTimeout(ctx, renderTimeout)
	defer cancel()

	result, err := r.Pipeline.Render(renderCtx, artifact.RenderRequest{
		Source:           artifact.Source{OCI: effective.Source.URL, Version: spec.Source.Version, Digest: spec.Source.Digest},
		Destination:      artifact.Destination{OCI: destination},
		Product:          productScope,
		Order:            orderScope,
		Render:           spec.Render,
		Patches:          spec.Patches,
		Edits:            spec.Edits,
		Images:           imageOpts,
		Name:             order.Name,
		Namespace:        order.Namespace,
		Description:      commitMessage,
		ParentDigest:     parentDigest,
		ExtraAnnotations: extraAnnotations,
		ArtifactType:     oci.PreparationArtifactType,
		Config:           config,
	})
	if err != nil {
		if reason := registry.Reason(err, ""); reason != "" {
			logger.Info("Order cannot render with its registries", "reason", reason, "error", err.Error())
			return ctrl.Result{}, statusUpdater.Blocked(ctx, order, reason, err.Error())
		}
		logger.Error(err, "Failed to process Order")
		if uerr := statusUpdater.Failed(ctx, order, err); uerr != nil {
			logger.Error(uerr, "Failed to update Order status")
		}

		return ctrl.Result{}, err
	}

	preparation, err := r.createPreparation(ctx, order, result, renderer, commitMessage, parentDigest, specHash, destConn.RegistryName())
	if err != nil {
		logger.Error(err, "Failed to create Preparation")
		if uerr := statusUpdater.Failed(ctx, order, fmt.Errorf("failed to create revision: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Order status")
		}

		return ctrl.Result{}, err
	}

	logger.Info("Created Preparation", "revision", preparation.Name)
	r.recordUnresolvedImages(order, preparation, result.Images)

	// One status write: a failed second write would leave the render
	// unrecorded, and the next reconcile would render it again.
	access := registryAccess(ctx, effective, productScope, sourceConn, destination, destConn, preparation)
	if err := statusUpdater.WithRegistryAccess(access).Ready(ctx, order, specHash, preparation.Name, result.DestRef.Digest,
		fmt.Sprintf("Successfully pushed to %s", result.DestRef)); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.ensureServing(ctx, order); err != nil {
		return ctrl.Result{}, err
	}

	// Remove the transient commit-message annotation now that it has been consumed.
	if _, hasAnnotation := order.Annotations[deliveryv1alpha1.AnnotationCommitMessage]; hasAnnotation {
		patch := client.MergeFrom(order.DeepCopy())
		delete(order.Annotations, deliveryv1alpha1.AnnotationCommitMessage)
		if err := r.Patch(ctx, order, patch); err != nil {
			logger.Error(err, "Failed to remove commit-message annotation from Order")
		}
	}

	return ctrl.Result{}, nil
}

// reconcileUpToDate handles an Order whose intent its latest Preparation
// already renders.
func (r *OrderReconciler) reconcileUpToDate(
	ctx context.Context, order *deliveryv1alpha1.Order, effective *resolve.EffectiveSpec, specHash string,
	productScope registry.Scope, sourceConn registry.Connection, destination string, destConn registry.Connection,
) error {
	log.FromContext(ctx).Info("Configuration is up-to-date, skipping reconciliation")
	// registryRefs are not part of the intent: a change records how the
	// Order connects now without rendering again.
	latest, err := r.latestPreparation(ctx, order)
	if err != nil {
		return err
	}
	access := registryAccess(ctx, effective, productScope, sourceConn, destination, destConn, latest)
	statusUpdater := status.NewOrderUpdater(r.Client).WithRegistryAccess(access)
	if waitedForProduct(order) {
		err = statusUpdater.Ready(ctx, order, specHash, order.Status.LatestPreparationName, order.Status.LatestArtifactDigest,
			fmt.Sprintf("Preparation %s is up to date", order.Status.LatestPreparationName))
	} else {
		err = statusUpdater.Observed(ctx, order)
	}
	if err != nil {
		return err
	}
	return r.ensureServing(ctx, order)
}

// latestPreparation returns the Order's latest Preparation, or nil when it
// no longer exists.
func (r *OrderReconciler) latestPreparation(ctx context.Context, order *deliveryv1alpha1.Order) (*deliveryv1alpha1.Preparation, error) {
	latest := &deliveryv1alpha1.Preparation{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: order.Status.LatestPreparationName}, latest); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get latest Preparation: %w", err)
	}
	return latest, nil
}

// exposesPrivateSource reports whether rendering the source into the
// destination would expose it: a private source rendered into a destination
// Kokumi writes without credentials could be read there by anyone.
func exposesPrivateSource(
	ctx context.Context, effective *resolve.EffectiveSpec, productScope registry.Scope, sourceConn, destConn registry.Connection,
) (bool, error) {
	if destConn.Authenticated() {
		return false, nil
	}
	pinned, err := oci.Parse(effective.Source.URL + "@" + effective.SourceDigest)
	if err != nil {
		return false, fmt.Errorf("failed to parse source reference: %w", err)
	}
	return productScope.Private(ctx, sourceConn, pinned), nil
}

// registryFailed records that the Order cannot connect to a registry, for
// the reason err carries, such as RegistryNotFound or SecretInvalid. The
// Registry watch reconciles the Order again once its Registries change.
// Errors without a reason are returned to be retried.
func (r *OrderReconciler) registryFailed(ctx context.Context, order *deliveryv1alpha1.Order, err error) (ctrl.Result, error) {
	reason := registry.Reason(err, "")
	if reason == "" {
		return ctrl.Result{}, fmt.Errorf("failed to connect to the Order's registries: %w", err)
	}
	log.FromContext(ctx).Info("Order cannot connect to its registries", "reason", reason, "error", err.Error())
	return ctrl.Result{}, status.NewOrderUpdater(r.Client).Blocked(ctx, order, reason, err.Error())
}

// waitedForProduct reports whether the Order's Ready condition says it is
// waiting for its Product.
func waitedForProduct(order *deliveryv1alpha1.Order) bool {
	ready := meta.FindStatusCondition(order.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
	return ready != nil && (ready.Reason == deliveryv1alpha1.ReasonProductNotFound ||
		ready.Reason == deliveryv1alpha1.ReasonProductNotReady ||
		ready.Reason == deliveryv1alpha1.ReasonProductDeleting)
}

// defaultCommitMessage returns the effective commit message for a Preparation.
func defaultCommitMessage(message string, messageProvided bool, isInitial bool) string {
	if messageProvided {
		return message
	}
	if isInitial {
		return initialCommitMessage
	}
	return automatedCommitMessage
}

// reconcileDelete removes the finalizer from the Order, allowing garbage collection.
func (r *OrderReconciler) reconcileDelete(ctx context.Context, order *deliveryv1alpha1.Order) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Order")

	if controllerutil.ContainsFinalizer(order, deliveryv1alpha1.Finalizer) {
		logger.Info("Cleaning up Order resources")

		controllerutil.RemoveFinalizer(order, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, order); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// isLatest reports whether order, as read from the cache, is the version the
// API server holds. An Order deleted meanwhile is not.
func (r *OrderReconciler) isLatest(ctx context.Context, order *deliveryv1alpha1.Order) (bool, error) {
	live := &deliveryv1alpha1.Order{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(order), live); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get Order: %w", err)
	}
	return live.ResourceVersion == order.ResourceVersion, nil
}

// imageOptions completes the image options of a render with the location
// the Product vendors images below, the Kitchen's mirrors, and, under Pin
// and Vendor, the images of the Order's latest Preparation, whose digests
// the render keeps.
func (r *OrderReconciler) imageOptions(ctx context.Context, order *deliveryv1alpha1.Order, effective *resolve.EffectiveSpec, opts artifact.ImageOptions) (artifact.ImageOptions, error) {
	opts.VendorURL = effective.ImagesVendorURL

	kitchen := &deliveryv1alpha1.Kitchen{}
	err := r.Get(ctx, client.ObjectKey{Namespace: r.InstallNamespace, Name: deliveryv1alpha1.DefaultKitchenName}, kitchen)
	if err != nil && !apierrors.IsNotFound(err) {
		return opts, fmt.Errorf("failed to get Kitchen: %w", err)
	}
	opts.Mirrors = resolve.ImageMirrors(kitchen)

	if effective.Images.EffectivePolicy() == deliveryv1alpha1.ImagePolicyRecord || order.Status.LatestPreparationName == "" {
		return opts, nil
	}

	parent := &deliveryv1alpha1.Preparation{}
	err = r.APIReader.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: order.Status.LatestPreparationName}, parent)
	switch {
	case err == nil:
		opts.Locked = resolve.LockedImages(parent)
	case !apierrors.IsNotFound(err):
		return opts, fmt.Errorf("failed to get latest Preparation: %w", err)
	}

	return opts, nil
}

// maxRegistryAccess is the most registry locations an Order lists, the
// MaxItems of status.registryAccess.
const maxRegistryAccess = 32

// registryAccess returns the registry locations of the Order for its
// status: the source and the images Kokumi pulls, through the Product's
// Registries; the destination it pushes to, through the Order's; the
// location the images are vendored to; and what Argo CD and the kubelet
// pull, for which they need credentials of their own. latest is the
// Preparation whose images are listed, nil when there is none.
func registryAccess(
	ctx context.Context,
	effective *resolve.EffectiveSpec,
	productScope registry.Scope,
	sourceConn registry.Connection,
	destination string,
	destConn registry.Connection,
	latest *deliveryv1alpha1.Preparation,
) []deliveryv1alpha1.RegistryAccess {
	registries := productScope.Registries()
	registryOf := func(url string) string {
		reg, err := registry.Match(registries, url)
		if err != nil || reg == nil {
			return ""
		}
		return reg.Name
	}

	accessByKey := map[deliveryv1alpha1.RegistryAccess]bool{}
	add := func(url string, op deliveryv1alpha1.RegistryOperation, who deliveryv1alpha1.RegistryClient, registryName string) {
		accessByKey[deliveryv1alpha1.RegistryAccess{URL: url, Operation: op, Client: who, Registry: registryName}] = true
	}

	add(effective.Source.URL, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKokumi, sourceConn.RegistryName())
	add(destination, deliveryv1alpha1.RegistryOperationPush, deliveryv1alpha1.RegistryClientKokumi, destConn.RegistryName())
	add(destination, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientArgoCD, "")

	if latest != nil {
		vendorImages := ""
		if effective.ImagesVendorURL != "" {
			vendorImages = effective.ImagesVendorURL + "/images"
		}
		for _, img := range latest.Spec.Images {
			loc, err := registry.ParseLocation(img.Name)
			if err != nil {
				continue
			}
			upstream := "oci://" + loc.Host
			if reg := registryOf(img.Name); reg != "" {
				upstream = registriesURL(registries, reg)
			}
			add(upstream, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKokumi, registryOf(img.Name))

			if img.Vendored != "" && vendorImages != "" {
				add(vendorImages, deliveryv1alpha1.RegistryOperationPush, deliveryv1alpha1.RegistryClientKokumi, registryOf(vendorImages))
				add(vendorImages, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKubelet, "")
				continue
			}
			add("oci://"+loc.Host, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKubelet, "")
		}
	}

	access := slices.SortedFunc(maps.Keys(accessByKey), func(a, b deliveryv1alpha1.RegistryAccess) int {
		return cmp.Or(cmp.Compare(a.URL, b.URL), cmp.Compare(a.Client, b.Client), cmp.Compare(a.Operation, b.Operation))
	})
	if len(access) > maxRegistryAccess {
		log.FromContext(ctx).Info("Listing only some registry locations of the Order", "listed", maxRegistryAccess, "total", len(access))
		access = access[:maxRegistryAccess]
	}
	return access
}

// registriesURL returns the url of the named Registry among registries.
func registriesURL(registries []deliveryv1alpha1.Registry, name string) string {
	for _, reg := range registries {
		if reg.Name == name {
			return reg.Spec.URL
		}
	}
	return ""
}

// maxEventNote is the longest note events.k8s.io/v1 accepts.
const maxEventNote = 1024

// recordUnresolvedImages records one Warning Event on order for the images
// of preparation that could not be resolved, which only the Record policy
// lets pass. The note lists as many of them as fit; the artifact's config
// blob records them all. Naming the Preparation as the related object keeps
// the Events of two renders apart: client-go merges Events that differ only
// in their note into one series that keeps the first note.
func (r *OrderReconciler) recordUnresolvedImages(order *deliveryv1alpha1.Order, preparation *deliveryv1alpha1.Preparation, imgs []images.Image) {
	var unresolved []string
	for _, img := range imgs {
		if img.Error != "" {
			unresolved = append(unresolved, img.Ref+": "+img.Error)
		}
	}
	if len(unresolved) == 0 {
		return
	}

	note := fmt.Sprintf("Could not resolve %d of %d images: %s", len(unresolved), len(imgs), strings.Join(unresolved, "; "))
	r.Recorder.Eventf(order, preparation, corev1.EventTypeWarning, deliveryv1alpha1.ReasonImageNotResolved, "Render", "%s", truncateNote(note))
}

// truncateNote cuts note to maxEventNote bytes at a rune boundary, marking
// the cut with an ellipsis.
func truncateNote(note string) string {
	if len(note) <= maxEventNote {
		return note
	}

	const ellipsis = "…"
	cut := maxEventNote - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(note[cut]) {
		cut--
	}
	return note[:cut] + ellipsis
}

// createPreparation creates a Preparation for the rendered artifact.
// If a Preparation with the same name already exists it is returned unchanged.
func (r *OrderReconciler) createPreparation(
	ctx context.Context,
	order *deliveryv1alpha1.Order,
	result *artifact.RenderResult,
	renderer deliveryv1alpha1.Renderer,
	commitMessage string,
	parentDigest string,
	configHash string,
	registryName string,
) (*deliveryv1alpha1.Preparation, error) {
	logger := log.FromContext(ctx)
	sourceRef, destRef := result.SourceRef, result.DestRef

	revisionName := fmt.Sprintf("%s-%s", order.Name, destRef.ShortDigest())

	existing := &deliveryv1alpha1.Preparation{}

	err := r.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: revisionName}, existing)
	if err == nil {
		logger.Info("Preparation already exists", "revision", revisionName)
		return existing, nil
	}

	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to check for existing revision: %w", err)
	}

	preparation := &deliveryv1alpha1.Preparation{
		Name:      revisionName,
		Namespace: order.Namespace,
		Labels: map[string]string{
			deliveryv1alpha1.LabelOrder:   order.Name,
			deliveryv1alpha1.LabelVersion: sourceRef.Tag,
		},
		Spec: deliveryv1alpha1.PreparationSpec{
			OrderName: order.Name,
			Source: deliveryv1alpha1.OrderSource{
				URL:        sourceRef.OCIRepositoryReference(),
				BaseDigest: sourceRef.Digest,
			},
			Renderer:   renderer,
			ConfigHash: configHash,
			Artifact: deliveryv1alpha1.Artifact{
				OCIRef: destRef.OCIString(),
				Digest: destRef.Digest,
			},
			CommitMessage:  commitMessage,
			ParentDigest:   parentDigest,
			GitSource:      deliveryv1alpha1.GitSource{Repo: result.SCM.Repo, Tag: result.SCM.Tag, CommitHash: result.SCM.CommitHash},
			ApprovalPolicy: order.ApprovalPolicy().DeepCopy(),
			Images:         preparationImages(result.Images),
		},
	}

	if registryName != "" {
		preparation.Spec.Artifact.RegistryRef = &corev1.LocalObjectReference{Name: registryName}
	}

	if err := controllerutil.SetControllerReference(order, preparation, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set owner reference: %w", err)
	}

	if err := r.Create(ctx, preparation); err != nil {
		return nil, fmt.Errorf("failed to create Preparation: %w", err)
	}

	return preparation, nil
}

// preparationImages converts the images of a render into their Preparation
// form, which leaves out why an image could not be resolved.
func preparationImages(imgs []images.Image) []deliveryv1alpha1.PreparationImage {
	if len(imgs) == 0 {
		return nil
	}

	out := make([]deliveryv1alpha1.PreparationImage, 0, len(imgs))
	for _, img := range imgs {
		out = append(out, deliveryv1alpha1.PreparationImage{
			Ref:       img.Ref,
			Name:      img.Name,
			Digest:    img.Digest,
			MediaType: img.MediaType,
			Vendored:  img.Vendored,
		})
	}
	return out
}

// rendererFor returns the identity of the renderer that processes render,
// attributed to the given builder.
func rendererFor(render *deliveryv1alpha1.Render, builderID string) deliveryv1alpha1.Renderer {
	renderer := deliveryv1alpha1.Renderer{
		Type:    deliveryv1alpha1.RenderTypeManifest,
		Version: "v" + version.Version,
		Builder: cmp.Or(builderID, version.FallbackBuilderID()),
	}

	if render != nil && render.Helm != nil {
		renderer.Type = deliveryv1alpha1.RenderTypeHelm
		renderer.Version = version.HelmVersion()
	}

	return renderer
}

// artifactConfig builds the part of the artifact config the Order controller
// knows; the pipeline fills in the rest.
func artifactConfig(order *deliveryv1alpha1.Order, effective *resolve.EffectiveSpec, intent []byte, configHash string, renderer deliveryv1alpha1.Renderer) artifact.Config {
	config := artifact.Config{
		Order:      &artifact.ConfigObject{Namespace: order.Namespace, Name: order.Name, UID: order.UID},
		Product:    &artifact.ConfigProduct{Namespace: order.Namespace, Name: effective.Product.Name, ConfigHash: effective.Product.ConfigHash},
		ConfigHash: configHash,
		Renderer:   configRenderer(renderer),
		Intent:     intent,
	}

	if verified := effective.SourceVerified; verified != nil {
		config.Source.Verified = &artifact.ConfigVerification{
			Provider: string(verified.Provider),
			KeyID:    verified.KeyID,
			Issuer:   verified.Issuer,
			Subject:  verified.Subject,
		}
	}

	return config
}

// configRenderer converts a Renderer into its artifact config form.
func configRenderer(renderer deliveryv1alpha1.Renderer) artifact.ConfigRenderer {
	return artifact.ConfigRenderer{Type: string(renderer.Type), Version: renderer.Version, Builder: renderer.Builder}
}

func (r *OrderReconciler) enqueueOrdersForProduct() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		orders, err := index.OrdersForProduct(ctx, r.Client, obj.GetNamespace(), obj.GetName())
		if err != nil {
			log.FromContext(ctx).Error(err, "Failed to list Orders of Product", "product", obj.GetName())
			return nil
		}

		reqs := make([]reconcile.Request, 0, len(orders))
		for _, order := range orders {
			reqs = append(reqs, reconcile.Request{Namespace: order.Namespace, Name: order.Name})
		}
		return reqs
	})
}

// enqueueOrdersForRegistry enqueues the Orders that connect through the
// Registry: those that list it, and the Orders of the Products that list
// it, so that they connect again when it changes.
func (r *OrderReconciler) enqueueOrdersForRegistry() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		logger := log.FromContext(ctx)

		orders, err := index.OrdersForRegistry(ctx, r.Client, obj.GetNamespace(), obj.GetName())
		if err != nil {
			logger.Error(err, "Failed to list Orders of Registry", "registry", obj.GetName())
			return nil
		}
		products, err := index.ProductsForRegistry(ctx, r.Client, obj.GetNamespace(), obj.GetName())
		if err != nil {
			logger.Error(err, "Failed to list Products of Registry", "registry", obj.GetName())
			return nil
		}
		for _, product := range products {
			ofProduct, err := index.OrdersForProduct(ctx, r.Client, product.Namespace, product.Name)
			if err != nil {
				logger.Error(err, "Failed to list Orders of Product", "product", product.Name)
				return nil
			}
			orders = append(orders, ofProduct...)
		}

		seen := map[client.ObjectKey]bool{}
		reqs := make([]reconcile.Request, 0, len(orders))
		for _, order := range orders {
			key := client.ObjectKeyFromObject(&order)
			if seen[key] {
				continue
			}
			seen[key] = true
			reqs = append(reqs, reconcile.Request{NamespacedName: key})
		}
		return reqs
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *OrderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Order{}).
		Owns(&deliveryv1alpha1.Preparation{}).
		Watches(&deliveryv1alpha1.Product{}, r.enqueueOrdersForProduct()).
		Watches(&deliveryv1alpha1.Registry{}, r.enqueueOrdersForRegistry()).
		Named("order").
		WithOptions(controller.Options{MaxConcurrentReconciles: orderConcurrency}).
		Complete(r)
}
