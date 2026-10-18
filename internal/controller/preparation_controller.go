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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/provenance"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

// signingKeyRetryInterval is how often a Preparation retries while the
// Kitchen signing key is not ready.
const signingKeyRetryInterval = 10 * time.Second

// preparationRegistries connects to the artifact of a Preparation.
// *registry.Resolver implements it.
type preparationRegistries interface {
	ConnectRecorded(ctx context.Context, namespace string, recorded *corev1.LocalObjectReference, url string,
		fallback func(context.Context) ([]corev1.LocalObjectReference, error)) (registry.Connection, error)
}

// preparationSigning provides the key artifacts and approvals are signed
// with. *signing.Provider implements it.
type preparationSigning interface {
	State(ctx context.Context) (signing.State, error)
}

// PreparationReconciler reconciles a Preparation object. It signs the
// Preparation's artifact and attaches its provenance, aggregates the
// Preparation's Approvals, and seals them into a signed OCI attestation once
// a Serving targets the approved Preparation.
type PreparationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader bypasses the cache so sealing never misses in-flight votes.
	APIReader client.Reader
	// Registries connects to the destination holding the artifact.
	Registries preparationRegistries
	// Signing signs the artifact and its attestations with the Kitchen key.
	Signing preparationSigning
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=preparations/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=approvals,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=servings,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile attaches the evidence of the Preparation's artifact and gates
// it on its Approvals.
func (r *PreparationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
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

	before := preparation.Status.DeepCopy()
	defer func() { result, err = r.patchStatus(ctx, preparation, before, result, err) }()

	if !preparation.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, preparation)
	}

	if !controllerutil.ContainsFinalizer(preparation, deliveryv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(preparation, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, preparation); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcilePreparation(ctx, preparation)
}

// reconcilePreparation signs the Preparation's artifact and attaches its
// provenance, which makes it ready, then aggregates its Approvals and seals
// them into a signed OCI attestation once a Serving targets the approved
// Preparation. Each step that writes to the registry ends the reconcile;
// its status write triggers the next one.
func (r *PreparationReconciler) reconcilePreparation(ctx context.Context, preparation *deliveryv1alpha1.Preparation) (ctrl.Result, error) {
	if !evidenceAttached(preparation) {
		return r.attachEvidence(ctx, preparation)
	}

	approvals, err := r.approvalsOf(ctx, r.Client, preparation)
	if err != nil {
		return ctrl.Result{}, err
	}
	res := approval.Evaluate(preparation, approvals)
	markApproval(preparation, res)

	switch {
	case !res.Required:
		return ctrl.Result{}, nil
	case approval.IsSealed(preparation) && preparation.Status.Approval.Attestation == nil:
		return r.archive(ctx, preparation)
	case approval.IsSealed(preparation), !res.Approved:
		return ctrl.Result{}, nil
	}

	targeted, err := r.isPromotionTarget(ctx, preparation)
	if err != nil || !targeted {
		return ctrl.Result{}, err
	}
	return r.seal(ctx, preparation)
}

// attachEvidence signs prep's artifact and attaches its provenance as signed
// OCI attestations, then marks prep ready. Both statements are rebuilt from
// the artifact's config blob and signed deterministically, so retries find
// the referrers they pushed before.
func (r *PreparationReconciler) attachEvidence(ctx context.Context, prep *deliveryv1alpha1.Preparation) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	state, err := r.Signing.State(ctx)
	if err != nil {
		logger.Info("Waiting for the signing key", "preparation", prep.Name, "reason", err.Error())
		markSignFailed(prep, deliveryv1alpha1.ReasonSigningKeyNotReady, err)
		return ctrl.Result{RequeueAfter: signingKeyRetryInterval}, nil
	}

	subject, err := oci.Parse(prep.Spec.Artifact.OCIRef)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("parsing Preparation artifact reference: %w", err)
	}

	conn, err := r.artifactConnection(ctx, prep)
	if err != nil {
		return ctrl.Result{}, err
	}

	cfg, err := provenance.ReadConfig(ctx, conn.Client, subject)
	if err != nil {
		markSignFailed(prep, deliveryv1alpha1.ReasonSignFailed, err)
		if errors.Is(err, provenance.ErrInvalidConfig) {
			// The artifact is immutable, so a retry would read the same config.
			return ctrl.Result{}, reconcile.TerminalError(fmt.Errorf("could not sign artifact: %w", err))
		}
		return ctrl.Result{}, fmt.Errorf("failed to read artifact config: %w", err)
	}
	created, _ := cfg.Created() // validated by ReadConfig

	signatureDigest, err := attestation.Sign(ctx, conn.Client, state.Signer, subject, created)
	if err != nil {
		markSignFailed(prep, deliveryv1alpha1.ReasonSignFailed, err)
		return ctrl.Result{}, fmt.Errorf("failed to sign artifact: %w", err)
	}

	provenanceDigest, err := provenance.Attach(ctx, conn.Client, state.Signer, subject, cfg)
	if err != nil {
		markProvenanceFailed(prep, err)
		if errors.Is(err, provenance.ErrInvalidConfig) {
			return ctrl.Result{}, reconcile.TerminalError(fmt.Errorf("could not attach provenance: %w", err))
		}
		return ctrl.Result{}, fmt.Errorf("failed to attach provenance: %w", err)
	}

	signature := attestationReference(deliveryv1alpha1.AttestationTypeSignature, subject, signatureDigest)
	provenanceRef := attestationReference(deliveryv1alpha1.AttestationTypeProvenance, subject, provenanceDigest)
	markEvidenceAttached(prep, signature, provenanceRef)
	logger.Info("Signed artifact", "preparation", prep.Name, "keyID", state.Signer.KeyID(),
		"signature", signature.OCIRef, "provenance", provenanceRef.OCIRef)

	return ctrl.Result{}, nil
}

// isPromotionTarget reports whether the Order's Serving converges to prep.
func (r *PreparationReconciler) isPromotionTarget(ctx context.Context, prep *deliveryv1alpha1.Preparation) (bool, error) {
	serving := &deliveryv1alpha1.Serving{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: prep.Namespace, Name: prep.Spec.OrderName}, serving); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return serving.Status.TargetPreparationName == prep.Name, nil
}

// seal locks the votes of prep, read fresh from the API server so that no
// vote in flight is missed. Votes submitted later are ignored. The status
// write triggers the reconcile that archives them.
func (r *PreparationReconciler) seal(ctx context.Context, prep *deliveryv1alpha1.Preparation) (ctrl.Result, error) {
	approvals, err := r.approvalsOf(ctx, r.APIReader, prep)
	if err != nil {
		return ctrl.Result{}, err
	}
	res := approval.Evaluate(prep, approvals)
	markApproval(prep, res)
	if !res.Approved {
		return ctrl.Result{}, nil
	}

	sealedTime := approval.NewSealTime(time.Now())
	prep.Status.Approval.SealedTime = &sealedTime
	prep.Status.Approval.Attestation = nil
	conditions.MarkFalse(prep, deliveryv1alpha1.ConditionTypeApprovalsSealed, deliveryv1alpha1.ReasonArchiving,
		"Recording the sealed approvals in the OCI registry")
	log.FromContext(ctx).Info("Sealed Approvals", "preparation", prep.Name, "approvedCount", res.Status.ApprovedCount)

	return ctrl.Result{}, nil
}

// archive records the sealed votes as a signed OCI attestation of the
// Preparation.
func (r *PreparationReconciler) archive(ctx context.Context, prep *deliveryv1alpha1.Preparation) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	state, err := r.Signing.State(ctx)
	if err != nil {
		logger.Info("Waiting for the signing key to archive Approvals", "preparation", prep.Name, "reason", err.Error())
		conditions.MarkFalse(prep, deliveryv1alpha1.ConditionTypeApprovalsSealed, deliveryv1alpha1.ReasonArchiveFailed, err.Error())
		return ctrl.Result{RequeueAfter: signingKeyRetryInterval}, nil
	}

	approvals, err := r.approvalsOf(ctx, r.APIReader, prep)
	if err != nil {
		return ctrl.Result{}, err
	}

	conn, err := r.artifactConnection(ctx, prep)
	if err != nil {
		return ctrl.Result{}, err
	}

	archived, err := approval.Archive(ctx, conn.Client, state.Signer, prep, approvals)
	if err != nil {
		conditions.MarkFalse(prep, deliveryv1alpha1.ConditionTypeApprovalsSealed, deliveryv1alpha1.ReasonArchiveFailed, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to archive Approvals: %w", err)
	}

	prep.Status.Approval.Attestation = &archived
	conditions.MarkTrue(prep, deliveryv1alpha1.ConditionTypeApprovalsSealed, deliveryv1alpha1.ReasonSealed,
		"Approvals sealed and recorded at "+archived.OCIRef)
	logger.Info("Recorded approval attestation", "preparation", prep.Name, "attestation", archived.OCIRef)
	return ctrl.Result{}, nil
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

// artifactConnection connects to the registry holding prep's artifact:
// through the Registry that authenticated its push, while it exists and
// still covers the artifact, and through the Order's registryRefs
// otherwise. So a Preparation stays readable when its Order moves its
// destination, and a deleted Order leaves the Registry the Preparation
// recorded.
func (r *PreparationReconciler) artifactConnection(ctx context.Context, prep *deliveryv1alpha1.Preparation) (registry.Connection, error) {
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

// approvalsOf lists the Approvals of prep with reader.
func (r *PreparationReconciler) approvalsOf(ctx context.Context, reader client.Reader, prep *deliveryv1alpha1.Preparation) ([]deliveryv1alpha1.Approval, error) {
	approvals := &deliveryv1alpha1.ApprovalList{}
	if err := reader.List(ctx, approvals,
		client.InNamespace(prep.Namespace),
		client.MatchingFields{deliveryv1alpha1.FieldPreparationRefName: prep.Name},
	); err != nil {
		return nil, fmt.Errorf("failed to list Approvals: %w", err)
	}
	return approvals.Items, nil
}

// patchStatus writes the status preparation took on in this reconcile, unless
// it is unchanged, and returns how to requeue: as the reconcile decided, or
// soon when the patch conflicts. The patch is locked to the version this
// reconcile read or last wrote, so that a reconcile from a stale cache never
// overwrites sealed votes.
func (r *PreparationReconciler) patchStatus(
	ctx context.Context, preparation *deliveryv1alpha1.Preparation, before *deliveryv1alpha1.PreparationStatus, result ctrl.Result, err error,
) (ctrl.Result, error) {
	preparation.Status.ObservedGeneration = preparation.Generation
	if equality.Semantic.DeepEqual(*before, preparation.Status) {
		return result, err
	}

	base := preparation.DeepCopy()
	base.Status = *before
	perr := r.Status().Patch(ctx, preparation, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	switch {
	case perr == nil, apierrors.IsNotFound(perr):
		return result, err
	case apierrors.IsConflict(perr) && err == nil:
		log.FromContext(ctx).V(1).Info("Preparation changed during the reconcile, reconciling it again")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	default:
		return result, errors.Join(err, fmt.Errorf("failed to patch Preparation status: %w", perr))
	}
}

// evidenceAttached reports whether prep's artifact is signed and its
// provenance attached.
func evidenceAttached(prep *deliveryv1alpha1.Preparation) bool {
	return meta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeSigned) &&
		meta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeProvenanceAttached)
}

// markEvidenceAttached records the signature and provenance attestations of
// the artifact and marks the Preparation as ready for serving.
func markEvidenceAttached(prep *deliveryv1alpha1.Preparation, signature, provenanceRef deliveryv1alpha1.AttestationReference) {
	setAttestation(&prep.Status.Attestations, signature)
	setAttestation(&prep.Status.Attestations, provenanceRef)
	conditions.MarkTrue(prep, deliveryv1alpha1.ConditionTypeSigned, deliveryv1alpha1.ReasonSigned, "Signature recorded at "+signature.OCIRef)
	conditions.MarkTrue(prep, deliveryv1alpha1.ConditionTypeProvenanceAttached, deliveryv1alpha1.ReasonProvenanceAttached,
		"Provenance recorded at "+provenanceRef.OCIRef)
	markEvidenceReady(prep)
}

// markSignFailed records that the artifact could not be signed, for reason.
// The Preparation is not ready until it is.
func markSignFailed(prep *deliveryv1alpha1.Preparation, reason string, err error) {
	conditions.MarkFalse(prep, deliveryv1alpha1.ConditionTypeSigned, reason, err.Error())
	markEvidenceReady(prep)
}

// markProvenanceFailed records that the provenance attestation could not be
// attached. The Preparation is not ready until it is.
func markProvenanceFailed(prep *deliveryv1alpha1.Preparation, err error) {
	conditions.MarkFalse(prep, deliveryv1alpha1.ConditionTypeProvenanceAttached, deliveryv1alpha1.ReasonProvenanceAttachFailed, err.Error())
	markEvidenceReady(prep)
}

// markEvidenceReady sets Ready from the evidence conditions: a Preparation
// is ready for serving once its artifact is signed and its provenance
// attached.
func markEvidenceReady(prep *deliveryv1alpha1.Preparation) {
	switch {
	case !meta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeSigned):
		conditions.MarkFalse(prep, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonAwaitingSignature,
			"Waiting for the artifact to be signed")
	case !meta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeProvenanceAttached):
		conditions.MarkFalse(prep, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonAwaitingProvenance,
			"Waiting for the provenance attestation to be recorded")
	default:
		conditions.MarkTrue(prep, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonReady, "Preparation is ready for serving")
	}
}

// markApproval records the aggregated approval status and the Approved
// condition.
func markApproval(prep *deliveryv1alpha1.Preparation, res approval.Result) {
	prep.Status.Approval = nil
	if res.Required {
		prep.Status.Approval = res.Status.DeepCopy()
	}
	condStatus := metav1.ConditionFalse
	if res.Approved {
		condStatus = metav1.ConditionTrue
	}
	conditions.Set(prep, deliveryv1alpha1.ConditionTypeApproved, condStatus, res.Reason, res.Message)
}

// setAttestation replaces the entry of the type of ref, or appends it.
func setAttestation(attestations *[]deliveryv1alpha1.AttestationReference, ref deliveryv1alpha1.AttestationReference) {
	for i := range *attestations {
		if (*attestations)[i].Type == ref.Type {
			(*attestations)[i] = ref
			return
		}
	}
	*attestations = append(*attestations, ref)
}

// attestationReference references the referrer with digest of subject.
func attestationReference(attestationType deliveryv1alpha1.AttestationType, subject oci.Reference, digest string) deliveryv1alpha1.AttestationReference {
	return deliveryv1alpha1.AttestationReference{
		Type:   attestationType,
		OCIRef: fmt.Sprintf("oci://%s@%s", subject.RepositoryReference(), digest),
		Digest: digest,
	}
}

// preparationOrder returns the Order a Preparation belongs to, for the index
// on deliveryv1alpha1.FieldOrderName.
func preparationOrder(obj client.Object) []string {
	preparation, ok := obj.(*deliveryv1alpha1.Preparation)
	if !ok {
		return nil
	}
	return []string{preparation.Spec.OrderName}
}

// enqueuePreparationForApproval enqueues the Preparation an Approval votes
// on.
func enqueuePreparationForApproval(_ context.Context, obj client.Object) []ctrl.Request {
	a, ok := obj.(*deliveryv1alpha1.Approval)
	if !ok {
		return nil
	}
	return []ctrl.Request{{Namespace: a.Namespace, Name: a.Spec.PreparationRef.Name}}
}

// enqueueTargetPreparation enqueues the Preparation a Serving converges to,
// which is sealed once it is approved.
func enqueueTargetPreparation(_ context.Context, obj client.Object) []ctrl.Request {
	serving, ok := obj.(*deliveryv1alpha1.Serving)
	if !ok || serving.Status.TargetPreparationName == "" {
		return nil
	}
	return []ctrl.Request{{Namespace: serving.Namespace, Name: serving.Status.TargetPreparationName}}
}

// servingTargetChanged passes the Serving updates that change the
// Preparation it converges to.
var servingTargetChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldServing, okOld := e.ObjectOld.(*deliveryv1alpha1.Serving)
		newServing, okNew := e.ObjectNew.(*deliveryv1alpha1.Serving)
		return okOld && okNew && oldServing.Status.TargetPreparationName != newServing.Status.TargetPreparationName
	},
}

// SetupWithManager sets up the controller with the Manager. It indexes
// Preparations by their Order, which the Serving controller lists them by.
func (r *PreparationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &deliveryv1alpha1.Preparation{},
		deliveryv1alpha1.FieldOrderName, preparationOrder); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Preparation{}).
		Watches(&deliveryv1alpha1.Approval{}, handler.EnqueueRequestsFromMapFunc(enqueuePreparationForApproval)).
		Watches(&deliveryv1alpha1.Serving{}, handler.EnqueueRequestsFromMapFunc(enqueueTargetPreparation),
			builder.WithPredicates(servingTargetChanged)).
		Named("preparation").
		Complete(r)
}
