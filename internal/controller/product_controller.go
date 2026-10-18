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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/kokumi-dev/kokumi/internal/index"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/signing"
	signingsource "github.com/kokumi-dev/kokumi/internal/signing/source"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// sourceVerificationRetryInterval is how often a source that did not verify
// is verified again, for example after its publisher signed it.
const sourceVerificationRetryInterval = 5 * time.Minute

// Keys of the Secrets a Product's spec.source.verify names.
const (
	publicKeySuffix = ".pub"
	trustedRootKey  = "trusted_root.json"
)

// ProductReconciler reconciles a Product object.
type ProductReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Store  *artifact.Store
	// Registries connects to the source and the vendor location through
	// the Product's registryRefs.
	Registries registryConnector
	// Signing holds the keys whose signatures make a source trusted without
	// spec.source.verify, and whether sources must verify at all.
	Signing *signing.Provider
	// SourceVerifier verifies sources against spec.source.verify.
	SourceVerifier SourceVerifier
}

// SourceVerifier verifies every signature in the graph of the source at
// ref, pinned to its digest: against policy when it is set, else against
// trust.
type SourceVerifier interface {
	Verify(ctx context.Context, conn registry.Connection, ref oci.Reference, policy *signingsource.Policy, trust *signing.TrustSet) (*signingsource.Report, error)
}

// productConcurrency is how many Products reconcile at a time. Checking
// signatures waits on registries, not on the CPU.
const productConcurrency = 4

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

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=registries,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile resolves the source a Product's Orders render from, vendoring it
// when configured, and keeps a deleted Product until no Order references it.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *ProductReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Product", "namespace", req.Namespace, "name", req.Name)

	product := &deliveryv1alpha1.Product{}
	if err := r.Get(ctx, req.NamespacedName, product); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Product resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get Product: %w", err)
	}

	if !product.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.reconcileDelete(ctx, product)
	}

	if !controllerutil.ContainsFinalizer(product, deliveryv1alpha1.FinalizerProductProtection) {
		controllerutil.AddFinalizer(product, deliveryv1alpha1.FinalizerProductProtection)
		if err := r.Update(ctx, product); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcileProduct(ctx, product)
}

// reconcileProduct resolves the Product's source, verifies its signature,
// vendors it when configured, and publishes it in status. Orders render
// nothing until the source is published and verified as required.
func (r *ProductReconciler) reconcileProduct(ctx context.Context, product *deliveryv1alpha1.Product) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	statusUpdater := status.NewProductUpdater(r.Client)

	fail := func(err error) (ctrl.Result, error) {
		logger.Error(err, "Failed to resolve Product source")
		if uerr := statusUpdater.Failed(ctx, product, err); uerr != nil {
			logger.Error(uerr, "Failed to update Product status")
		}
		return ctrl.Result{}, err
	}

	state, err := r.Signing.State(ctx)
	if err != nil {
		// The provider notifies the watch once the key is loaded.
		logger.Info("Waiting for the signing key to verify the Product source", "reason", err.Error())
		return ctrl.Result{}, statusUpdater.NotReady(ctx, product, deliveryv1alpha1.ReasonSigningKeyNotReady, err.Error())
	}

	scope, err := registryScope(ctx, r.Client, r.Registries, product.Namespace, product.Spec.RegistryRefs)
	if err != nil {
		return r.registryFailed(ctx, product, err)
	}
	sourceURL := product.Spec.Source.URL
	sourceConn, err := scope.Connect(sourceURL)
	if err != nil {
		return r.registryFailed(ctx, product, err)
	}

	var (
		vendorURL  string
		vendorConn registry.Connection
	)
	if v := product.Spec.Vendor; v != nil {
		vendorURL = cmp.Or(v.URL, artifact.DefaultVendorDestination(product.Namespace, product.Name))
		if vendorConn, err = scope.Connect(vendorURL); err != nil {
			return r.registryFailed(ctx, product, err)
		}
	}

	// The status is written once, by whichever write ends the reconcile.
	statusUpdater = statusUpdater.WithRegistryAccess(productRegistryAccess(sourceURL, sourceConn, vendorURL, vendorConn))

	configHash, err := resolve.CalculateProductHash(product.Spec.Source, vendorURL)
	if err != nil {
		return ctrl.Result{}, err
	}

	src := artifact.Source{OCI: sourceURL, Version: product.Spec.Source.Version}

	// The published source was resolved from the same inputs: keep its
	// digest without resolving or vendoring it again, but verify it again,
	// since the trust may have changed.
	if product.Status.ConfigHash == configHash && product.Status.Source != nil {
		published := product.Status.Source.DeepCopy()
		src.Digest = published.Digest

		check, err := r.verifySource(ctx, product, state, src, sourceConn)
		switch {
		case errors.Is(err, errTrustLookup):
			logger.Info("Keeping the recorded verification of the Product source", "reason", err.Error())
		case err != nil:
			return r.verificationFailed(ctx, product, err)
		default:
			published.Verified = check.verified
			status.RecordSignatures(published, product.Status.Source, check.signatures, time.Now())
		}

		return ctrl.Result{}, statusUpdater.Ready(ctx, product, configHash, published)
	}

	digest, err := r.Store.ResolveDigest(ctx, src, sourceConn.Client)
	if err != nil {
		return fail(fmt.Errorf("failed to resolve source digest: %w", err))
	}
	src.Digest = digest

	check, err := r.verifySource(ctx, product, state, src, sourceConn)
	switch {
	case errors.Is(err, errTrustLookup):
		logger.Info("Publishing the Product source unverified", "reason", err.Error())
		check = &sourceCheck{}
	case err != nil:
		return r.verificationFailed(ctx, product, err)
	}

	published := &deliveryv1alpha1.ProductSourceStatus{
		URL:      sourceURL,
		Version:  product.Spec.Source.Version,
		Digest:   digest,
		Verified: check.verified,
	}
	status.RecordSignatures(published, product.Status.Source, check.signatures, time.Now())

	// Only a verified source is vendored. The copy keeps the digest and the
	// signatures attached as referrers, so it verifies like the source.
	if product.Spec.Vendor != nil {
		pinned, err := oci.Parse(sourceURL + "@" + digest)
		if err != nil {
			return fail(fmt.Errorf("failed to parse source reference: %w", err))
		}
		// A private source copied to a location Kokumi writes without
		// credentials could be read there by anyone.
		if !vendorConn.Authenticated() && scope.Private(ctx, sourceConn, pinned) {
			msg := fmt.Sprintf("The source is private, and vendoring it to %s, which Kokumi writes without credentials, would make it public: "+
				"list a Registry with credentials for %s in spec.registryRefs", vendorURL, vendorURL)
			logger.Info("Refusing to vendor a private source without credentials", "source", sourceURL, "vendor", vendorURL)
			return ctrl.Result{}, statusUpdater.NotReady(ctx, product, deliveryv1alpha1.ReasonCredentialsRequired, msg)
		}

		vendored, err := r.Store.Copy(ctx, src, sourceConn.Client, artifact.Destination{OCI: vendorURL}, vendorConn.Client)
		if err != nil {
			return fail(fmt.Errorf("failed to vendor source: %w", err))
		}
		published.URL = vendorURL
		published.Digest = vendored
	}

	if err := statusUpdater.Ready(ctx, product, configHash, published); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Resolved Product source", "name", product.Name, "source", published.URL, "verified", published.Verified != nil)
	return ctrl.Result{}, nil
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
	return ctrl.Result{}, status.NewProductUpdater(r.Client).NotReady(ctx, product, reason, err.Error())
}

// productRegistryAccess returns the registry locations of a Product: its
// source, and its vendor location when it vendors.
func productRegistryAccess(sourceURL string, sourceConn registry.Connection, vendorURL string, vendorConn registry.Connection) []deliveryv1alpha1.RegistryAccess {
	access := []deliveryv1alpha1.RegistryAccess{{
		URL: sourceURL, Operation: deliveryv1alpha1.RegistryOperationPull, Client: deliveryv1alpha1.RegistryClientKokumi, Registry: sourceConn.RegistryName(),
	}}
	if vendorURL != "" {
		access = append(access, deliveryv1alpha1.RegistryAccess{
			URL: vendorURL, Operation: deliveryv1alpha1.RegistryOperationPush, Client: deliveryv1alpha1.RegistryClientKokumi, Registry: vendorConn.RegistryName(),
		})
	}
	return access
}

// verificationFailed records a source that did not verify, or returns err
// when the registry could not be read.
func (r *ProductReconciler) verificationFailed(ctx context.Context, product *deliveryv1alpha1.Product, err error) (ctrl.Result, error) {
	var rejected *errSourceRejected
	if !errors.As(err, &rejected) {
		return ctrl.Result{}, fmt.Errorf("failed to verify source: %w", err)
	}

	log.FromContext(ctx).Info("Product source did not verify", "reason", rejected.reason, "error", err.Error())
	if err := status.NewProductUpdater(r.Client).VerificationFailed(ctx, product, rejected.reason, err, rejected.checked); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: sourceVerificationRetryInterval}, nil
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
func (r *ProductReconciler) reconcileDelete(ctx context.Context, product *deliveryv1alpha1.Product) error {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(product, deliveryv1alpha1.FinalizerProductProtection) {
		return nil
	}

	orders, err := index.OrdersForProduct(ctx, r.Client, product.Namespace, product.Name)
	if err != nil {
		return fmt.Errorf("failed to list Orders of Product: %w", err)
	}

	if len(orders) > 0 {
		names := make([]string, 0, len(orders))
		for _, order := range orders {
			names = append(names, order.Name)
		}
		slices.Sort(names)

		logger.Info("Waiting for Orders to be deleted before deleting Product", "orders", names)
		return status.NewProductUpdater(r.Client).InUse(ctx, product,
			fmt.Sprintf("Waiting for %d Order(s) to be deleted: %s", len(names), strings.Join(names, ", ")))
	}

	controllerutil.RemoveFinalizer(product, deliveryv1alpha1.FinalizerProductProtection)
	if err := r.Update(ctx, product); err != nil {
		return err
	}

	logger.Info("Removed Product protection finalizer", "name", product.Name)
	return nil
}

// enqueueProductsForRegistry enqueues the Products that list the Registry
// in their registryRefs, so that they connect again when it changes.
func (r *ProductReconciler) enqueueProductsForRegistry() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		products, err := index.ProductsForRegistry(ctx, r.Client, obj.GetNamespace(), obj.GetName())
		if err != nil {
			log.FromContext(ctx).Error(err, "Failed to list Products of Registry", "registry", obj.GetName())
			return nil
		}

		reqs := make([]reconcile.Request, 0, len(products))
		for _, product := range products {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&product)})
		}
		return reqs
	})
}

// enqueueProductsForSecret enqueues the Products whose spec.source.verify
// reads the Secret, so that changed keys or a changed trusted root verify
// their sources again.
func (r *ProductReconciler) enqueueProductsForSecret() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		products, err := index.ProductsForSecret(ctx, r.Client, obj.GetNamespace(), obj.GetName())
		if err != nil {
			log.FromContext(ctx).Error(err, "Failed to list Products of Secret", "secret", obj.GetName())
			return nil
		}

		reqs := make([]reconcile.Request, 0, len(products))
		for _, product := range products {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&product)})
		}
		return reqs
	})
}

// enqueueProductOfOrder enqueues the Product an Order references.
func enqueueProductOfOrder() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		order, ok := obj.(*deliveryv1alpha1.Order)
		if !ok {
			return nil
		}
		return []reconcile.Request{{Namespace: order.Namespace, Name: order.Spec.ProductRef.Name}}
	})
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

// SetupWithManager sets up the controller with the Manager.
func (r *ProductReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// A deleted Product waits for its Orders: a deleted Order may release it,
	// and a created one keeps it and is reported. productRef is immutable, so
	// updates never matter.
	orderCreatedOrDeleted := predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		UpdateFunc:  func(event.UpdateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Product{}).
		Watches(&deliveryv1alpha1.Registry{}, r.enqueueProductsForRegistry()).
		Watches(&corev1.Secret{}, r.enqueueProductsForSecret()).
		Watches(&deliveryv1alpha1.Order{}, enqueueProductOfOrder(), builder.WithPredicates(orderCreatedOrDeleted)).
		WatchesRawSource(source.Channel(r.Signing.Changes(), handler.EnqueueRequestsFromMapFunc(r.enqueueAllProducts))).
		Named("product").
		WithOptions(controller.Options{MaxConcurrentReconciles: productConcurrency}).
		Complete(r)
}
