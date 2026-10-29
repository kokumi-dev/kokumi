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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
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

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/promotion"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/resources"
)

const (
	// orderConcurrency is how many Orders reconcile at a time. Renders wait
	// on registries more than on the CPU, above all when they vendor images.
	orderConcurrency = 4
	// renderTimeout bounds one render, so a registry that stalls cannot hold
	// a worker forever. A retry resumes vendoring where it stopped, since
	// copies skip what the destination holds already.
	renderTimeout = 30 * time.Minute
	// maxRegistryAccess is the most registry locations an Order lists, the
	// MaxItems of status.registryAccess.
	maxRegistryAccess = 32
	// maxEventNote is the longest note events.k8s.io/v1 accepts.
	maxEventNote = 1024
)

// orderRegistriesField indexes Orders by the Registries their
// spec.registryRefs list.
const orderRegistriesField = "registryRefNames"

// orderUpstreamsField indexes Orders by the upstream Orders their
// spec.promotion.after lists. A selectable field cannot point into a list.
const orderUpstreamsField = "upstreamOrderNames"

// orderPipeline renders the intent of an Order into an artifact.
// *artifact.Pipeline implements it.
type orderPipeline interface {
	Render(ctx context.Context, req artifact.RenderRequest) (*artifact.RenderResult, error)
}

// orderRegistries connects through the Registries a Product or an Order
// lists. *registry.Resolver implements it.
type orderRegistries interface {
	Scope(ctx context.Context, namespace string, refs []corev1.LocalObjectReference) (registry.Scope, error)
}

// OrderReconciler reconciles an Order object.
type OrderReconciler struct {
	client.Client
	// APIReader bypasses the cache. Before a render, it confirms that the
	// cached Order is the latest and reads the Preparation whose images the
	// render locks, which the cache may not hold yet. It reads the Serving
	// the Order adopts, which the Serving controller writes meanwhile.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Pipeline  orderPipeline
	// Registries connects to the source, the images and the vendor location
	// through the Product's registryRefs, and to the destination through
	// the Order's.
	Registries orderRegistries
	// Recorder records Events on Orders.
	Recorder events.EventRecorder
	// InstallNamespace holds the Kitchen, whose image mirrors renders use.
	InstallNamespace string
	// BuilderID identifies this controller as the builder of the artifacts
	// it renders.
	BuilderID string
}

// orderConnections are the registry locations an Order renders from and to,
// with the connections Kokumi uses for them.
type orderConnections struct {
	// product is the Scope of the Product's Registries, which connect to
	// what the Product owns: the source, the images and their vendor
	// location.
	product registry.Scope
	// order is the Scope of the Order's Registries, which connect to its
	// destination.
	order       registry.Scope
	source      registry.Connection
	destination string
	destConn    registry.Connection
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=kitchens,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile renders the intent of the Order into a Preparation whenever it
// changes, and keeps the Order's Serving.
func (r *OrderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
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

	before := order.Status.DeepCopy()
	defer func() { result, err = r.patchStatus(ctx, order, before, result, err) }()

	if !order.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, order)
	}

	if !controllerutil.ContainsFinalizer(order, deliveryv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(order, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, order); err != nil {
			return ctrl.Result{}, err
		}
	}

	result, err = r.reconcileOrder(ctx, order)

	// Every outcome keeps the Serving, so that a Serving the first promote
	// creates while the Order fails still goes with the Order.
	if serr := r.ensureServing(ctx, order); serr != nil {
		return result, errors.Join(err, serr)
	}
	return result, err
}

// reconcileOrder merges the Order over its Product and renders the intent
// they form, unless its latest Preparation renders it already. An Order
// whose Product is missing or not ready waits; the Product watch reconciles
// it again. An Order whose upstream stages do not form a valid pipeline
// renders nothing; the Order watch reconciles it again when they change.
func (r *OrderReconciler) reconcileOrder(ctx context.Context, order *deliveryv1alpha1.Order) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	problem, err := r.pipelineProblem(ctx, order)
	if err != nil {
		return ctrl.Result{}, err
	}
	if problem != "" {
		logger.Info("Order is not a valid stage of its pipeline", "problem", problem)
		conditions.MarkFalse(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonInvalidPipeline, problem)
		return ctrl.Result{}, nil
	}

	product, err := r.readyProduct(ctx, order)
	if product == nil || err != nil {
		return ctrl.Result{}, err
	}

	effective, err := resolve.ForProduct(product, order)
	if err != nil {
		conditions.MarkFalse(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProcessingFailed, err.Error())
		// Retrying does not help; a change to the Order or Product does.
		return ctrl.Result{}, reconcile.TerminalError(err)
	}

	conns, err := r.connect(ctx, order, effective)
	if err != nil {
		return r.registryFailed(ctx, order, err)
	}

	intent, err := resolve.NewIntent(order, effective, conns.destination).Canonical()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate spec hash: %w", err)
	}
	specHash := resolve.CalculateSpecHash(intent)

	if order.Status.LatestConfigHash == specHash && order.Status.LatestPreparationName != "" {
		return ctrl.Result{}, r.reconcileUpToDate(ctx, order, effective, conns)
	}

	exposes, err := exposesPrivateSource(ctx, effective, conns)
	if err != nil {
		return ctrl.Result{}, err
	}
	if exposes {
		logger.Info("Refusing to push a private source without credentials", "source", effective.Source.URL, "destination", conns.destination)
		conditions.MarkFalse(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonCredentialsRequired,
			fmt.Sprintf("The source is private, and pushing its render to %s, which Kokumi writes without credentials, would make it public: "+
				"list a Registry with credentials for %s in spec.registryRefs", conns.destination, conns.destination))
		return ctrl.Result{}, nil
	}

	// A render can take minutes, so the Order reports that it is processing
	// first. That status write triggers the reconcile that renders.
	if !conditions.HasReason(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProcessing) {
		conditions.MarkUnknown(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProcessing, "Processing component configuration")
		return ctrl.Result{}, nil
	}

	return r.render(ctx, order, effective, conns, intent, specHash)
}

// pipelineProblem says why the upstream stages of the Order do not form a
// valid pipeline with the Orders of its namespace, "" when they do.
func (r *OrderReconciler) pipelineProblem(ctx context.Context, order *deliveryv1alpha1.Order) (string, error) {
	if len(order.UpstreamStages()) == 0 {
		return "", nil
	}
	orders := &deliveryv1alpha1.OrderList{}
	if err := r.List(ctx, orders, client.InNamespace(order.Namespace)); err != nil {
		return "", fmt.Errorf("failed to list Orders: %w", err)
	}
	pipeline := promotion.Pipeline{}
	for i := range orders.Items {
		pipeline[orders.Items[i].Name] = pipelineOrder(&orders.Items[i])
	}
	// The Order as this reconcile read it, which the cache may lag behind.
	pipeline[order.Name] = pipelineOrder(order)
	return pipeline.Problem(order.Name), nil
}

// pipelineOrder maps order to its stage of a pipeline.
func pipelineOrder(order *deliveryv1alpha1.Order) promotion.PipelineOrder {
	return promotion.PipelineOrder{Product: order.Spec.ProductRef.Name, Upstreams: orderUpstreamNames(order)}
}

// readyProduct returns the Product of the Order once Orders may render from
// it, and nil while the Order waits for it.
func (r *OrderReconciler) readyProduct(ctx context.Context, order *deliveryv1alpha1.Order) (*deliveryv1alpha1.Product, error) {
	logger := log.FromContext(ctx)
	name := order.Spec.ProductRef.Name

	product := &deliveryv1alpha1.Product{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: name}, product); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get Product %q: %w", name, err)
		}
		logger.Info("Waiting for Product", "product", name)
		conditions.MarkFalse(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProductNotFound,
			fmt.Sprintf("Product %q not found", name))
		return nil, nil
	}

	if !product.DeletionTimestamp.IsZero() {
		logger.Info("Waiting for Product deletion", "product", name)
		conditions.MarkFalse(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProductDeleting,
			fmt.Sprintf("Product %q is being deleted", name))
		return nil, nil
	}

	if !resolve.ProductReady(product) {
		logger.Info("Waiting for Product to become ready", "product", name)
		msg := fmt.Sprintf("Product %q is not ready", name)
		if ready := meta.FindStatusCondition(product.Status.Conditions, deliveryv1alpha1.ConditionTypeReady); ready != nil && ready.Message != "" {
			msg += ": " + ready.Message
		}
		conditions.MarkFalse(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProductNotReady, msg)
		return nil, nil
	}

	return product, nil
}

// connect connects to the Order's source through the Product's Registries,
// and to its destination through the Order's.
func (r *OrderReconciler) connect(ctx context.Context, order *deliveryv1alpha1.Order, effective *resolve.EffectiveSpec) (orderConnections, error) {
	conns := orderConnections{destination: artifact.DefaultDestination(order.Namespace, order.Name)}
	if d := order.Spec.Destination; d != nil && d.URL != "" {
		conns.destination = d.URL
	}

	var err error
	if conns.product, err = r.Registries.Scope(ctx, order.Namespace, effective.RegistryRefs); err != nil {
		return orderConnections{}, err
	}
	if conns.order, err = r.Registries.Scope(ctx, order.Namespace, order.Spec.RegistryRefs); err != nil {
		return orderConnections{}, err
	}
	if conns.source, err = conns.product.Connect(effective.Source.URL); err != nil {
		return orderConnections{}, err
	}
	if conns.destConn, err = conns.order.Connect(conns.destination); err != nil {
		return orderConnections{}, err
	}
	return conns, nil
}

// reconcileUpToDate handles an Order whose intent its latest Preparation
// already renders. registryRefs are not part of the intent: a change
// records how the Order connects now without rendering again.
func (r *OrderReconciler) reconcileUpToDate(
	ctx context.Context, order *deliveryv1alpha1.Order, effective *resolve.EffectiveSpec, conns orderConnections,
) error {
	log.FromContext(ctx).V(4).Info("Configuration is up-to-date, skipping render")

	latest, err := r.latestPreparation(ctx, order)
	if err != nil {
		return err
	}
	r.markUpToDate(ctx, order, effective, conns, latest)
	return nil
}

// render renders the intent of the Order into an artifact and records it in
// a new Preparation, the Order's latest.
func (r *OrderReconciler) render(
	ctx context.Context, order *deliveryv1alpha1.Order, effective *resolve.EffectiveSpec, conns orderConnections, intent []byte, specHash string,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

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

	spec, err := effective.ToArtifactSpec()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to convert effective spec: %w", err)
	}

	imageOpts, err := r.imageOptions(ctx, order, effective, spec.Images)
	if err != nil {
		return renderFailed(order, err)
	}

	parentDigest := order.Status.LatestArtifactDigest
	commitMessage := resolve.CommitMessage(order.Annotations, parentDigest == "")
	renderer := resolve.Renderer(effective.Render, r.BuilderID)

	renderCtx, cancel := context.WithTimeout(ctx, renderTimeout)
	defer cancel()

	result, err := r.Pipeline.Render(renderCtx, artifact.RenderRequest{
		Source:       artifact.Source{OCI: effective.Source.URL, Version: spec.Source.Version, Digest: spec.Source.Digest},
		Destination:  artifact.Destination{OCI: conns.destination},
		Product:      conns.product,
		Order:        conns.order,
		Render:       spec.Render,
		Patches:      spec.Patches,
		Edits:        spec.Edits,
		Images:       imageOpts,
		Name:         order.Name,
		Namespace:    effective.TargetNamespace,
		Description:  commitMessage,
		ParentDigest: parentDigest,
		ArtifactType: oci.PreparationArtifactType,
		Config:       resolve.ArtifactConfig(order, effective, intent, specHash, renderer),
	})
	if err != nil {
		if reason := registry.Reason(err, ""); reason != "" {
			// The Registry watch reconciles the Order again once its
			// Registries change.
			logger.Info("Order cannot render with its registries", "reason", reason, "error", err.Error())
			conditions.MarkFalse(order, deliveryv1alpha1.ConditionTypeReady, reason, err.Error())
			return ctrl.Result{}, nil
		}
		return renderFailed(order, err)
	}

	preparation, err := r.createPreparation(ctx, order, resources.Rendered{
		Source:          result.SourceRef,
		Artifact:        result.DestRef,
		RegistryName:    conns.destConn.RegistryName(),
		Git:             deliveryv1alpha1.GitSource{Repo: result.SCM.Repo, Tag: result.SCM.Tag, CommitHash: result.SCM.CommitHash},
		Images:          result.Images,
		Renderer:        renderer,
		ConfigHash:      specHash,
		ProductRevision: effective.ProductRevision,
		TargetNamespace: effective.TargetNamespace,
		CommitMessage:   commitMessage,
		ParentDigest:    parentDigest,
	})
	if err != nil {
		return renderFailed(order, fmt.Errorf("failed to create revision: %w", err))
	}
	logger.Info("Created Preparation", "revision", preparation.Name)

	r.recordUnresolvedImages(order, preparation, result.Images)
	r.consumeCommitMessage(ctx, order)

	order.Status.LatestConfigHash = specHash
	order.Status.LatestPreparationName = preparation.Name
	order.Status.LatestArtifactDigest = result.DestRef.Digest
	r.markUpToDate(ctx, order, effective, conns, preparation)
	return ctrl.Result{}, nil
}

// markUpToDate marks the Order ready, its intent rendered by its latest
// Preparation latest, nil when it no longer exists, and records the
// registry locations the Order needs.
func (r *OrderReconciler) markUpToDate(
	ctx context.Context, order *deliveryv1alpha1.Order, effective *resolve.EffectiveSpec, conns orderConnections, latest *deliveryv1alpha1.Preparation,
) {
	var latestImages []deliveryv1alpha1.PreparationImage
	if latest != nil {
		latestImages = latest.Spec.Images
	}
	access := registry.OrderAccess(registry.OrderLocations{
		Product:         conns.product,
		Source:          effective.Source.URL,
		SourceConn:      conns.source,
		Destination:     conns.destination,
		DestinationConn: conns.destConn,
		ImagesVendorURL: effective.ImagesVendorURL,
		Images:          latestImages,
	})
	if len(access) > maxRegistryAccess {
		log.FromContext(ctx).Info("Listing only some registry locations of the Order", "listed", maxRegistryAccess, "total", len(access))
		access = access[:maxRegistryAccess]
	}
	order.Status.RegistryAccess = access

	conditions.MarkTrue(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonReady,
		fmt.Sprintf("Preparation %s is up to date", order.Status.LatestPreparationName))
}

// renderFailed records that the intent of the Order could not be rendered
// and returns err to be retried.
func renderFailed(order *deliveryv1alpha1.Order, err error) (ctrl.Result, error) {
	conditions.MarkFalse(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProcessingFailed, err.Error())
	return ctrl.Result{}, err
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
	conditions.MarkFalse(order, deliveryv1alpha1.ConditionTypeReady, reason, err.Error())
	return ctrl.Result{}, nil
}

// exposesPrivateSource reports whether rendering the source into the
// destination would expose it: a private source rendered into a destination
// Kokumi writes without credentials could be read there by anyone.
func exposesPrivateSource(ctx context.Context, effective *resolve.EffectiveSpec, conns orderConnections) (bool, error) {
	if conns.destConn.Authenticated() {
		return false, nil
	}
	pinned, err := oci.Parse(effective.Source.URL + "@" + effective.SourceDigest)
	if err != nil {
		return false, fmt.Errorf("failed to parse source reference: %w", err)
	}
	return conns.product.Private(ctx, conns.source, pinned), nil
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

// createPreparation creates the Preparation that records rendered, controlled
// by the Order. A Preparation of the same artifact is returned as it is.
func (r *OrderReconciler) createPreparation(ctx context.Context, order *deliveryv1alpha1.Order, rendered resources.Rendered) (*deliveryv1alpha1.Preparation, error) {
	preparation := resources.Preparation(order, rendered)

	existing := &deliveryv1alpha1.Preparation{}
	err := r.Get(ctx, client.ObjectKeyFromObject(preparation), existing)
	if err == nil {
		log.FromContext(ctx).Info("Preparation already exists", "revision", existing.Name)
		return existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to check for existing revision: %w", err)
	}

	if err := controllerutil.SetControllerReference(order, preparation, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set owner reference: %w", err)
	}
	if err := r.Create(ctx, preparation); err != nil {
		return nil, fmt.Errorf("failed to create Preparation: %w", err)
	}
	return preparation, nil
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

// recordUnresolvedImages records one Warning Event on order for the images
// of preparation that could not be resolved, which only the Record policy
// lets pass. The note lists as many of them as fit; the artifact's config
// blob records them all. Naming the Preparation as the related object keeps
// the Events of two renders apart: client-go merges Events that differ only
// in their note into one series that keeps the first note.
func (r *OrderReconciler) recordUnresolvedImages(order *deliveryv1alpha1.Order, preparation *deliveryv1alpha1.Preparation, imgs []images.Image) {
	note := images.UnresolvedNote(imgs, maxEventNote)
	if note == "" {
		return
	}
	r.Recorder.Eventf(order, preparation, corev1.EventTypeWarning, deliveryv1alpha1.ReasonImageNotResolved, "Render", "%s", note)
}

// consumeCommitMessage removes the commit-message annotation, which the
// Preparation just created carries. It patches a copy, so that the status
// this reconcile set stays, and carries its resource version back for the
// status write. A failure leaves the annotation to the next render.
func (r *OrderReconciler) consumeCommitMessage(ctx context.Context, order *deliveryv1alpha1.Order) {
	if _, ok := order.Annotations[deliveryv1alpha1.AnnotationCommitMessage]; !ok {
		return
	}

	consumed := order.DeepCopy()
	delete(consumed.Annotations, deliveryv1alpha1.AnnotationCommitMessage)
	if err := r.Patch(ctx, consumed, client.MergeFromWithOptions(order, client.MergeFromWithOptimisticLock{})); err != nil {
		log.FromContext(ctx).Error(err, "Failed to remove commit-message annotation from Order")
		return
	}
	order.Annotations = consumed.Annotations
	order.ResourceVersion = consumed.ResourceVersion
}

// ensureServing makes the Order the controller of its Serving, which carries
// the Order's name, so that the Serving goes with the Order. Under Automatic
// promotion it creates the Serving once the Order has a Preparation, so the
// Serving controller can pick up new Preparations. Under Manual promotion
// the first promote creates it, and the Order adopts it. The Serving spec is
// never changed; the promotion mode is read from the Order directly.
func (r *OrderReconciler) ensureServing(ctx context.Context, order *deliveryv1alpha1.Order) error {
	key := client.ObjectKey{Namespace: order.Namespace, Name: order.Name}
	serving := &deliveryv1alpha1.Serving{}
	err := r.Get(ctx, key, serving)
	if apierrors.IsNotFound(err) {
		if order.EffectivePromotionMode() != deliveryv1alpha1.PromotionModeAutomatic || order.Status.LatestPreparationName == "" {
			return nil
		}
		return r.createServing(ctx, order)
	}
	if err != nil {
		return fmt.Errorf("failed to get Serving: %w", err)
	}
	if metav1.IsControlledBy(serving, order) {
		return nil
	}

	// The Serving controller adds its finalizer to the Serving the first
	// promote creates while the Order adopts it, so adoption reads the
	// Serving from the API server rather than the cache, and again after a
	// conflict.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		serving := &deliveryv1alpha1.Serving{}
		if err := r.APIReader.Get(ctx, key, serving); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get Serving: %w", err)
		}
		return r.adoptServing(ctx, order, serving)
	})
}

// adoptServing makes the Order the controller of serving, unless serving
// belongs to another Order or still to a deleted one.
func (r *OrderReconciler) adoptServing(ctx context.Context, order *deliveryv1alpha1.Order, serving *deliveryv1alpha1.Serving) error {
	logger := log.FromContext(ctx)

	if metav1.IsControlledBy(serving, order) {
		return nil
	}
	if serving.Spec.OrderName != order.Name {
		logger.Info("Serving serves another Order, not adopting it", "order", serving.Spec.OrderName)
		return nil
	}
	if owner := metav1.GetControllerOfNoCopy(serving); owner != nil {
		// The Serving of a deleted Order of the same name, which the
		// garbage collector deletes or orphans; the Serving watch
		// reconciles the Order again once it has.
		logger.Info("Waiting for garbage collection of Serving", "owner", owner.UID)
		return nil
	}

	// The optimistic lock keeps the merge patch, which replaces the owner
	// references as a whole, from dropping one added meanwhile.
	patch := client.MergeFromWithOptions(serving.DeepCopy(), client.MergeFromWithOptimisticLock{})
	if err := controllerutil.SetControllerReference(order, serving, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference: %w", err)
	}
	if err := r.Patch(ctx, serving, patch); err != nil {
		return fmt.Errorf("failed to adopt Serving: %w", err)
	}
	logger.Info("Adopted Serving")
	return nil
}

// createServing creates the Serving of an Automatic Order, controlled by the
// Order.
func (r *OrderReconciler) createServing(ctx context.Context, order *deliveryv1alpha1.Order) error {
	serving := resources.Serving(order)
	if err := controllerutil.SetControllerReference(order, serving, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference: %w", err)
	}
	if err := r.Create(ctx, serving); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// The Serving watch reconciles the Order again to adopt it.
			return nil
		}
		return fmt.Errorf("failed to create Serving: %w", err)
	}
	log.FromContext(ctx).Info("Created Serving")
	return nil
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

// patchStatus writes the status order took on in this reconcile, unless it is
// unchanged, and returns how to requeue: as the reconcile decided, or soon
// when the patch conflicts. The patch is locked to the version this reconcile
// read or last wrote, so that a reconcile from a stale cache never overwrites
// a newer status.
func (r *OrderReconciler) patchStatus(
	ctx context.Context, order *deliveryv1alpha1.Order, before *deliveryv1alpha1.OrderStatus, result ctrl.Result, err error,
) (ctrl.Result, error) {
	order.Status.ObservedGeneration = order.Generation
	if equality.Semantic.DeepEqual(*before, order.Status) {
		return result, err
	}

	base := order.DeepCopy()
	base.Status = *before
	perr := r.Status().Patch(ctx, order, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	switch {
	case perr == nil, apierrors.IsNotFound(perr):
		return result, err
	case apierrors.IsConflict(perr) && err == nil:
		log.FromContext(ctx).V(1).Info("Order changed during the reconcile, reconciling it again")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	default:
		return ctrl.Result{}, errors.Join(err, fmt.Errorf("failed to patch Order status: %w", perr))
	}
}

// orderProduct returns the Product an Order is a variant of, for the index
// on deliveryv1alpha1.FieldProductRefName.
func orderProduct(obj client.Object) []string {
	order, ok := obj.(*deliveryv1alpha1.Order)
	if !ok {
		return nil
	}
	return []string{order.Spec.ProductRef.Name}
}

// orderEnvironment returns the Environment an Order deploys to, for
// FieldEnvironmentName.
func orderEnvironment(obj client.Object) []string {
	order, ok := obj.(*deliveryv1alpha1.Order)
	if !ok {
		return nil
	}
	return []string{order.Spec.EnvironmentRef.Name}
}

// orderRegistryNames returns the Registries an Order lists, for
// orderRegistriesField.
func orderRegistryNames(obj client.Object) []string {
	order, ok := obj.(*deliveryv1alpha1.Order)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(order.Spec.RegistryRefs))
	for _, ref := range order.Spec.RegistryRefs {
		names = append(names, ref.Name)
	}
	return names
}

// orderUpstreamNames returns the upstream Orders an Order runs after, for
// orderUpstreamsField.
func orderUpstreamNames(obj client.Object) []string {
	order, ok := obj.(*deliveryv1alpha1.Order)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(order.UpstreamStages()))
	for _, stage := range order.UpstreamStages() {
		names = append(names, stage.Order)
	}
	return names
}

// ordersOf lists the Orders in namespace whose field index field holds
// value.
func (r *OrderReconciler) ordersOf(ctx context.Context, namespace, field, value string) ([]deliveryv1alpha1.Order, error) {
	orders := &deliveryv1alpha1.OrderList{}
	if err := r.List(ctx, orders, client.InNamespace(namespace), client.MatchingFields{field: value}); err != nil {
		return nil, err
	}
	return orders.Items, nil
}

// enqueueOrdersForProduct enqueues the variants of the Product.
func (r *OrderReconciler) enqueueOrdersForProduct(ctx context.Context, obj client.Object) []reconcile.Request {
	orders, err := r.ordersOf(ctx, obj.GetNamespace(), deliveryv1alpha1.FieldProductRefName, obj.GetName())
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Orders of Product", "product", obj.GetName())
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(orders))
	for i := range orders {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&orders[i])})
	}
	return reqs
}

// enqueueOrdersForRegistry enqueues the Orders that connect through the
// Registry: those that list it, and the Orders of the Products that list
// it, so that they connect again when it changes.
func (r *OrderReconciler) enqueueOrdersForRegistry(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)

	orders, err := r.ordersOf(ctx, obj.GetNamespace(), orderRegistriesField, obj.GetName())
	if err != nil {
		logger.Error(err, "Failed to list Orders of Registry", "registry", obj.GetName())
		return nil
	}
	products := &deliveryv1alpha1.ProductList{}
	if err := r.List(ctx, products,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{productRegistriesField: obj.GetName()},
	); err != nil {
		logger.Error(err, "Failed to list Products of Registry", "registry", obj.GetName())
		return nil
	}
	for _, product := range products.Items {
		ofProduct, err := r.ordersOf(ctx, product.Namespace, deliveryv1alpha1.FieldProductRefName, product.Name)
		if err != nil {
			logger.Error(err, "Failed to list Orders of Product", "product", product.Name)
			return nil
		}
		orders = append(orders, ofProduct...)
	}

	seen := map[client.ObjectKey]bool{}
	reqs := make([]reconcile.Request, 0, len(orders))
	for i := range orders {
		key := client.ObjectKeyFromObject(&orders[i])
		if seen[key] {
			continue
		}
		seen[key] = true
		reqs = append(reqs, reconcile.Request{NamespacedName: key})
	}
	return reqs
}

// enqueueDownstreamOrders enqueues the Orders that run after the Order, so
// that they check their pipeline again.
func (r *OrderReconciler) enqueueDownstreamOrders(ctx context.Context, obj client.Object) []reconcile.Request {
	orders, err := r.ordersOf(ctx, obj.GetNamespace(), orderUpstreamsField, obj.GetName())
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to list downstream Orders", "order", obj.GetName())
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(orders))
	for i := range orders {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&orders[i])})
	}
	return reqs
}

// pipelineChanged passes the Order events after which the Orders that run
// after it may form another pipeline: creates, deletes, spec changes, and
// an Order that becomes or stops being an invalid stage, so that a cycle is
// reported on every Order in it.
var pipelineChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldOrder, okOld := e.ObjectOld.(*deliveryv1alpha1.Order)
		newOrder, okNew := e.ObjectNew.(*deliveryv1alpha1.Order)
		return okOld && okNew && (oldOrder.Generation != newOrder.Generation ||
			conditions.HasReason(oldOrder, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonInvalidPipeline) !=
				conditions.HasReason(newOrder, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonInvalidPipeline))
	},
	GenericFunc: func(event.GenericEvent) bool { return false },
}

// enqueueOrderOfServing maps a Serving to its Order, whose name it carries.
func enqueueOrderOfServing(_ context.Context, obj client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(obj)}}
}

// servingControllerChanged passes the Serving events after which its Order
// may have to create or adopt it: creates, deletes, and updates that change
// the controller reference. The frequent status writes of a Serving do not
// pass.
var servingControllerChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		return controllerUID(e.ObjectOld) != controllerUID(e.ObjectNew)
	},
	GenericFunc: func(event.GenericEvent) bool { return false },
}

// controllerUID returns the UID of the controller of obj, or "" when it has
// none.
func controllerUID(obj client.Object) types.UID {
	if owner := metav1.GetControllerOfNoCopy(obj); owner != nil {
		return owner.UID
	}
	return ""
}

// SetupWithManager sets up the controller with the Manager. It indexes
// Orders by their Product, Registries and upstream Orders, which the
// Product and Serving controllers list them by as well.
func (r *OrderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	indexer := mgr.GetFieldIndexer()
	if err := indexer.IndexField(context.Background(), &deliveryv1alpha1.Order{}, deliveryv1alpha1.FieldProductRefName, orderProduct); err != nil {
		return err
	}
	if err := indexer.IndexField(context.Background(), &deliveryv1alpha1.Order{}, orderRegistriesField, orderRegistryNames); err != nil {
		return err
	}
	if err := indexer.IndexField(context.Background(), &deliveryv1alpha1.Order{}, orderUpstreamsField, orderUpstreamNames); err != nil {
		return err
	}
	if err := indexer.IndexField(context.Background(), &deliveryv1alpha1.Order{}, deliveryv1alpha1.FieldEnvironmentName, orderEnvironment); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Order{}).
		Owns(&deliveryv1alpha1.Preparation{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&deliveryv1alpha1.Order{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueDownstreamOrders),
			builder.WithPredicates(pipelineChanged)).
		Watches(&deliveryv1alpha1.Product{}, handler.EnqueueRequestsFromMapFunc(r.enqueueOrdersForProduct)).
		Watches(&deliveryv1alpha1.Registry{}, handler.EnqueueRequestsFromMapFunc(r.enqueueOrdersForRegistry)).
		Watches(&deliveryv1alpha1.Serving{},
			handler.EnqueueRequestsFromMapFunc(enqueueOrderOfServing),
			builder.WithPredicates(servingControllerChanged)).
		Named("order").
		WithOptions(controller.Options{MaxConcurrentReconciles: orderConcurrency}).
		Complete(r)
}
