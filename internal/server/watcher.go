package server

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/namespace"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/spf13/afero"
	corev1 "k8s.io/api/core/v1"
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
	Orders       int `json:"orders"`
	Preparations int `json:"preparations"`
	Servings     int `json:"servings"`
	Menus        int `json:"menus"`
	Pantries     int `json:"pantries"`
}

const (
	// eventCounts is the SSE event type name for resource count updates.
	eventCounts = "counts"
	// eventOrders is the SSE event type name for full order list snapshots.
	eventOrders = "orders"
	// eventPreparations is the SSE event type name for full preparation list snapshots.
	eventPreparations = "preparations"
	// eventServings is the SSE event type name for full serving list snapshots.
	eventServings = "servings"
	// eventMenus is the SSE event type name for full menu list snapshots.
	eventMenus = "menus"
	// eventPantries is the SSE event type name for full pantry list snapshots.
	eventPantries = "pantries"
)

// newScheme builds a runtime Scheme with the types the server needs.
func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(deliveryv1alpha1.AddToScheme(s))
	return s
}

// startK8sWatcher connects to the Kubernetes API, registers informers for
// Order, Preparation, and Serving resources, and broadcasts updated Counts,
// Order snapshots, and Preparation snapshots to h on every change event.
//
// If no Kubernetes config is found (e.g. running outside a cluster without a
// kubeconfig) the function logs the situation and returns nil; the hub simply
// stays idle.
func startK8sWatcher(
	ctx context.Context,
	logger logr.Logger,
	h *hub,
	getenv func(string) string,
) (*apiDeps, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		logger.Info("No Kubernetes config found, API endpoints will return 503", "error", err)
		return nil, nil //nolint:nilnil
	}

	scheme := newScheme()
	installNamespace := namespace.Current(getenv)

	k8sCache, err := cache.New(cfg, cache.Options{
		Scheme: scheme,
		// Restrict Secret watches to the server's own namespace so that the
		// namespaced RBAC Role (not a ClusterRole) is sufficient.
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Secret{}: {
				Namespaces: map[string]cache.Config{
					installNamespace: {},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating Kubernetes cache: %w", err)
	}

	writer, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating Kubernetes client: %w", err)
	}

	deps := &apiDeps{
		reader:    k8sCache,
		apiReader: writer,
		ociClient: oci.NewORASClient(),
		fs:        afero.NewOsFs(),
		logger:    logger,
	}

	// Construct the auth manager up front so it is never nil when the cache
	// event handlers fire (the cache starts in a background goroutine below).
	deps.authMgr = newAuthManager(ctx, k8sCache, writer, installNamespace, getenv, logger)

	informers, err := getInformers(ctx, k8sCache)
	if err != nil {
		return nil, err
	}
	orderInformer := informers.order
	prepInformer := informers.prep
	servingInformer := informers.serving
	menuInformer := informers.menu
	pantryInformer := informers.pantry
	kitchenInformer := informers.kitchen
	secretInformer := informers.secret

	// refreshAll reads current state from the in-memory informer cache and
	// broadcasts counts, full order snapshots, and full preparation snapshots
	// to all SSE subscribers. All reads are local — no network calls.
	refreshAll := func() {
		orderList := &deliveryv1alpha1.OrderList{}
		if err := k8sCache.List(ctx, orderList); err != nil {
			logger.Error(err, "Failed to list Orders from cache")
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

		menuList := &deliveryv1alpha1.MenuList{}
		if err := k8sCache.List(ctx, menuList); err != nil {
			logger.Error(err, "Failed to list Menus from cache")
			return
		}

		pantryList := &deliveryv1alpha1.PantryList{}
		if err := k8sCache.List(ctx, pantryList); err != nil {
			logger.Error(err, "Failed to list Pantries from cache")
			return
		}

		if err := h.publish(eventCounts, Counts{
			Orders:       len(orderList.Items),
			Preparations: len(prepList.Items),
			Servings:     len(servingList.Items),
			Menus:        len(menuList.Items),
			Pantries:     len(pantryList.Items),
		}); err != nil {
			logger.Error(err, "Failed to publish counts event")
		}

		if err := h.publish(eventOrders, enrichOrders(orderList.Items, servingList.Items)); err != nil {
			logger.Error(err, "Failed to publish orders event")
		}

		if err := h.publish(eventPreparations, enrichPreparations(prepList.Items, servingList.Items)); err != nil {
			logger.Error(err, "Failed to publish preparations event")
		}

		if err := h.publish(eventServings, servingsToDTO(servingList.Items)); err != nil {
			logger.Error(err, "Failed to publish servings event")
		}

		if err := h.publish(eventMenus, menusToDTO(menuList.Items)); err != nil {
			logger.Error(err, "Failed to publish menus event")
		}

		if err := h.publish(eventPantries, pantriesFromList(*pantryList)); err != nil {
			logger.Error(err, "Failed to publish pantries event")
		}
	}

	handler := toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ any) { refreshAll() },
		UpdateFunc: func(_, _ any) { refreshAll() },
		DeleteFunc: func(_ any) { refreshAll() },
	}

	if _, err := orderInformer.AddEventHandler(handler); err != nil {
		return nil, fmt.Errorf("adding Order event handler: %w", err)
	}
	if _, err := prepInformer.AddEventHandler(handler); err != nil {
		return nil, fmt.Errorf("adding Preparation event handler: %w", err)
	}
	if _, err := servingInformer.AddEventHandler(handler); err != nil {
		return nil, fmt.Errorf("adding Serving event handler: %w", err)
	}
	if _, err := menuInformer.AddEventHandler(handler); err != nil {
		return nil, fmt.Errorf("adding Menu event handler: %w", err)
	}
	if _, err := pantryInformer.AddEventHandler(handler); err != nil {
		return nil, fmt.Errorf("adding Pantry event handler: %w", err)
	}
	if _, err := kitchenInformer.AddEventHandler(handler); err != nil {
		return nil, fmt.Errorf("adding Kitchen event handler: %w", err)
	}

	// The auth Secret only affects authentication, so it gets its own handler
	// that reloads the authenticator without triggering a full SSE broadcast.
	// Filter to the resolved auth Secret name (from the Kitchen's
	// spec.adminUser.secretRef, defaulting to kokumi-server-auth) to avoid
	// reloading on unrelated Secret changes in the namespace.
	isAuthSecret := func(obj any) bool {
		o, ok := obj.(client.Object)
		return ok && o.GetNamespace() == installNamespace && o.GetName() == deps.authMgr.secretName()
	}
	secretHandler := toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if isAuthSecret(obj) {
				deps.authMgr.refresh(ctx)
			}
		},
		UpdateFunc: func(_, newObj any) {
			if isAuthSecret(newObj) {
				deps.authMgr.refresh(ctx)
			}
		},
		DeleteFunc: func(obj any) {
			if isAuthSecret(obj) {
				deps.authMgr.refresh(ctx)
			}
		},
	}
	if _, err := secretInformer.AddEventHandler(secretHandler); err != nil {
		return nil, fmt.Errorf("adding Secret event handler: %w", err)
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

// informers bundles the informers the server watches.
type informers struct {
	order   cache.Informer
	prep    cache.Informer
	serving cache.Informer
	menu    cache.Informer
	pantry  cache.Informer
	kitchen cache.Informer
	secret  cache.Informer
}

// getInformers registers and returns the informers the server needs. It is
// kept separate from startK8sWatcher to keep that function's cyclomatic
// complexity within lint limits.
func getInformers(ctx context.Context, c cache.Cache) (informers, error) {
	var out informers
	var err error
	if out.order, err = c.GetInformer(ctx, &deliveryv1alpha1.Order{}); err != nil {
		return out, fmt.Errorf("getting Order informer: %w", err)
	}
	if out.prep, err = c.GetInformer(ctx, &deliveryv1alpha1.Preparation{}); err != nil {
		return out, fmt.Errorf("getting Preparation informer: %w", err)
	}
	if out.serving, err = c.GetInformer(ctx, &deliveryv1alpha1.Serving{}); err != nil {
		return out, fmt.Errorf("getting Serving informer: %w", err)
	}
	if out.menu, err = c.GetInformer(ctx, &deliveryv1alpha1.Menu{}); err != nil {
		return out, fmt.Errorf("getting Menu informer: %w", err)
	}
	if out.pantry, err = c.GetInformer(ctx, &deliveryv1alpha1.Pantry{}); err != nil {
		return out, fmt.Errorf("getting Pantry informer: %w", err)
	}
	if out.kitchen, err = c.GetInformer(ctx, &deliveryv1alpha1.Kitchen{}); err != nil {
		return out, fmt.Errorf("getting Kitchen informer: %w", err)
	}
	if out.secret, err = c.GetInformer(ctx, &corev1.Secret{}); err != nil {
		return out, fmt.Errorf("getting Secret informer: %w", err)
	}
	return out, nil
}
