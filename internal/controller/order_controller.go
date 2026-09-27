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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=menus,verbs=get;list;watch
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

	effective, err := r.resolveEffectiveSpec(ctx, order)
	if err != nil {
		statusUpdater := status.NewOrderUpdater(r.Client)
		if uerr := statusUpdater.Failed(ctx, order, err); uerr != nil {
			logger.Error(uerr, "Failed to update Order status")
		}
		return ctrl.Result{}, err
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

// resolveEffectiveSpec computes the effective source, render, and patches.
// For plain Orders (no menuRef), the Order's own fields are used directly.
// For Menu-based Orders, the Menu's base config is merged with validated consumer overrides.
func (r *OrderReconciler) resolveEffectiveSpec(ctx context.Context, order *deliveryv1alpha1.Order) (*resolve.EffectiveSpec, error) {
	logger := log.FromContext(ctx)

	if order.Spec.MenuRef == nil {
		return resolve.FromOrder(order)
	}

	m := &deliveryv1alpha1.Menu{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: order.Spec.MenuRef.Name}, m); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("referenced Menu %q not found in namespace %q", order.Spec.MenuRef.Name, order.Namespace)
		}
		return nil, fmt.Errorf("failed to get Menu %q: %w", order.Spec.MenuRef.Name, err)
	}

	logger.Info("Resolved Menu for Order", "menu", m.Name)

	return resolve.ForMenu(m, order)
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

	specHash, err := resolve.CalculateSpecHash(order.Spec, sourceURL, resolvedDest)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate spec hash: %w", err)
	}

	if order.Status.LatestConfigHash == specHash && order.Status.LatestPreparationName != "" {
		logger.Info("Configuration is up-to-date, skipping reconciliation")
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

	result, err := r.Pipeline.Render(ctx, artifact.RenderRequest{
		Source:           artifact.Source{OCI: sourceURL, Version: spec.Source.Version},
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
	})
	if err != nil {
		logger.Error(err, "Failed to process Order")
		if uerr := statusUpdater.Failed(ctx, order, err); uerr != nil {
			logger.Error(uerr, "Failed to update Order status")
		}

		return ctrl.Result{}, err
	}

	preparation, err := r.createPreparation(ctx, order, result.SourceRef, result.DestRef, commitMessage, parentDigest, specHash, deliveryv1alpha1.GitSource{Repo: result.SCM.Repo, Tag: result.SCM.Tag, CommitHash: result.SCM.CommitHash})
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

	renderType := deliveryv1alpha1.RenderTypeManifest
	if order.Spec.Render != nil && order.Spec.Render.Helm != nil {
		renderType = deliveryv1alpha1.RenderTypeHelm
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
			Renderer: deliveryv1alpha1.Renderer{
				Version:    "v1.0.0",
				Digest:     destRef.Digest,
				RenderType: renderType,
			},
			ConfigHash: configHash,
			Artifact: deliveryv1alpha1.Artifact{
				OCIRef: destRef.OCIString(),
				Digest: destRef.Digest,
				Signed: false,
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
		Watches(&deliveryv1alpha1.Pantry{}, r.enqueueOrdersForPantry()).
		Named("order").
		Complete(r)
}
