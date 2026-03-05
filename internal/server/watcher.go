package server

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/spf13/afero"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Counts holds the current resource count for each CRD type.
type Counts struct {
	Recipes      int `json:"recipes"`
	Preparations int `json:"preparations"`
	Servings     int `json:"servings"`
}

const (
	// eventCounts is the SSE event type name for resource count updates.
	eventCounts = "counts"
	// eventRecipes is the SSE event type name for full recipe list snapshots.
	eventRecipes = "recipes"
	// eventPreparations is the SSE event type name for full preparation list snapshots.
	eventPreparations = "preparations"
	// eventServings is the SSE event type name for full serving list snapshots.
	eventServings = "servings"
)

// newScheme builds a runtime Scheme with the types the server needs.
func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(deliveryv1alpha1.AddToScheme(s))
	return s
}

// startK8sWatcher connects to the Kubernetes API, registers informers for
// Recipe, Preparation, and Serving resources, and broadcasts updated Counts,
// Recipe snapshots, and Preparation snapshots to h on every change event.
//
// If no Kubernetes config is found (e.g. running outside a cluster without a
// kubeconfig) the function logs the situation and returns nil; the hub simply
// stays idle.
func startK8sWatcher(ctx context.Context, logger logr.Logger, h *hub) (*apiDeps, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		logger.Info("No Kubernetes config found, API endpoints will return 503", "error", err)
		return nil, nil //nolint:nilnil
	}

	scheme := newScheme()

	k8sCache, err := cache.New(cfg, cache.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating Kubernetes cache: %w", err)
	}

	writer, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating Kubernetes client: %w", err)
	}

	deps := &apiDeps{
		reader:    k8sCache,
		writer:    writer,
		ociClient: oci.NewORASClient(),
		fs:        afero.NewOsFs(),
		logger:    logger,
	}

	recipeInformer, err := k8sCache.GetInformer(ctx, &deliveryv1alpha1.Recipe{})
	if err != nil {
		return nil, fmt.Errorf("getting Recipe informer: %w", err)
	}

	prepInformer, err := k8sCache.GetInformer(ctx, &deliveryv1alpha1.Preparation{})
	if err != nil {
		return nil, fmt.Errorf("getting Preparation informer: %w", err)
	}

	servingInformer, err := k8sCache.GetInformer(ctx, &deliveryv1alpha1.Serving{})
	if err != nil {
		return nil, fmt.Errorf("getting Serving informer: %w", err)
	}

	// refreshAll reads current state from the in-memory informer cache and
	// broadcasts counts, full recipe snapshots, and full preparation snapshots
	// to all SSE subscribers. All reads are local — no network calls.
	refreshAll := func() {
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

		if err := h.publish(eventRecipes, enrichRecipes(recipeList.Items, servingList.Items)); err != nil {
			logger.Error(err, "Failed to publish recipes event")
		}

		if err := h.publish(eventPreparations, enrichPreparations(prepList.Items, servingList.Items)); err != nil {
			logger.Error(err, "Failed to publish preparations event")
		}

		if err := h.publish(eventServings, servingsToDTO(servingList.Items)); err != nil {
			logger.Error(err, "Failed to publish servings event")
		}
	}

	handler := toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ any) { refreshAll() },
		UpdateFunc: func(_, _ any) { refreshAll() },
		DeleteFunc: func(_ any) { refreshAll() },
	}

	if _, err := recipeInformer.AddEventHandler(handler); err != nil {
		return nil, fmt.Errorf("adding Recipe event handler: %w", err)
	}
	if _, err := prepInformer.AddEventHandler(handler); err != nil {
		return nil, fmt.Errorf("adding Preparation event handler: %w", err)
	}
	if _, err := servingInformer.AddEventHandler(handler); err != nil {
		return nil, fmt.Errorf("adding Serving event handler: %w", err)
	}

	// Start the cache in the background; it runs until ctx is cancelled.
	go func() {
		if err := k8sCache.Start(ctx); err != nil {
			logger.Error(err, "Kubernetes cache stopped with error")
		}
	}()

	// After the cache has synced, broadcast the current state immediately so
	// that clients connecting before the first Kubernetes change event already
	// receive the full resource lists.
	go func() {
		if !k8sCache.WaitForCacheSync(ctx) {
			return
		}
		refreshAll()
	}()

	return deps, nil
}
