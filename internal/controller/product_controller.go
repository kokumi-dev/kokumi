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
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	"sigs.k8s.io/controller-runtime/pkg/source"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/signing"
	signingsource "github.com/kokumi-dev/kokumi/internal/signing/source"
)

const (
	// productConcurrency is how many Products reconcile at a time. Checking
	// signatures waits on registries, not on the CPU.
	productConcurrency = 4
	// sourceVerificationRetryInterval is how often a source that did not
	// verify is verified again, for example after its publisher signed it.
	sourceVerificationRetryInterval = 5 * time.Minute
	// signaturesRefresh is how old the recorded check time of unchanged
	// signatures may get before a check refreshes it. Refreshing it on
	// every check would trigger the next reconcile with each status write.
	signaturesRefresh = time.Hour
)

// Keys of the Secrets a Product's spec.source.verify names.
const (
	publicKeySuffix = ".pub"
	trustedRootKey  = "trusted_root.json"
)

// Field indexes on Products.
const (
	// productRegistriesField indexes Products by the Registries their
	// spec.registryRefs list.
	productRegistriesField = "registryRefNames"
	// productVerifySecretsField indexes Products by the Secrets their
	// spec.source.verify reads keys or a trusted root from.
	productVerifySecretsField = "verifySecretNames"
)

// productStore resolves source artifacts and copies them to their vendor
// location. *artifact.Store implements it.
type productStore interface {
	ResolveDigest(ctx context.Context, src artifact.Source, srcClient oci.Client) (string, error)
	Copy(ctx context.Context, src artifact.Source, srcClient oci.Client, dst artifact.Destination, dstClient oci.Client) (string, error)
}

// productRegistries connects through the Registries a Product lists.
// *registry.Resolver implements it.
type productRegistries interface {
	Scope(ctx context.Context, namespace string, refs []corev1.LocalObjectReference) (registry.Scope, error)
}

// productSigning provides the trust sources verify against and tells when it
// changes. *signing.Provider implements it.
type productSigning interface {
	State(ctx context.Context) (signing.State, error)
	OnChange(notify func())
}

// sourceVerifier verifies every signature in the graph of the source at
// ref, pinned to its digest: against policy when it is set, else against
// trust. *signingsource.Verifier implements it.
type sourceVerifier interface {
	Verify(ctx context.Context, conn registry.Connection, ref oci.Reference, policy *signingsource.Policy, trust *signing.TrustSet) (*signingsource.Report, error)
}

// ProductReconciler reconciles a Product object.
type ProductReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Store  productStore
	// Registries connects to the source and the vendor location through
	// the Product's registryRefs.
	Registries productRegistries
	// Signing holds the keys whose signatures make a source trusted without
	// spec.source.verify, and whether sources must verify at all.
	Signing productSigning
	// SourceVerifier verifies sources against spec.source.verify.
	SourceVerifier sourceVerifier
}

// errSourceRejected reports a source whose signature did not verify, as
// opposed to a registry that could not be read. checked holds the verdicts
// of the signatures when they were checked.
type errSourceRejected struct {
	reason  string
	err     error
	checked *[]deliveryv1alpha1.SignatureVerdict
}

func (e *errSourceRejected) Error() string { return e.err.Error() }
func (e *errSourceRejected) Unwrap() error { return e.err }

// errTrustLookup reports that the signatures of a source that need not
// verify could not be looked up. The source is published, keeping what was
// verified before.
var errTrustLookup = errors.New("could not look up trusted signatures")

// productSource is where a Product's source is read from and vendored to,
// with the connections Kokumi uses for both.
type productSource struct {
	scope registry.Scope
	url   string
	conn  registry.Connection
	// vendorURL is "" when the Product does not vendor its source.
	vendorURL  string
	vendorConn registry.Connection
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile publishes the source a Product's Orders render from, and keeps a
// deleted Product until no Order references it.
func (r *ProductReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Product", "namespace", req.Namespace, "name", req.Name)

	product := &deliveryv1alpha1.Product{}
	if err := r.Get(ctx, req.NamespacedName, product); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Product resource not found, ignoring")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Product")

		return ctrl.Result{}, fmt.Errorf("failed to get Product: %w", err)
	}

	before := product.Status.DeepCopy()
	defer func() { result, err = r.patchStatus(ctx, product, before, result, err) }()

	if !product.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, product)
	}

	if !controllerutil.ContainsFinalizer(product, deliveryv1alpha1.FinalizerProductProtection) {
		controllerutil.AddFinalizer(product, deliveryv1alpha1.FinalizerProductProtection)

		if err := r.Update(ctx, product); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcileProduct(ctx, product)
}

// reconcileProduct connects to the Product's source and publishes it in
// status: resolved to a digest, verified, and vendored when configured.
// Orders render nothing until the source is published and verified as
// required.
func (r *ProductReconciler) reconcileProduct(ctx context.Context, product *deliveryv1alpha1.Product) (ctrl.Result, error) {
	state, err := r.Signing.State(ctx)
	if err != nil {
		// The provider notifies the watch once the key is loaded.
		log.FromContext(ctx).Info("Waiting for the signing key to verify the Product source", "reason", err.Error())
		conditions.MarkFalse(product, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonSigningKeyNotReady, err.Error())
		return ctrl.Result{}, nil
	}

	src, err := r.connect(ctx, product)
	if err != nil {
		return r.registryFailed(ctx, product, err)
	}
	product.Status.RegistryAccess = registry.ProductAccess(src.url, src.conn, src.vendorURL, src.vendorConn)

	configHash, err := resolve.CalculateProductHash(product.Spec.Source, src.vendorURL)
	if err != nil {
		return ctrl.Result{}, err
	}

	if product.Status.ConfigHash == configHash && product.Status.Source != nil {
		return r.reverify(ctx, product, state, src, configHash)
	}
	return r.publish(ctx, product, state, src, configHash)
}

// connect connects to the Product's source, and to its vendor location when
// it vendors, through the Product's Registries.
func (r *ProductReconciler) connect(ctx context.Context, product *deliveryv1alpha1.Product) (productSource, error) {
	scope, err := r.Registries.Scope(ctx, product.Namespace, product.Spec.RegistryRefs)
	if err != nil {
		return productSource{}, err
	}

	src := productSource{scope: scope, url: product.Spec.Source.URL}
	if src.conn, err = scope.Connect(src.url); err != nil {
		return productSource{}, err
	}
	if v := product.Spec.Vendor; v != nil {
		src.vendorURL = cmp.Or(v.URL, artifact.DefaultVendorDestination(product.Namespace, product.Name))
		if src.vendorConn, err = scope.Connect(src.vendorURL); err != nil {
			return productSource{}, err
		}
	}
	return src, nil
}

// reverify keeps the published source, which was resolved from the same
// inputs, without resolving or vendoring it again, but verifies it again,
// since the trust may have changed.
func (r *ProductReconciler) reverify(
	ctx context.Context, product *deliveryv1alpha1.Product, state signing.State, src productSource, configHash string,
) (ctrl.Result, error) {
	published := product.Status.Source.DeepCopy()

	check, err := r.verifySource(ctx, product, state, artifact.Source{OCI: src.url, Version: product.Spec.Source.Version, Digest: published.Digest}, src.conn)
	switch {
	case errors.Is(err, errTrustLookup):
		log.FromContext(ctx).Info("Keeping the recorded verification of the Product source", "reason", err.Error())
	case err != nil:
		return r.verificationFailed(ctx, product, err)
	default:
		published.Verified = check.verified
		recordSignatures(published, product.Status.Source, check.signatures, time.Now())
	}

	markPublished(product, configHash, published)
	return ctrl.Result{}, nil
}

// publish resolves the source to a digest, verifies it, and vendors it when
// configured. Only a verified source is vendored. The copy keeps the digest
// and the signatures attached as referrers, so it verifies like the source.
func (r *ProductReconciler) publish(
	ctx context.Context, product *deliveryv1alpha1.Product, state signing.State, src productSource, configHash string,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	upstream := artifact.Source{OCI: src.url, Version: product.Spec.Source.Version}
	digest, err := r.Store.ResolveDigest(ctx, upstream, src.conn.Client)
	if err != nil {
		return r.failed(ctx, product, fmt.Errorf("failed to resolve source digest: %w", err))
	}
	upstream.Digest = digest

	check, err := r.verifySource(ctx, product, state, upstream, src.conn)
	switch {
	case errors.Is(err, errTrustLookup):
		logger.Info("Publishing the Product source unverified", "reason", err.Error())
		check = &sourceCheck{}
	case err != nil:
		return r.verificationFailed(ctx, product, err)
	}

	published := &deliveryv1alpha1.ProductSourceStatus{
		URL:      src.url,
		Version:  product.Spec.Source.Version,
		Digest:   digest,
		Verified: check.verified,
	}
	recordSignatures(published, product.Status.Source, check.signatures, time.Now())

	if src.vendorURL != "" {
		pinned, err := oci.Parse(src.url + "@" + digest)
		if err != nil {
			return r.failed(ctx, product, fmt.Errorf("failed to parse source reference: %w", err))
		}
		// A private source copied to a location Kokumi writes without
		// credentials could be read there by anyone.
		if !src.vendorConn.Authenticated() && src.scope.Private(ctx, src.conn, pinned) {
			logger.Info("Refusing to vendor a private source without credentials", "source", src.url, "vendor", src.vendorURL)
			conditions.MarkFalse(product, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonCredentialsRequired,
				fmt.Sprintf("The source is private, and vendoring it to %s, which Kokumi writes without credentials, would make it public: "+
					"list a Registry with credentials for %s in spec.registryRefs", src.vendorURL, src.vendorURL))
			return ctrl.Result{}, nil
		}

		vendored, err := r.Store.Copy(ctx, upstream, src.conn.Client, artifact.Destination{OCI: src.vendorURL}, src.vendorConn.Client)
		if err != nil {
			return r.failed(ctx, product, fmt.Errorf("failed to vendor source: %w", err))
		}
		published.URL = src.vendorURL
		published.Digest = vendored
	}

	markPublished(product, configHash, published)
	logger.Info("Resolved Product source", "name", product.Name, "source", published.URL, "verified", published.Verified != nil)
	return ctrl.Result{}, nil
}

// markPublished publishes published, the source resolved from the inputs
// configHash records, so that the Product's Orders render from it. The
// SourceVerified condition reports published.verified, and is removed when
// the source was not verified.
func markPublished(product *deliveryv1alpha1.Product, configHash string, published *deliveryv1alpha1.ProductSourceStatus) {
	product.Status.Source = published
	product.Status.ConfigHash = configHash

	reason, msg := deliveryv1alpha1.ReasonResolved, "Source resolved"
	if product.Spec.Vendor != nil {
		reason, msg = deliveryv1alpha1.ReasonVendored, "Source vendored"
	}
	conditions.MarkTrue(product, deliveryv1alpha1.ConditionTypeReady, reason, msg)

	if published.Verified == nil {
		conditions.Remove(product, deliveryv1alpha1.ConditionTypeSourceVerified)
		return
	}
	conditions.MarkTrue(product, deliveryv1alpha1.ConditionTypeSourceVerified, deliveryv1alpha1.ReasonSucceeded,
		"Verified signature of "+verifiedSigner(published.Verified))
}

// failed records that the Product's source could not be resolved and
// returns err to be retried.
func (r *ProductReconciler) failed(ctx context.Context, product *deliveryv1alpha1.Product, err error) (ctrl.Result, error) {
	log.FromContext(ctx).Error(err, "Failed to resolve Product source")
	conditions.MarkFalse(product, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonFailed, err.Error())
	return ctrl.Result{}, err
}

// registryFailed records that the Product cannot connect to its source or
// vendor location, for the reason err carries, such as RegistryNotFound or
// SecretInvalid. The Registry watch reconciles the Product again once its
// Registries change. Errors without a reason are returned to be retried.
func (r *ProductReconciler) registryFailed(ctx context.Context, product *deliveryv1alpha1.Product, err error) (ctrl.Result, error) {
	reason := registry.Reason(err, "")
	if reason == "" {
		return ctrl.Result{}, fmt.Errorf("failed to connect to the Product's registries: %w", err)
	}
	log.FromContext(ctx).Info("Product cannot connect to its registries", "reason", reason, "error", err.Error())
	conditions.MarkFalse(product, deliveryv1alpha1.ConditionTypeReady, reason, err.Error())
	return ctrl.Result{}, nil
}

// verificationFailed records a source that did not verify, or returns err
// when the registry could not be read. Orders render nothing until it does.
// The published source is kept, but no longer recorded as verified. When
// the signatures were checked, their verdicts are recorded with it.
func (r *ProductReconciler) verificationFailed(ctx context.Context, product *deliveryv1alpha1.Product, err error) (ctrl.Result, error) {
	var rejected *errSourceRejected
	if !errors.As(err, &rejected) {
		return ctrl.Result{}, fmt.Errorf("failed to verify source: %w", err)
	}

	log.FromContext(ctx).Info("Product source did not verify", "reason", rejected.reason, "error", err.Error())
	if published := product.Status.Source; published != nil {
		published.Verified = nil
		if rejected.checked != nil {
			recordSignatures(published, published.DeepCopy(), *rejected.checked, time.Now())
		}
	}
	conditions.MarkFalse(product, deliveryv1alpha1.ConditionTypeSourceVerified, rejected.reason, err.Error())
	conditions.MarkFalse(product, deliveryv1alpha1.ConditionTypeReady, rejected.reason, err.Error())
	return ctrl.Result{RequeueAfter: sourceVerificationRetryInterval}, nil
}

// recordSignatures records signatures, the verdicts of a check made at now,
// in published. recorded is the source as last recorded, nil when there is
// none. The check time is kept while the verdicts are unchanged and it is
// younger than signaturesRefresh.
func recordSignatures(published, recorded *deliveryv1alpha1.ProductSourceStatus, signatures []deliveryv1alpha1.SignatureVerdict, now time.Time) {
	published.Signatures = signatures
	if recorded != nil && recorded.SignaturesCheckedTime != nil &&
		equality.Semantic.DeepEqual(recorded.Signatures, signatures) &&
		now.Sub(recorded.SignaturesCheckedTime.Time) < signaturesRefresh {
		published.SignaturesCheckedTime = recorded.SignaturesCheckedTime
		return
	}
	checked := metav1.NewTime(now)
	published.SignaturesCheckedTime = &checked
}

// verifiedSigner describes the signer verification recorded.
func verifiedSigner(verified *deliveryv1alpha1.SourceVerification) string {
	if verified.KeyID != "" {
		return "key " + verified.KeyID
	}
	return verified.Subject + " (issuer " + verified.Issuer + ")"
}

// sourceCheck is the result of checking the signatures of a source.
type sourceCheck struct {
	// verified is who signed the source itself; nil when it is not
	// verified.
	verified *deliveryv1alpha1.SourceVerification
	// signatures holds the verdict of every signature in the graph of the
	// source.
	signatures []deliveryv1alpha1.SignatureVerdict
}

// verifySource checks every signature in the graph of the source artifact
// src, pinned to its digest, at its source location. With
// spec.source.verify the source must be signed as configured there. Without,
// a signature by a key the Kitchen trusts verifies it: releases of this or
// another Kokumi installation. Its verified signer is nil when the source is
// not verified and need not be.
func (r *ProductReconciler) verifySource(ctx context.Context, product *deliveryv1alpha1.Product, state signing.State, src artifact.Source, conn registry.Connection) (*sourceCheck, error) {
	ref, err := oci.Parse(src.OCI + "@" + src.Digest)
	if err != nil {
		return nil, fmt.Errorf("parsing source reference: %w", err)
	}

	if verify := product.Spec.Source.Verify; verify != nil {
		policy, err := r.verificationPolicy(ctx, product.Namespace, verify)
		if err != nil {
			return nil, &errSourceRejected{reason: deliveryv1alpha1.ReasonVerificationError, err: err}
		}

		report, err := r.SourceVerifier.Verify(ctx, conn, ref, &policy, nil)
		if err != nil {
			// An unreachable registry and a missing signature both leave
			// the source unverified, as with cosign.
			return nil, &errSourceRejected{reason: deliveryv1alpha1.ReasonVerificationError, err: err}
		}

		check := newSourceCheck(report)
		if check.verified == nil {
			return nil, &errSourceRejected{
				reason:  deliveryv1alpha1.ReasonVerificationError,
				err:     fmt.Errorf("no signature of %s verifies against spec.source.verify%s", ref, describeVerdicts(report, ref.Digest)),
				checked: &check.signatures,
			}
		}
		return check, nil
	}

	report, err := r.SourceVerifier.Verify(ctx, conn, ref, nil, state.Trust)
	switch {
	case err != nil && state.RequireSourceVerification:
		// The registry could not be read; retry without changing the status.
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("%w: %w", errTrustLookup, err)
	}

	check := newSourceCheck(report)
	if check.verified == nil && state.RequireSourceVerification {
		return nil, &errSourceRejected{
			reason:  deliveryv1alpha1.ReasonVerificationRequired,
			err:     fmt.Errorf("the Kitchen requires sources to verify, and the source sets no spec.source.verify and carries no signature by a trusted Kokumi key%s", describeVerdicts(report, ref.Digest)),
			checked: &check.signatures,
		}
	}
	return check, nil
}

// newSourceCheck converts the report of a signature check.
func newSourceCheck(report *signingsource.Report) *sourceCheck {
	check := &sourceCheck{verified: sourceVerification(report.Signer)}
	for _, v := range report.Verdicts {
		check.signatures = append(check.signatures, deliveryv1alpha1.SignatureVerdict{
			Digest:  v.Digest,
			Subject: v.Subject,
			Result:  deliveryv1alpha1.SignatureResult(v.Result),
			Signer:  sourceVerification(v.Signer),
			Message: truncate(v.Message, maxVerdictMessage),
		})
	}
	return check
}

// maxVerdictMessage is the longest message a SignatureVerdict records.
const maxVerdictMessage = 1024

func sourceVerification(signer *signingsource.Signer) *deliveryv1alpha1.SourceVerification {
	if signer == nil {
		return nil
	}
	return &deliveryv1alpha1.SourceVerification{
		Provider: deliveryv1alpha1.VerificationProviderCosign,
		KeyID:    signer.KeyID,
		Issuer:   signer.Issuer,
		Subject:  signer.Subject,
	}
}

// describeVerdicts summarizes why none of the signatures of the source with
// digest verified, for an error message.
func describeVerdicts(report *signingsource.Report, digest string) string {
	var reasons []string
	for _, v := range report.Verdicts {
		if v.Subject != digest {
			continue
		}
		reason := string(v.Result)
		if v.Message != "" {
			reason += ": " + v.Message
		}
		reasons = append(reasons, reason)
	}
	if len(reasons) == 0 {
		return "; the source carries no signature"
	}
	return "; " + strings.Join(reasons, "; ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// verificationPolicy reads the keys or the trusted root verify names from
// Secrets in namespace.
func (r *ProductReconciler) verificationPolicy(ctx context.Context, namespace string, verify *deliveryv1alpha1.OCISourceVerification) (signingsource.Policy, error) {
	var policy signingsource.Policy

	if ref := verify.SecretRef; ref != nil {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, secret); err != nil {
			return policy, fmt.Errorf("reading verification keys: %w", err)
		}

		names := make([]string, 0, len(secret.Data))
		for name := range secret.Data {
			if strings.HasSuffix(name, publicKeySuffix) {
				names = append(names, name)
			}
		}
		slices.Sort(names)

		if len(names) == 0 {
			return policy, fmt.Errorf("secret %q holds no public key ending in %q", ref.Name, publicKeySuffix)
		}
		for _, name := range names {
			policy.Keys = append(policy.Keys, signingsource.Key{Name: name, PEM: secret.Data[name]})
		}
	}

	for _, identity := range verify.MatchOIDCIdentity {
		policy.Identities = append(policy.Identities, signingsource.Identity{Issuer: identity.Issuer, Subject: identity.Subject})
	}

	if ref := verify.TrustedRootSecretRef; ref != nil {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, secret); err != nil {
			return policy, fmt.Errorf("reading trusted root: %w", err)
		}
		if policy.TrustedRoot = secret.Data[trustedRootKey]; len(policy.TrustedRoot) == 0 {
			return policy, fmt.Errorf("secret %q holds no %q", ref.Name, trustedRootKey)
		}
	}

	return policy, nil
}

// reconcileDelete removes the protection finalizer once no Order references
// the Product any more. Until then the Product reports which Orders remain.
func (r *ProductReconciler) reconcileDelete(ctx context.Context, product *deliveryv1alpha1.Product) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Product")

	if !controllerutil.ContainsFinalizer(product, deliveryv1alpha1.FinalizerProductProtection) {
		return ctrl.Result{}, nil
	}

	orders := &deliveryv1alpha1.OrderList{}
	if err := r.List(ctx, orders,
		client.InNamespace(product.Namespace),
		client.MatchingFields{deliveryv1alpha1.FieldProductRefName: product.Name},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list Orders of Product: %w", err)
	}

	if len(orders.Items) > 0 {
		names := make([]string, 0, len(orders.Items))
		for _, order := range orders.Items {
			names = append(names, order.Name)
		}
		slices.Sort(names)

		// The Order watch reconciles again once one is deleted.
		logger.Info("Waiting for Orders to be deleted before deleting Product", "orders", names)
		conditions.MarkFalse(product, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonInUse,
			fmt.Sprintf("Waiting for %d Order(s) to be deleted: %s", len(names), strings.Join(names, ", ")))
		return ctrl.Result{}, nil
	}

	controllerutil.RemoveFinalizer(product, deliveryv1alpha1.FinalizerProductProtection)
	if err := r.Update(ctx, product); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Removed Product protection finalizer", "name", product.Name)
	return ctrl.Result{}, nil
}

// patchStatus writes the status product took on in this reconcile, unless it
// is unchanged, and returns how to requeue: as the reconcile decided, or soon
// when the patch conflicts. The patch is locked to the version this reconcile
// read or last wrote, so that a spec change while the source was resolved
// fails it: Orders render only from a Product that is Ready for its current
// generation.
func (r *ProductReconciler) patchStatus(
	ctx context.Context, product *deliveryv1alpha1.Product, before *deliveryv1alpha1.ProductStatus, result ctrl.Result, err error,
) (ctrl.Result, error) {
	product.Status.ObservedGeneration = product.Generation
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
		log.FromContext(ctx).V(1).Info("Product changed during the reconcile, reconciling it again")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	default:
		return ctrl.Result{}, errors.Join(err, fmt.Errorf("failed to patch Product status: %w", perr))
	}
}

// productRegistryNames returns the Registries a Product lists, for
// productRegistriesField.
func productRegistryNames(obj client.Object) []string {
	product, ok := obj.(*deliveryv1alpha1.Product)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(product.Spec.RegistryRefs))
	for _, ref := range product.Spec.RegistryRefs {
		names = append(names, ref.Name)
	}
	return names
}

// productVerifySecrets returns the Secrets a Product's spec.source.verify
// reads, for productVerifySecretsField.
func productVerifySecrets(obj client.Object) []string {
	product, ok := obj.(*deliveryv1alpha1.Product)
	if !ok || product.Spec.Source.Verify == nil {
		return nil
	}
	verify := product.Spec.Source.Verify
	var names []string
	if ref := verify.SecretRef; ref != nil {
		names = append(names, ref.Name)
	}
	if ref := verify.TrustedRootSecretRef; ref != nil && !slices.Contains(names, ref.Name) {
		names = append(names, ref.Name)
	}
	return names
}

// enqueueProducts enqueues the Products in namespace whose field index
// field holds value.
func (r *ProductReconciler) enqueueProducts(ctx context.Context, namespace, field, value string) []reconcile.Request {
	products := &deliveryv1alpha1.ProductList{}
	if err := r.List(ctx, products, client.InNamespace(namespace), client.MatchingFields{field: value}); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Products", "field", field, "value", value)
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(products.Items))
	for i := range products.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&products.Items[i])})
	}
	return reqs
}

// enqueueProductsForRegistry enqueues the Products that list the Registry
// in their registryRefs, so that they connect again when it changes.
func (r *ProductReconciler) enqueueProductsForRegistry(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.enqueueProducts(ctx, obj.GetNamespace(), productRegistriesField, obj.GetName())
}

// enqueueProductsForSecret enqueues the Products whose spec.source.verify
// reads the Secret, so that changed keys or a changed trusted root verify
// their sources again.
func (r *ProductReconciler) enqueueProductsForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.enqueueProducts(ctx, obj.GetNamespace(), productVerifySecretsField, obj.GetName())
}

// enqueueProductOfOrder enqueues the Product an Order references.
func enqueueProductOfOrder(_ context.Context, obj client.Object) []reconcile.Request {
	order, ok := obj.(*deliveryv1alpha1.Order)
	if !ok {
		return nil
	}
	return []reconcile.Request{{Namespace: order.Namespace, Name: order.Spec.ProductRef.Name}}
}

// enqueueAllProducts enqueues every Product, so that their sources are
// verified again against changed trust.
func (r *ProductReconciler) enqueueAllProducts(ctx context.Context, _ client.Object) []reconcile.Request {
	products := &deliveryv1alpha1.ProductList{}
	if err := r.List(ctx, products); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Products")
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(products.Items))
	for i := range products.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&products.Items[i])})
	}
	return reqs
}

// signingChanges returns a channel that receives an event whenever the
// trust of the signing state changes.
func (r *ProductReconciler) signingChanges() <-chan event.GenericEvent {
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

// orderCreatedOrDeleted passes the Order events a deleted Product waits for:
// a deleted Order may release it, and a created one keeps it and is
// reported. productRef is immutable, so updates never matter.
var orderCreatedOrDeleted = predicate.Funcs{
	CreateFunc:  func(event.CreateEvent) bool { return true },
	UpdateFunc:  func(event.UpdateEvent) bool { return false },
	DeleteFunc:  func(event.DeleteEvent) bool { return true },
	GenericFunc: func(event.GenericEvent) bool { return false },
}

// SetupWithManager sets up the controller with the Manager. It indexes
// Products by the Registries and Secrets they read, which the Order
// controller lists them by as well.
func (r *ProductReconciler) SetupWithManager(mgr ctrl.Manager) error {
	indexer := mgr.GetFieldIndexer()
	if err := indexer.IndexField(context.Background(), &deliveryv1alpha1.Product{}, productRegistriesField, productRegistryNames); err != nil {
		return err
	}
	if err := indexer.IndexField(context.Background(), &deliveryv1alpha1.Product{}, productVerifySecretsField, productVerifySecrets); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Product{}).
		Watches(&deliveryv1alpha1.Registry{}, handler.EnqueueRequestsFromMapFunc(r.enqueueProductsForRegistry)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.enqueueProductsForSecret)).
		Watches(&deliveryv1alpha1.Order{}, handler.EnqueueRequestsFromMapFunc(enqueueProductOfOrder), builder.WithPredicates(orderCreatedOrDeleted)).
		WatchesRawSource(source.Channel(r.signingChanges(), handler.EnqueueRequestsFromMapFunc(r.enqueueAllProducts))).
		Named("product").
		WithOptions(controller.Options{MaxConcurrentReconciles: productConcurrency}).
		Complete(r)
}
