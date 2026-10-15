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
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/index"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// ProductReconciler reconciles a Product object.
type ProductReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Store          *artifact.Store
	PantryResolver credential.PantryResolver
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=products/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=pantries,verbs=get;list;watch
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

	return ctrl.Result{}, r.reconcileProduct(ctx, product)
}

// reconcileProduct resolves the Product's source and publishes it in status.
func (r *ProductReconciler) reconcileProduct(ctx context.Context, product *deliveryv1alpha1.Product) error {
	logger := log.FromContext(ctx)
	statusUpdater := status.NewProductUpdater(r.Client)

	fail := func(err error) error {
		logger.Error(err, "Failed to resolve Product source")
		if uerr := statusUpdater.Failed(ctx, product, err); uerr != nil {
			logger.Error(uerr, "Failed to update Product status")
		}
		return err
	}

	sourceURL, sourceClient, err := r.PantryResolver.ResolveSource(ctx, product.Spec.Source, product.Namespace)
	if err != nil {
		return fail(fmt.Errorf("failed to resolve source Pantry: %w", err))
	}

	var (
		vendorURL    string
		vendorClient oci.Client
	)
	if product.Spec.Vendor != nil {
		vendorURL, vendorClient, err = r.PantryResolver.ResolveDestination(ctx, product.Spec.Vendor,
			artifact.DefaultVendorDestination(product.Namespace, product.Name), product.Namespace)
		if err != nil {
			return fail(fmt.Errorf("failed to resolve vendor destination: %w", err))
		}
	}

	configHash, err := resolve.CalculateProductHash(product.Spec.Source, product.Spec.Vendor, sourceURL, vendorURL)
	if err != nil {
		return err
	}

	// The published source was resolved from the same inputs: mark it ready
	// for this generation without resolving or vendoring it again.
	if product.Status.ConfigHash == configHash && product.Status.Source != nil {
		return statusUpdater.Ready(ctx, product, configHash, product.Status.Source)
	}

	src := artifact.Source{OCI: sourceURL, Version: product.Spec.Source.Version}

	published := &deliveryv1alpha1.ProductSourceStatus{
		OCI:       sourceURL,
		Version:   product.Spec.Source.Version,
		PantryRef: product.Spec.Source.PantryRef,
	}

	if product.Spec.Vendor == nil {
		digest, err := r.Store.ResolveDigest(ctx, src, sourceClient)
		if err != nil {
			return fail(fmt.Errorf("failed to resolve source digest: %w", err))
		}
		published.Digest = digest
	} else {
		digest, err := r.Store.Copy(ctx, src, sourceClient, artifact.Destination{OCI: vendorURL}, vendorClient)
		if err != nil {
			return fail(fmt.Errorf("failed to vendor source: %w", err))
		}
		published.OCI = vendorURL
		published.PantryRef = product.Spec.Vendor.PantryRef
		published.Digest = digest
	}

	if err := statusUpdater.Ready(ctx, product, configHash, published); err != nil {
		return err
	}

	logger.Info("Resolved Product source", "name", product.Name, "source", published.OCI)
	return nil
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

// enqueueProductsForPantry enqueues the Products whose source or vendor
// destination references the Pantry, so that a changed Pantry URL resolves
// them again.
func (r *ProductReconciler) enqueueProductsForPantry() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		products, err := index.ProductsForPantry(ctx, r.Client, obj.GetNamespace(), obj.GetName())
		if err != nil {
			log.FromContext(ctx).Error(err, "Failed to list Products of Pantry", "pantry", obj.GetName())
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
		Watches(&deliveryv1alpha1.Pantry{}, r.enqueueProductsForPantry()).
		Watches(&deliveryv1alpha1.Order{}, enqueueProductOfOrder(), builder.WithPredicates(orderCreatedOrDeleted)).
		Named("product").
		Complete(r)
}
