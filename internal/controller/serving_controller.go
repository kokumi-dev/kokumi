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
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/lru"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/deployer"
	"github.com/kokumi-dev/kokumi/internal/evidence"
	"github.com/kokumi-dev/kokumi/internal/index"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// verificationCacheSize bounds how many verified artifacts the Serving
// controller remembers.
const verificationCacheSize = 1024

// verificationKey identifies a verification result. Digests are immutable,
// so a result holds until the trust changes; it is specific to the Order the
// provenance must name and to whether approvals are required.
type verificationKey struct {
	digest           string
	trust            string
	order            string
	approvalRequired bool
}

// ServingReconciler reconciles a Serving object
type ServingReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Deployer deployer.Deployer
	// Registries connects to the destination holding the artifacts.
	Registries registryConnector
	// Signing holds the keys the evidence of a Preparation must verify
	// against before it is deployed.
	Signing *signing.Provider

	// verified remembers verified artifacts by verificationKey.
	verified     *lru.Cache
	verifiedOnce sync.Once
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=approvals,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *ServingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

// reconcileServing handles the serving by driving the deployment through the
// configured Deployer.
func (r *ServingReconciler) reconcileServing(ctx context.Context, serving *deliveryv1alpha1.Serving) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	statusUpdater := status.NewServingUpdater(r.Client)

	preparationName, result, err := r.resolveTarget(ctx, serving, statusUpdater)
	if result != nil || err != nil {
		return *result, err
	}

	preparation := &deliveryv1alpha1.Preparation{}
	preparationKey := client.ObjectKey{Namespace: serving.Namespace, Name: preparationName}
	if err := r.Get(ctx, preparationKey, preparation); err != nil {
		logger.Error(err, "Failed to get Preparation", "preparation", preparationName)
		if uerr := statusUpdater.Failed(ctx, serving, fmt.Errorf("preparation not found: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return ctrl.Result{}, err
	}

	// A Preparation is served only once it is Ready, under both promotion
	// modes. The currently deployed Preparation is left untouched, and the
	// Preparation watch triggers the next reconcile.
	if !apimeta.IsStatusConditionTrue(preparation.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) {
		logger.Info("Preparation not ready, waiting", "preparation", preparationName)
		if uerr := statusUpdater.Pending(ctx, serving, fmt.Sprintf("Waiting for Preparation %q to become ready", preparationName)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return ctrl.Result{}, nil
	}

	blocked, err := r.gate(ctx, serving, preparation, statusUpdater)
	if blocked || err != nil {
		return ctrl.Result{}, err
	}

	// The registry is the record: the signature and attestations are
	// verified there, not trusted from the Preparation's status.
	blocked, err = r.verify(ctx, serving, preparation, statusUpdater)
	if blocked || err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Found Preparation", "preparation", preparation.Name, "digest", preparation.Spec.Artifact.Digest)

	desiredDigest := preparation.Spec.Artifact.Digest

	// Deploy is idempotent: it leaves a deployment that already points at the
	// Preparation untouched and reverts drift otherwise.
	if err := r.Deployer.Deploy(ctx, serving, preparation); err != nil {
		if errors.Is(err, deployer.ErrOptInRequired) {
			logger.Info("Could not update deployment, opt-in required", "error", err.Error())
			if uerr := statusUpdater.Failed(ctx, serving, err); uerr != nil {
				logger.Error(uerr, "Failed to update Serving status")
			}
			// Terminal for this generation: do not requeue. The deployer watch
			// reports a change to the opt-in, and a spec change reports itself.
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to deploy")
		if uerr := statusUpdater.Failed(ctx, serving, fmt.Errorf("failed to deploy: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return ctrl.Result{}, err
	}

	deploymentStatus, err := r.Deployer.Status(ctx, serving)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get deployment status: %w", err)
	}

	// The status is derived from the observed deployment and written once per
	// reconcile. An unchanged status is not written, so a Serving that stays
	// degraded or progressing settles until the deployment changes. The health
	// of another revision says nothing about this Preparation, so until the
	// deployment reaches the desired revision the Serving is Deploying.
	atDesired := deploymentStatus.Revision == desiredDigest
	switch {
	case atDesired && deploymentStatus.Phase == deployer.PhaseDegraded:
		return ctrl.Result{}, statusUpdater.Degraded(ctx, serving, preparationName, deploymentStatus.Message)
	case atDesired && deploymentStatus.Phase == deployer.PhaseHealthy:
		return ctrl.Result{}, statusUpdater.Deployed(ctx, serving, preparationName, desiredDigest, "Successfully deployed component")
	default:
		logger.Info("Deployment not yet healthy at desired revision",
			"preparation", preparationName, "phase", deploymentStatus.Phase, "revision", deploymentStatus.Revision)
		return ctrl.Result{}, statusUpdater.Deploying(ctx, serving, preparationName)
	}
}

// gate enforces the approval gate of the target Preparation for both
// promotion modes. It returns true when deployment must not proceed; the
// currently deployed Preparation is left untouched.
func (r *ServingReconciler) gate(ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation, statusUpdater *status.ServingUpdater) (bool, error) {
	approvals, err := index.ApprovalsForPreparation(ctx, r.Client, preparation)
	if err != nil {
		return true, fmt.Errorf("failed to list Approvals: %w", err)
	}

	gate := approval.Gate(preparation, approvals)
	if gate.Blocked {
		log.FromContext(ctx).V(4).Info("Approval gate blocks deployment", "preparation", preparation.Name, "reason", gate.Reason)
	}
	if err := statusUpdater.Gate(ctx, serving, preparation.Name, gate.Status, gate.Reason, gate.Message); err != nil {
		return true, fmt.Errorf("failed to update Serving approval status: %w", err)
	}
	return gate.Blocked, nil
}

// verify checks the evidence of preparation's artifact in the registry
// before it is deployed: its signature, its provenance for the Serving's
// Order, and, when an approval policy applies, its sealed approvals. It
// returns true when deployment must not proceed; the currently deployed
// Preparation is left untouched.
func (r *ServingReconciler) verify(ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation, statusUpdater *status.ServingUpdater) (bool, error) {
	logger := log.FromContext(ctx)

	state, err := r.Signing.State(ctx)
	if err != nil {
		// The provider notifies the watch once the key is loaded.
		logger.Info("Waiting for the signing key to verify Preparation", "preparation", preparation.Name, "reason", err.Error())
		return true, statusUpdater.Verified(ctx, serving, metav1.ConditionUnknown, deliveryv1alpha1.ReasonSigningKeyNotReady,
			preparation.Name+": waiting for the signing key: "+err.Error())
	}

	key := verificationKey{
		digest:           preparation.Spec.Artifact.Digest,
		trust:            state.Trust.ID(),
		order:            serving.Namespace + "/" + serving.Spec.OrderName,
		approvalRequired: preparation.Spec.ApprovalPolicy != nil,
	}

	r.verifiedOnce.Do(func() { r.verified = lru.New(verificationCacheSize) })
	if _, ok := r.verified.Get(key); !ok {
		destClient, err := destinationClient(ctx, r.Client, r.Registries, preparation)
		if err != nil {
			if reason := registry.Reason(err, ""); reason != "" {
				// The Registry watch of the Order reconciles again once
				// the Registries change.
				return true, statusUpdater.Verified(ctx, serving, metav1.ConditionFalse, reason, preparation.Name+": "+err.Error())
			}
			return true, err
		}

		if err := evidence.Verify(ctx, destClient, state.Trust, preparation, key.order); err != nil {
			var rejected *evidence.Rejected
			if !errors.As(err, &rejected) {
				return true, fmt.Errorf("failed to verify Preparation %q: %w", preparation.Name, err)
			}
			logger.Info("Evidence of Preparation did not verify, keeping the current deployment",
				"preparation", preparation.Name, "reason", rejected.Reason, "error", err.Error())
			return true, statusUpdater.Verified(ctx, serving, metav1.ConditionFalse, rejected.Reason, preparation.Name+": "+err.Error())
		}

		r.verified.Add(key, struct{}{})
	}

	return false, statusUpdater.Verified(ctx, serving, metav1.ConditionTrue, deliveryv1alpha1.ReasonSignaturesVerified,
		preparation.Name+": signature and attestations verified")
}

// resolveTarget determines the Preparation the Serving converges to. The
// promotion mode is read from the Order: Manual uses spec.preparationName,
// Automatic uses the newest Ready Preparation of the Order. The Serving spec
// is never modified.
func (r *ServingReconciler) resolveTarget(ctx context.Context, serving *deliveryv1alpha1.Serving, statusUpdater *status.ServingUpdater) (string, *ctrl.Result, error) {
	logger := log.FromContext(ctx)

	order := &deliveryv1alpha1.Order{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: serving.Namespace, Name: serving.Spec.OrderName}, order); err != nil {
		if !apierrors.IsNotFound(err) {
			return "", &ctrl.Result{}, fmt.Errorf("failed to get Order: %w", err)
		}
		if uerr := statusUpdater.Pending(ctx, serving, fmt.Sprintf("Waiting for Order %q", serving.Spec.OrderName)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return "", &ctrl.Result{}, nil
	}

	if order.EffectivePromotionMode() != deliveryv1alpha1.PromotionModeAutomatic {
		if serving.Spec.PreparationName == "" {
			if uerr := statusUpdater.Pending(ctx, serving, "Waiting for a Preparation to be promoted"); uerr != nil {
				logger.Error(uerr, "Failed to update Serving status")
			}
			return "", &ctrl.Result{}, nil
		}
		return serving.Spec.PreparationName, nil, nil
	}

	preparations, err := index.PreparationsForOrder(ctx, r.Client, serving.Namespace, serving.Spec.OrderName)
	if err != nil {
		logger.Error(err, "Failed to list Preparations")
		if uerr := statusUpdater.Failed(ctx, serving, fmt.Errorf("failed to list preparations: %w", err)); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		return "", &ctrl.Result{}, err
	}

	if len(preparations) == 0 {
		logger.Info("No preparations found for order", "order", serving.Spec.OrderName)
		if uerr := statusUpdater.Pending(ctx, serving, "Waiting for preparations"); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		result := ctrl.Result{RequeueAfter: 30 * time.Second}
		return "", &result, nil
	}

	var latestPreparation *deliveryv1alpha1.Preparation
	for i := range preparations {
		prep := &preparations[i]
		if !apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) {
			continue
		}
		if latestPreparation == nil || prep.CreationTimestamp.After(latestPreparation.CreationTimestamp.Time) {
			latestPreparation = prep
		}
	}

	if latestPreparation == nil {
		logger.Info("No ready preparations found for order", "order", serving.Spec.OrderName)
		if uerr := statusUpdater.Pending(ctx, serving, "Waiting for ready preparation"); uerr != nil {
			logger.Error(uerr, "Failed to update Serving status")
		}
		result := ctrl.Result{RequeueAfter: 30 * time.Second}
		return "", &result, nil
	}

	preparationName := latestPreparation.Name
	logger.Info("Selected latest preparation", "preparation", preparationName)

	return preparationName, nil, nil
}

// reconcileDelete handles the deletion of a Serving
func (r *ServingReconciler) reconcileDelete(ctx context.Context, serving *deliveryv1alpha1.Serving) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Serving")

	if controllerutil.ContainsFinalizer(serving, deliveryv1alpha1.Finalizer) {
		logger.Info("Cleaning up deployment")
		if err := r.Deployer.Remove(ctx, serving); err != nil {
			return ctrl.Result{}, err
		}

		controllerutil.RemoveFinalizer(serving, deliveryv1alpha1.Finalizer)
		if err := r.Update(ctx, serving); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
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

// SetupWithManager sets up the controller with the Manager.
func (r *ServingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Status writes of the controller itself need no reconcile.
		For(&deliveryv1alpha1.Serving{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&deliveryv1alpha1.Preparation{}, handler.EnqueueRequestsFromMapFunc(enqueueServingForOrderName)).
		Watches(&deliveryv1alpha1.Order{},
			handler.EnqueueRequestsFromMapFunc(enqueueServingForOrderName),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(r.Deployer.WatchObject(),
			handler.EnqueueRequestsFromMapFunc(r.Deployer.EnqueueRequests),
			builder.WithPredicates(r.Deployer.WatchPredicate())).
		WatchesRawSource(source.Channel(signingChanges(r.Signing), handler.EnqueueRequestsFromMapFunc(r.enqueueAllServings))).
		Named("serving").
		Complete(r)
}
