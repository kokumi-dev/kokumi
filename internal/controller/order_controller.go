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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/index"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/status"
	"github.com/kokumi-dev/kokumi/internal/version"
)

const (
	initialCommitMessage   = "Initial commit"
	automatedCommitMessage = "Automatically generated"
)

// OrderReconciler reconciles an Order object.
type OrderReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Pipeline       *artifact.Pipeline
	PantryResolver credential.PantryResolver
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
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=pantries,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

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

	effectiveDest := artifact.DefaultDestination(order.Namespace, order.Name)
	if order.Spec.Destination != nil && order.Spec.Destination.OCI != "" {
		effectiveDest = order.Spec.Destination.OCI
	}

	// Resolve Pantry references into plain OCI URLs before hashing so a live
	// Pantry URL change is part of artifact identity.
	sourceURL, sourceClient, err := r.PantryResolver.ResolveSource(ctx, effective.Source, order.Namespace)
	if err != nil {
		if uerr := statusUpdater.Failed(ctx, order, fmt.Errorf("failed to resolve source Pantry: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Order status")
		}
		return ctrl.Result{}, err
	}

	resolvedDest, destClient, err := r.PantryResolver.ResolveDestination(ctx, order.Spec.Destination, effectiveDest, order.Namespace)
	if err != nil {
		if uerr := statusUpdater.Failed(ctx, order, fmt.Errorf("failed to resolve destination Pantry: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Order status")
		}
		return ctrl.Result{}, err
	}

	intent, err := resolve.NewIntent(order, effective, sourceURL, resolvedDest).Canonical()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate spec hash: %w", err)
	}

	specHash := resolve.CalculateSpecHash(intent)

	if order.Status.LatestConfigHash == specHash && order.Status.LatestPreparationName != "" {
		logger.Info("Configuration is up-to-date, skipping reconciliation")
		if waitedForProduct(order) {
			if err := statusUpdater.Ready(ctx, order, specHash, order.Status.LatestPreparationName, order.Status.LatestArtifactDigest,
				fmt.Sprintf("Preparation %s is up to date", order.Status.LatestPreparationName)); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, r.ensureServing(ctx, order)
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

	renderer := rendererFor(effective.Render, r.BuilderID)
	config := artifactConfig(order, effective, intent, specHash, renderer)

	result, err := r.Pipeline.Render(ctx, artifact.RenderRequest{
		Source:           artifact.Source{OCI: sourceURL, Version: spec.Source.Version, Digest: spec.Source.Digest},
		SourceClient:     sourceClient,
		Destination:      artifact.Destination{OCI: resolvedDest},
		DestClient:       destClient,
		Render:           spec.Render,
		Patches:          spec.Patches,
		Edits:            spec.Edits,
		Name:             order.Name,
		Namespace:        order.Namespace,
		Description:      commitMessage,
		ParentDigest:     parentDigest,
		ExtraAnnotations: extraAnnotations,
		ArtifactType:     oci.PreparationArtifactType,
		Config:           config,
	})
	if err != nil {
		logger.Error(err, "Failed to process Order")
		if uerr := statusUpdater.Failed(ctx, order, err); uerr != nil {
			logger.Error(uerr, "Failed to update Order status")
		}

		return ctrl.Result{}, err
	}

	preparation, err := r.createPreparation(ctx, order, result.SourceRef, result.DestRef, renderer, commitMessage, parentDigest, specHash, deliveryv1alpha1.GitSource{Repo: result.SCM.Repo, Tag: result.SCM.Tag, CommitHash: result.SCM.CommitHash})
	if err != nil {
		logger.Error(err, "Failed to create Preparation")
		if uerr := statusUpdater.Failed(ctx, order, fmt.Errorf("failed to create revision: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Order status")
		}

		return ctrl.Result{}, err
	}

	logger.Info("Created Preparation", "revision", preparation.Name)

	if err := statusUpdater.Ready(ctx, order, specHash, preparation.Name, result.DestRef.Digest, fmt.Sprintf("Successfully pushed to %s", result.DestRef)); err != nil {
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

// createPreparation creates a Preparation for the rendered artifact.
// If a Preparation with the same name already exists it is returned unchanged.
func (r *OrderReconciler) createPreparation(
	ctx context.Context,
	order *deliveryv1alpha1.Order,
	sourceRef, destRef oci.Reference,
	renderer deliveryv1alpha1.Renderer,
	commitMessage string,
	parentDigest string,
	configHash string,
	gitSource deliveryv1alpha1.GitSource,
) (*deliveryv1alpha1.Preparation, error) {
	logger := log.FromContext(ctx)

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
				OCI:        sourceRef.OCIRepositoryReference(),
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
			GitSource:      gitSource,
			ApprovalPolicy: order.ApprovalPolicy().DeepCopy(),
		},
	}

	if err := controllerutil.SetControllerReference(order, preparation, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set owner reference: %w", err)
	}

	if err := r.Create(ctx, preparation); err != nil {
		return nil, fmt.Errorf("failed to create Preparation: %w", err)
	}

	return preparation, nil
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

	if effective.Source.PantryRef != nil {
		config.Source.PantryRef = effective.Source.PantryRef.Name
	}
	if verified := effective.SourceVerified; verified != nil {
		config.Source.Verified = &artifact.ConfigVerification{
			Provider: string(verified.Provider),
			KeyID:    verified.KeyID,
			Issuer:   verified.Issuer,
			Subject:  verified.Subject,
		}
	}
	if order.Spec.Destination != nil && order.Spec.Destination.PantryRef != nil {
		config.Destination.PantryRef = order.Spec.Destination.PantryRef.Name
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

func (r *OrderReconciler) enqueueOrdersForPantry() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		pantry, ok := obj.(*deliveryv1alpha1.Pantry)
		if !ok {
			return nil
		}

		orders, err := index.OrdersForPantry(ctx, r.Client, pantry.Namespace, pantry.Name)
		if err != nil {
			return nil
		}

		reqs := make([]reconcile.Request, 0, len(orders))
		for _, order := range orders {
			reqs = append(reqs, reconcile.Request{Namespace: order.Namespace, Name: order.Name})
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
		Watches(&deliveryv1alpha1.Pantry{}, r.enqueueOrdersForPantry()).
		Named("order").
		Complete(r)
}
