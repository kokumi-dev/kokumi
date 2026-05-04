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
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/renderer"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/service"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// OrderReconciler reconciles an Order object.
type OrderReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Service service.OrderService
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=menus,verbs=get;list;watch

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
		_ = statusUpdater.Failed(ctx, order, err)
		return ctrl.Result{}, err
	}

	return r.reconcileRender(ctx, order, effective)
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
	if err := r.Get(ctx, client.ObjectKey{Name: order.Spec.MenuRef.Name}, m); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("referenced Menu %q not found", order.Spec.MenuRef.Name)
		}
		return nil, fmt.Errorf("failed to get Menu %q: %w", order.Spec.MenuRef.Name, err)
	}

	logger.Info("Resolved Menu for Order", "menu", m.Name)

	return resolve.ForMenu(m, order)
}

// reconcileRender delegates FS/OCI work to the service and then handles CRD concerns:
// updating status and creating the Preparation resource.
func (r *OrderReconciler) reconcileRender(ctx context.Context, order *deliveryv1alpha1.Order, effective *resolve.EffectiveSpec) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	statusUpdater := status.NewOrderUpdater(r.Client)

	specHash, err := renderer.CalculateSpecHash(order.Spec)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate spec hash: %w", err)
	}

	if order.Status.LatestConfigHash == specHash {
		logger.Info("Configuration is up-to-date, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	if err := statusUpdater.Processing(ctx, order, specHash); err != nil {
		return ctrl.Result{}, err
	}

	effectiveDest := service.DefaultDestination(order.Namespace, order.Name)
	if order.Spec.Destination != nil && order.Spec.Destination.OCI != "" {
		effectiveDest = order.Spec.Destination.OCI
	}

	parentDigest := order.Status.LatestArtifactDigest

	userMessage, messageProvided := order.Annotations[deliveryv1alpha1.AnnotationCommitMessage]
	commitMessage := service.DefaultCommitMessage(userMessage, messageProvided, parentDigest == "")

	result, err := r.Service.ProcessOrder(ctx, order, effective.Source, effective.Render, effective.Patches, effective.Edits, effectiveDest, commitMessage, parentDigest)
	if err != nil {
		logger.Error(err, "Failed to process Order")
		_ = statusUpdater.Failed(ctx, order, err)

		return ctrl.Result{}, err
	}

	preparation, err := r.createPreparation(ctx, order, result.SourceRef, result.SourceDigest, effective.Source.Version, result.DestRef, result.DestDigest, commitMessage, parentDigest)
	if err != nil {
		logger.Error(err, "Failed to create Preparation")
		_ = statusUpdater.Failed(ctx, order, fmt.Errorf("failed to create revision: %w", err))

		return ctrl.Result{}, err
	}

	logger.Info("Created Preparation", "revision", preparation.Name)

	order.Status.LatestRevision = preparation.Name
	order.Status.LatestArtifactDigest = result.DestDigest

	if err := statusUpdater.Ready(ctx, order, specHash, fmt.Sprintf("Successfully pushed to %s", result.DestRef)); err != nil {
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
	sourceRef, sourceDigest, sourceVersion, destRef, destDigest string,
	commitMessage string,
	parentDigest string,
) (*deliveryv1alpha1.Preparation, error) {
	logger := log.FromContext(ctx)

	shortDigest := destDigest[len("sha256:") : len("sha256:")+12]
	revisionName := fmt.Sprintf("%s-%s", order.Name, shortDigest)

	existing := &deliveryv1alpha1.Preparation{}

	err := r.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: revisionName}, existing)
	if err == nil {
		logger.Info("Preparation already exists", "revision", revisionName)
		return existing, nil
	}

	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to check for existing revision: %w", err)
	}

	configHash, err := renderer.CalculateSpecHash(order.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate config hash: %w", err)
	}

	renderType := deliveryv1alpha1.RenderTypeManifest
	if order.Spec.Render != nil && order.Spec.Render.Helm != nil {
		renderType = deliveryv1alpha1.RenderTypeHelm
	}

	now := metav1.Time{Time: time.Now()}

	preparation := &deliveryv1alpha1.Preparation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      revisionName,
			Namespace: order.Namespace,
			Labels: map[string]string{
				deliveryv1alpha1.LabelOrder:      order.Name,
				deliveryv1alpha1.LabelVersion:    sourceVersion,
				deliveryv1alpha1.LabelAutoDeploy: strconv.FormatBool(order.Spec.AutoDeploy),
			},
		},
		Spec: deliveryv1alpha1.PreparationSpec{
			Order: order.Name,
			Source: deliveryv1alpha1.OrderSource{
				OCI:        fmt.Sprintf("oci://%s", sourceRef),
				BaseDigest: sourceDigest,
			},
			Renderer: deliveryv1alpha1.Renderer{
				Version:    "v1.0.0",
				Digest:     destDigest,
				RenderType: renderType,
			},
			ConfigHash: configHash,
			Artifact: deliveryv1alpha1.Artifact{
				OCIRef: fmt.Sprintf("oci://%s@%s", destRef, destDigest),
				Digest: destDigest,
				Signed: false,
			},
			CommitMessage: commitMessage,
			ParentDigest:  parentDigest,
		},
		Status: deliveryv1alpha1.PreparationStatus{
			Phase:     deliveryv1alpha1.PreparationPhaseReady,
			CreatedAt: &now,
		},
	}

	if err := controllerutil.SetControllerReference(order, preparation, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set owner reference: %w", err)
	}

	if err := r.Create(ctx, preparation); err != nil {
		return nil, fmt.Errorf("failed to create Preparation: %w", err)
	}

	preparation.Status.Phase = deliveryv1alpha1.PreparationPhaseReady
	preparation.Status.CreatedAt = &metav1.Time{Time: time.Now()}

	if err := r.Status().Update(ctx, preparation); err != nil {
		logger.Error(err, "Failed to update Preparation status")
	}

	return preparation, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *OrderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Order{}).
		Owns(&deliveryv1alpha1.Preparation{}).
		Named("order").
		Complete(r)
}
