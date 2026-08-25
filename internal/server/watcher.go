package server

import (
	"context"
	"fmt"
	"strings"
	"time"

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

// SSE event type names.
const (
	eventCounts       = "counts"
	eventOrders       = "orders"
	eventPreparations = "preparations"
	eventServings     = "servings"
	eventMenus        = "menus"
	eventPantries     = "pantries"
)

// newScheme builds a runtime Scheme with the types the server needs.
func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(deliveryv1alpha1.AddToScheme(s))
	return s
}

// startK8sWatcher connects to the Kubernetes API, registers informers, and
// broadcasts resource snapshots to h on every change. With no kubeconfig it
// logs and returns nil so the hub stays idle.
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
		// Restrict Secret watches to the server namespace so the namespaced RBAC Role suffices.
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

	var tokenTTL time.Duration
	if v := strings.TrimSpace(getenv("KOKUMI_TOKEN_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			tokenTTL = d
		}
	}

	// Build the auth manager up front so it is never nil when cache handlers fire.
	deps.authMgr = newAuthManager(ctx, k8sCache, writer, installNamespace, tokenTTL, logger)

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

	// refreshAll reads from the local informer cache and broadcasts snapshots to all SSE subscribers.
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

	// Kitchen changes reload the authenticator (plus SSE refresh). Auth Secrets only
	// affect authentication, so they get a dedicated handler that reloads without a
	// full SSE broadcast; filter to the resolved admin/OIDC Secret names to skip unrelated Secrets.
	isAuthSecret := func(obj any) bool {
		o, ok := obj.(client.Object)
		if !ok || o.GetNamespace() != installNamespace {
			return false
		}
		name := o.GetName()
		return name == deps.authMgr.secretName() || name == deps.authMgr.oidcSecretName()
	}
	kitchenHandler := toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ any) { refreshAll(); deps.authMgr.refresh(ctx) },
		UpdateFunc: func(_, _ any) { refreshAll(); deps.authMgr.refresh(ctx) },
		DeleteFunc: func(_ any) { refreshAll(); deps.authMgr.refresh(ctx) },
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

	if err := registerWatchers(
		orderInformer, prepInformer, servingInformer, menuInformer, pantryInformer,
		kitchenInformer, secretInformer,
		refreshAll, kitchenHandler, secretHandler,
	); err != nil {
		return nil, err
	}

	// Start the cache in the background until ctx is cancelled.
	go func() {
		if err := k8sCache.Start(ctx); err != nil {
			logger.Error(err, "Kubernetes cache stopped with error")
		}
	}()

	// After sync, broadcast current state so early clients get the full lists.
	go func() {
		if !k8sCache.WaitForCacheSync(ctx) {
			return
		}
		refreshAll()
	}()

	return deps, nil
}

// registerWatchers wires the SSE-refresh handler onto resource informers and the
// auth handlers onto Kitchen/Secret informers (split out to keep startK8sWatcher small).
func registerWatchers(
	order, prep, serving, menu, pantry, kitchen, secret cache.Informer,
	refreshAll func(),
	kitchenHandler, secretHandler toolscache.ResourceEventHandlerFuncs,
) error {
	sseHandler := toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ any) { refreshAll() },
		UpdateFunc: func(_, _ any) { refreshAll() },
		DeleteFunc: func(_ any) { refreshAll() },
	}
	type reg struct {
		informer cache.Informer
		handler  toolscache.ResourceEventHandlerFuncs
		name     string
	}
	for _, r := range []reg{
		{order, sseHandler, "Order"},
		{prep, sseHandler, "Preparation"},
		{serving, sseHandler, "Serving"},
		{menu, sseHandler, "Menu"},
		{pantry, sseHandler, "Pantry"},
		{kitchen, kitchenHandler, "Kitchen"},
		{secret, secretHandler, "Secret"},
	} {
		if _, err := r.informer.AddEventHandler(r.handler); err != nil {
			return fmt.Errorf("adding %s event handler: %w", r.name, err)
		}
	}
	return nil
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

// getInformers registers and returns the informers the server needs (split out to keep startK8sWatcher small).
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
