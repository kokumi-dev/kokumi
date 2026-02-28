package server

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

// Counts holds the current resource count for each CRD type.
type Counts struct {
	Recipes      int `json:"recipes"`
	Preparations int `json:"preparations"`
	Servings     int `json:"servings"`
}

// eventCounts is the SSE event type name for resource count updates.
const eventCounts = "counts"

// newScheme builds a runtime Scheme with the types the server needs.
func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(deliveryv1alpha1.AddToScheme(s))
	return s
}

// startK8sWatcher connects to the Kubernetes API, registers informers for
// Recipe, Preparation, and Serving resources, and broadcasts updated Counts
// to h on every add / update / delete event.
//
// If no Kubernetes config is found (e.g. running outside a cluster without a
// kubeconfig) the function logs the situation and returns nil; the hub simply
// stays idle.
func startK8sWatcher(ctx context.Context, logger logr.Logger, h *hub) error {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		logger.Info("No Kubernetes config found, resource counts will not be available", "error", err)
		return nil
	}

	scheme := newScheme()

	k8sCache, err := cache.New(cfg, cache.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("creating Kubernetes cache: %w", err)
	}

	recipeInformer, err := k8sCache.GetInformer(ctx, &deliveryv1alpha1.Recipe{})
	if err != nil {
		return fmt.Errorf("getting Recipe informer: %w", err)
	}

	prepInformer, err := k8sCache.GetInformer(ctx, &deliveryv1alpha1.Preparation{})
	if err != nil {
		return fmt.Errorf("getting Preparation informer: %w", err)
	}

	servingInformer, err := k8sCache.GetInformer(ctx, &deliveryv1alpha1.Serving{})
	if err != nil {
		return fmt.Errorf("getting Serving informer: %w", err)
	}

	// refresh reads counts from the in-memory informer stores and broadcasts
	// them to all SSE subscribers. k8sCache implements client.Reader so List
	// reads from the local cache — no network call to the Kubernetes API.
	refresh := func() {
		recipeList := &deliveryv1alpha1.RecipeList{}
		if err := k8sCache.List(ctx, recipeList); err != nil {
			logger.Error(err, "Failed to list Recipes from cache")
			return
		}

		prepList := &deliveryv1alpha1.PreparationList{}
		if err := k8sCache.List(ctx, prepList); err != nil {
			logger.Error(err, "Failed to list Preparations from cache")
			return
		}

		servingList := &deliveryv1alpha1.ServingList{}
		if err := k8sCache.List(ctx, servingList); err != nil {
			logger.Error(err, "Failed to list Servings from cache")
			return
		}

		if err := h.publish(eventCounts, Counts{
			Recipes:      len(recipeList.Items),
			Preparations: len(prepList.Items),
			Servings:     len(servingList.Items),
		}); err != nil {
			logger.Error(err, "Failed to publish counts event")
		}
	}

	handler := toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ any) { refresh() },
		UpdateFunc: func(_, _ any) { refresh() },
		DeleteFunc: func(_ any) { refresh() },
	}

	if _, err := recipeInformer.AddEventHandler(handler); err != nil {
		return fmt.Errorf("adding Recipe event handler: %w", err)
	}
	if _, err := prepInformer.AddEventHandler(handler); err != nil {
		return fmt.Errorf("adding Preparation event handler: %w", err)
	}
	if _, err := servingInformer.AddEventHandler(handler); err != nil {
		return fmt.Errorf("adding Serving event handler: %w", err)
	}

	// Start the cache in the background; it runs until ctx is cancelled.
	go func() {
		if err := k8sCache.Start(ctx); err != nil {
			logger.Error(err, "Kubernetes cache stopped with error")
		}
	}()

	// After the cache has synced, broadcast the current state so that clients
	// connecting before the first Kubernetes event receive counts immediately.
	go func() {
		if !k8sCache.WaitForCacheSync(ctx) {
			return
		}
		refresh()
	}()

	return nil
}
