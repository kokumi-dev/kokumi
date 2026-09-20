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
	"sigs.k8s.io/controller-runtime/pkg/log"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// MenuReconciler reconciles a Menu object.
type MenuReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Store          *artifact.Store
	Pipeline       *artifact.Pipeline
	PantryResolver credential.PantryResolver
}

// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=menus,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=menus/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=menus/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *MenuReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Menu", "namespace", req.Namespace, "name", req.Name)

	menu := &deliveryv1alpha1.Menu{}

	if err := r.Get(ctx, req.NamespacedName, menu); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Menu resource not found, ignoring")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Menu")

		return ctrl.Result{}, fmt.Errorf("failed to get Menu: %w", err)
	}

	if !menu.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, menu)
	}

	if !controllerutil.ContainsFinalizer(menu, deliveryv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(menu, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, menu); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcileMenu(ctx, menu)
}

// reconcileMenu resolves the Menu's consumable artifact source and andles CRD
// concerns with updating its status.
func (r *MenuReconciler) reconcileMenu(ctx context.Context, menu *deliveryv1alpha1.Menu) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	statusUpdater := status.NewMenuUpdater(r.Client)

	configHash, err := resolve.CalculateMenuHash(menu.Spec)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compute Menu config hash: %w", err)
	}

	if menu.Status.Source != nil && menu.Status.ConfigHash == configHash {
		logger.V(4).Info("Menu config unchanged, skipping re-resolve", "name", menu.Name)
		return ctrl.Result{}, nil
	}

	source, err := r.resolveSource(ctx, menu)
	if err != nil {
		logger.Error(err, "Failed to resolve Menu source")
		if uerr := statusUpdater.Failed(ctx, menu, err); uerr != nil {
			logger.Error(uerr, "Failed to update Menu status")
		}
		return ctrl.Result{}, err
	}

	if err := statusUpdater.Ready(ctx, menu, configHash, source); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Menu source published", "name", menu.Name, "source", source.OCI)
	return ctrl.Result{}, nil
}

// resolveSource resolves the Menu's consumable source
func (r *MenuReconciler) resolveSource(ctx context.Context, menu *deliveryv1alpha1.Menu) (*deliveryv1alpha1.MenuSourceStatus, error) {
	resolved, srcClient, err := r.PantryResolver.ResolveSource(ctx, menu.Spec.Source, menu.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve source Pantry: %w", err)
	}

	if menu.Spec.Vendor == nil {
		return r.advertiseUpstream(ctx, menu, resolved, srcClient)
	}

	destURL, destClient, err := r.resolveDestination(ctx, menu)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve vendor destination: %w", err)
	}

	if effectiveVendorMode(menu) == deliveryv1alpha1.VendorModeRender {
		return r.publishRendered(ctx, menu, resolved, srcClient, destURL, destClient)
	}

	return r.vendorCopy(ctx, menu, resolved, srcClient, destURL, destClient)
}

// advertiseUpstream advertises the upstream source without vendoring,
// resolving the current digest best-effort.
func (r *MenuReconciler) advertiseUpstream(ctx context.Context, menu *deliveryv1alpha1.Menu, resolved deliveryv1alpha1.OCISource, srcClient oci.Client) (*deliveryv1alpha1.MenuSourceStatus, error) {
	digest, _ := r.Store.ResolveDigest(ctx, artifact.Source{OCI: resolved.OCI, Version: menu.Spec.Source.Version}, srcClient)

	return &deliveryv1alpha1.MenuSourceStatus{
		OCI:       resolved.OCI,
		Version:   menu.Spec.Source.Version,
		PantryRef: menu.Spec.Source.PantryRef,
		Digest:    digest,
	}, nil
}

// publishRendered renders the Menu artifact per spec.render, applies the
// Menu's patches, and pushes it to the destination with a prerendered marker.
func (r *MenuReconciler) publishRendered(ctx context.Context, menu *deliveryv1alpha1.Menu, resolved deliveryv1alpha1.OCISource, srcClient oci.Client, destURL string, destClient oci.Client) (*deliveryv1alpha1.MenuSourceStatus, error) {
	if menu.Spec.Render == nil {
		return nil, fmt.Errorf("vendor mode Render requires spec.render to be set")
	}

	spec, err := resolve.MenuArtifactSpec(menu)
	if err != nil {
		return nil, err
	}

	result, err := r.Pipeline.Render(ctx, artifact.RenderRequest{
		Source:       artifact.Source{OCI: resolved.OCI, Version: menu.Spec.Source.Version},
		SourceClient: srcClient,
		Destination:  artifact.Destination{OCI: destURL},
		DestClient:   destClient,
		Render:       spec.Render,
		Patches:      spec.Patches,
		Name:         menu.Name,
		Namespace:    menu.Namespace,
		Description:  fmt.Sprintf("Rendered and patched by Menu %s", menu.Name),
		ExtraAnnotations: map[string]string{
			artifact.AnnotationPrerendered: "true",
		},
	})
	if err != nil {
		return nil, err
	}

	return &deliveryv1alpha1.MenuSourceStatus{
		OCI:         destURL,
		Version:     menu.Spec.Source.Version,
		PantryRef:   menu.Spec.Vendor.Destination.PantryRef,
		Digest:      result.DestRef.Digest,
		PreRendered: true,
	}, nil
}

// vendorCopy copies the upstream artifact to the destination registry unchanged.
func (r *MenuReconciler) vendorCopy(ctx context.Context, menu *deliveryv1alpha1.Menu, resolved deliveryv1alpha1.OCISource, srcClient oci.Client, destURL string, destClient oci.Client) (*deliveryv1alpha1.MenuSourceStatus, error) {
	digest, err := r.Store.Copy(ctx,
		artifact.Source{OCI: resolved.OCI, Version: menu.Spec.Source.Version}, srcClient,
		artifact.Destination{OCI: destURL}, destClient,
	)
	if err != nil {
		return nil, err
	}

	return &deliveryv1alpha1.MenuSourceStatus{
		OCI:       destURL,
		Version:   menu.Spec.Source.Version,
		PantryRef: menu.Spec.Vendor.Destination.PantryRef,
		Digest:    digest,
	}, nil
}

// resolveDestination resolves the vendor destination into a plain OCI URL and client.
func (r *MenuReconciler) resolveDestination(ctx context.Context, menu *deliveryv1alpha1.Menu) (string, oci.Client, error) {
	if menu.Spec.Vendor.Destination.PantryRef != nil {
		resolved, c, err := r.PantryResolver.ResolveSource(ctx, deliveryv1alpha1.OCISource{
			PantryRef: menu.Spec.Vendor.Destination.PantryRef,
		}, menu.Namespace)
		if err != nil {
			return "", nil, err
		}
		return resolved.OCI, c, nil
	}

	if menu.Spec.Vendor.Destination.OCI != "" {
		return menu.Spec.Vendor.Destination.OCI, nil, nil
	}

	return artifact.DefaultDestination(menu.Namespace, menu.Name), nil, nil
}

// effectiveVendorMode returns the Menu's vendor mode, defaulting to Render.
func effectiveVendorMode(menu *deliveryv1alpha1.Menu) deliveryv1alpha1.VendorMode {
	if menu.Spec.Vendor == nil || menu.Spec.Vendor.Mode == "" {
		return deliveryv1alpha1.VendorModeRender
	}
	return menu.Spec.Vendor.Mode
}

// reconcileDelete removes the finalizer from the Menu, allowing garbage collection.
func (r *MenuReconciler) reconcileDelete(ctx context.Context, menu *deliveryv1alpha1.Menu) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of Menu")

	if controllerutil.ContainsFinalizer(menu, deliveryv1alpha1.Finalizer) {
		logger.Info("Cleaning up Menu resources")

		controllerutil.RemoveFinalizer(menu, deliveryv1alpha1.Finalizer)

		if err := r.Update(ctx, menu); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MenuReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Menu{}).
		Named("menu").
		Complete(r)
}
