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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/index"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/provenance"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// signingKeyRetryInterval is how often a Preparation retries while the
// Kitchen signing key is not ready.
const signingKeyRetryInterval = 10 * time.Second

// PreparationReconciler reconciles a Preparation object. It signs the
// Preparation's artifact and attaches its provenance, aggregates the
// Preparation's Approvals, and seals them into a signed OCI attestation once
// a Serving targets the approved Preparation.
type PreparationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader bypasses the cache so sealing never misses in-flight votes.
	APIReader client.Reader
	// OCIClient is used when the Order's destination has no credentials.
	OCIClient      oci.Client
	PantryResolver credential.PantryResolver
	// Signing signs the artifact and its attestations with the Kitchen key.
	Signing *signing.Provider
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=approvals,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *PreparationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Preparation", "namespace", req.Namespace, "name", req.Name)

	preparation := &deliveryv1alpha1.Preparation{}
	if err := r.Get(ctx, req.NamespacedName, preparation); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Preparation resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Preparation")
		return ctrl.Result{}, fmt.Errorf("failed to get Preparation: %w", err)
	}

	if !preparation.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, preparation)
	}

	if !controllerutil.ContainsFinalizer(preparation, deliveryv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(preparation, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, preparation); err != nil {
			return ctrl.Result{}, err
		}

		// The update triggers the next reconcile. Continuing here would race
		// it: both would attach provenance and one status write would
		// conflict.
		return ctrl.Result{}, nil
	}

	return r.reconcilePreparation(ctx, preparation)
}

// reconcilePreparation signs the Preparation's artifact and attaches its
// provenance, which makes it ready, then aggregates its Approvals and seals
// them into a signed OCI attestation once a Serving targets the approved
// Preparation.
func (r *PreparationReconciler) reconcilePreparation(ctx context.Context, preparation *deliveryv1alpha1.Preparation) (ctrl.Result, error) {
	if !apimeta.IsStatusConditionTrue(preparation.Status.Conditions, deliveryv1alpha1.ConditionTypeSigned) ||
		!apimeta.IsStatusConditionTrue(preparation.Status.Conditions, deliveryv1alpha1.ConditionTypeProvenanceAttached) {
		// The status update triggers the next reconcile.
		return r.attachEvidence(ctx, preparation)
	}

	approvals, err := index.ApprovalsForPreparation(ctx, r.Client, preparation)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list Approvals: %w", err)
	}

	res := approval.Evaluate(preparation, approvals)
	if err := status.NewPreparationUpdater(r.Client).Approval(ctx, preparation, res); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update Preparation approval status: %w", err)
	}
	if !res.Required {
		return ctrl.Result{}, nil
	}

	if approval.IsSealed(preparation) {
		if preparation.Status.Approval.Attestation == nil {
			return r.archive(ctx, preparation)
		}
		return ctrl.Result{}, nil
	}

	if !res.Approved {
		return ctrl.Result{}, nil
	}
	targeted, err := r.isPromotionTarget(ctx, preparation)
	if err != nil || !targeted {
		return ctrl.Result{}, err
	}
	return r.seal(ctx, preparation)
}

// isPromotionTarget reports whether the Order's Serving converges to prep.
func (r *PreparationReconciler) isPromotionTarget(ctx context.Context, prep *deliveryv1alpha1.Preparation) (bool, error) {
	serving := &deliveryv1alpha1.Serving{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: prep.Namespace, Name: prep.Spec.OrderName}, serving); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return serving.Status.TargetPreparationName == prep.Name, nil
}

// seal locks the votes of prep using a fresh read from the API server, then
// archives them. Votes submitted later are ignored.
func (r *PreparationReconciler) seal(ctx context.Context, prep *deliveryv1alpha1.Preparation) (ctrl.Result, error) {
	updater := status.NewPreparationUpdater(r.Client)

	approvals, err := index.ApprovalsForPreparation(ctx, r.APIReader, prep)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list Approvals: %w", err)
	}
	res := approval.Evaluate(prep, approvals)
	if !res.Approved {
		return ctrl.Result{}, updater.Approval(ctx, prep, res)
	}

	if err := updater.Seal(ctx, prep, res, approval.NewSealTime(time.Now())); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to seal Approvals: %w", err)
	}
	log.FromContext(ctx).Info("Sealed Approvals", "preparation", prep.Name, "approvedCount", res.Status.ApprovedCount)

	return r.archive(ctx, prep)
}

// archive records the sealed votes as a signed OCI attestation of the
// Preparation.
func (r *PreparationReconciler) archive(ctx context.Context, prep *deliveryv1alpha1.Preparation) (ctrl.Result, error) {
	updater := status.NewPreparationUpdater(r.Client)

	state, err := r.Signing.State(ctx)
	if err != nil {
		log.FromContext(ctx).Info("Waiting for the signing key to archive Approvals", "preparation", prep.Name, "reason", err.Error())
		if uerr := updater.ArchiveFailed(ctx, prep, err); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: signingKeyRetryInterval}, nil
	}

	approvals, err := index.ApprovalsForPreparation(ctx, r.APIReader, prep)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list Approvals: %w", err)
	}

	destClient, err := r.destinationClient(ctx, prep)
	if err != nil {
		return ctrl.Result{}, err
	}

	archived, err := approval.Archive(ctx, destClient, state.Signer, prep, approvals)
	if err != nil {
		if uerr := updater.ArchiveFailed(ctx, prep, err); uerr != nil {
			log.FromContext(ctx).Error(uerr, "Failed to update Preparation status")
		}
		return ctrl.Result{}, fmt.Errorf("failed to archive Approvals: %w", err)
	}
	if err := updater.Archived(ctx, prep, archived); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to record approval attestation: %w", err)
	}
	log.FromContext(ctx).Info("Recorded approval attestation", "preparation", prep.Name, "attestation", archived.OCIRef)
	return ctrl.Result{}, nil
}

// destinationClient returns the client for the registry holding prep's
// artifact.
func (r *PreparationReconciler) destinationClient(ctx context.Context, prep *deliveryv1alpha1.Preparation) (oci.Client, error) {
	return destinationClient(ctx, r.Client, r.PantryResolver, r.OCIClient, prep)
}

// destinationClient returns the client for the registry holding prep's
// artifact. It uses the credentials of the Order's destination; a deleted
// Order falls back to the anonymous client.
func destinationClient(ctx context.Context, reader client.Reader, resolver credential.PantryResolver, anonymous oci.Client, prep *deliveryv1alpha1.Preparation) (oci.Client, error) {
	order := &deliveryv1alpha1.Order{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: prep.Namespace, Name: prep.Spec.OrderName}, order); err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get Order %q: %w", prep.Spec.OrderName, err)
	}

	_, destClient, err := resolver.ResolveDestination(ctx, order.Spec.Destination, "", prep.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve destination credentials: %w", err)
	}

	return cmp.Or(destClient, anonymous), nil
}

// attachEvidence signs prep's artifact and attaches its provenance as signed
// OCI attestations, then marks prep ready. Both statements are rebuilt from
// the artifact's config blob and signed deterministically, so retries find
// the referrers they pushed before.
func (r *PreparationReconciler) attachEvidence(ctx context.Context, prep *deliveryv1alpha1.Preparation) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	updater := status.NewPreparationUpdater(r.Client)

	state, err := r.Signing.State(ctx)
	if err != nil {
		logger.Info("Waiting for the signing key", "preparation", prep.Name, "reason", err.Error())
		if uerr := updater.SignFailed(ctx, prep, deliveryv1alpha1.ReasonSigningKeyNotReady, err); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: signingKeyRetryInterval}, nil
	}

	subject, err := oci.Parse(prep.Spec.Artifact.OCIRef)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("parsing Preparation artifact reference: %w", err)
	}

	destClient, err := r.destinationClient(ctx, prep)
	if err != nil {
		return ctrl.Result{}, err
	}

	cfg, err := provenance.ReadConfig(ctx, destClient, subject)
	if err != nil {
		// Terminal for an invalid config: the artifact is immutable, so a
		// retry would read the same config. Only a failed status write is
		// retried.
		uerr := updater.SignFailed(ctx, prep, deliveryv1alpha1.ReasonSignFailed, err)
		if errors.Is(err, provenance.ErrInvalidConfig) {
			logger.Error(err, "Could not sign artifact", "preparation", prep.Name)
			return ctrl.Result{}, uerr
		}
		if uerr != nil {
			logger.Error(uerr, "Failed to update Preparation status")
		}
		return ctrl.Result{}, fmt.Errorf("failed to read artifact config: %w", err)
	}
	created, _ := cfg.Created() // validated by ReadConfig

	signatureDigest, err := attestation.Sign(ctx, destClient, state.Signer, subject, created)
	if err != nil {
		if uerr := updater.SignFailed(ctx, prep, deliveryv1alpha1.ReasonSignFailed, err); uerr != nil {
			logger.Error(uerr, "Failed to update Preparation status")
		}
		return ctrl.Result{}, fmt.Errorf("failed to sign artifact: %w", err)
	}

	provenanceDigest, err := provenance.Attach(ctx, destClient, state.Signer, subject, cfg)
	if err != nil {
		uerr := updater.ProvenanceFailed(ctx, prep, err)
		if errors.Is(err, provenance.ErrInvalidConfig) {
			logger.Error(err, "Could not attach provenance", "preparation", prep.Name)
			return ctrl.Result{}, uerr
		}
		if uerr != nil {
			logger.Error(uerr, "Failed to update Preparation status")
		}
		return ctrl.Result{}, fmt.Errorf("failed to attach provenance: %w", err)
	}

	signature := attestationReference(deliveryv1alpha1.AttestationTypeSignature, subject, signatureDigest)
	provenanceRef := attestationReference(deliveryv1alpha1.AttestationTypeProvenance, subject, provenanceDigest)
	if err := updater.EvidenceAttached(ctx, prep, signature, provenanceRef); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to record attestations: %w", err)
	}
	logger.Info("Signed artifact", "preparation", prep.Name, "keyID", state.Signer.KeyID(),
		"signature", signature.OCIRef, "provenance", provenanceRef.OCIRef)

	return ctrl.Result{}, nil
}

// attestationReference references the referrer with digest of subject.
func attestationReference(attestationType deliveryv1alpha1.AttestationType, subject oci.Reference, digest string) deliveryv1alpha1.AttestationReference {
	return deliveryv1alpha1.AttestationReference{
		Type:   attestationType,
		OCIRef: fmt.Sprintf("oci://%s@%s", subject.RepositoryReference(), digest),
		Digest: digest,
	}
}

// reconcileDelete removes the finalizer from the Preparation, allowing garbage collection.
func (r *PreparationReconciler) reconcileDelete(ctx context.Context, preparation *deliveryv1alpha1.Preparation) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Preparation")

	if controllerutil.ContainsFinalizer(preparation, deliveryv1alpha1.Finalizer) {
		logger.Info("Cleaning up Preparation resources")

		controllerutil.RemoveFinalizer(preparation, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, preparation); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func enqueuePreparationForApproval(_ context.Context, obj client.Object) []ctrl.Request {
	a, ok := obj.(*deliveryv1alpha1.Approval)
	if !ok {
		return nil
	}
	return []ctrl.Request{{Namespace: a.Namespace, Name: a.Spec.PreparationRef.Name}}
}

func enqueueTargetPreparation(_ context.Context, obj client.Object) []ctrl.Request {
	serving, ok := obj.(*deliveryv1alpha1.Serving)
	if !ok || serving.Status.TargetPreparationName == "" {
		return nil
	}
	return []ctrl.Request{{Namespace: serving.Namespace, Name: serving.Status.TargetPreparationName}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *PreparationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Preparation{}).
		Watches(&deliveryv1alpha1.Approval{}, handler.EnqueueRequestsFromMapFunc(enqueuePreparationForApproval)).
		Watches(&deliveryv1alpha1.Serving{},
			handler.EnqueueRequestsFromMapFunc(enqueueTargetPreparation),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldServing, okOld := e.ObjectOld.(*deliveryv1alpha1.Serving)
					newServing, okNew := e.ObjectNew.(*deliveryv1alpha1.Serving)
					return okOld && okNew && oldServing.Status.TargetPreparationName != newServing.Status.TargetPreparationName
				},
			})).
		Named("preparation").
		Complete(r)
}
