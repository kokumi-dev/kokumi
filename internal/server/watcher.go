package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
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
	"sigs.k8s.io/controller-runtime/pkg/log"
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

// authSecretPollInterval bounds how long rotated admin/OIDC/token credentials
// can go unnoticed, since auth Secrets are polled rather than watched
// (get-only RBAC on named Secrets).
const authSecretPollInterval = 30 * time.Second

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
	h *hub,
	getenv func(string) string,
) (*apiDeps, error) {
	logger := log.FromContext(ctx)
	cfg, err := ctrl.GetConfig()
	if err != nil {
		logger.Info("No Kubernetes config found, API endpoints will return 503", "error", err)
		return nil, nil //nolint:nilnil
	}

	scheme := newScheme()
	installNamespace := namespace.Current(getenv)

	k8sCache, err := cache.New(cfg, cache.Options{
		Scheme: scheme,
		// Restrict the ServiceAccount watch to the server namespace so the
		// namespaced RBAC Role suffices. Auth Secrets are no longer watched:
		// the authManager poller (see startAuthSecretPoller) reads them by name
		// with get-only RBAC on named Secrets.
		ByObject: map[client.Object]cache.ByObject{
			&corev1.ServiceAccount{}: {
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
		ociClient:      oci.NewORASClient(),
		store:          artifact.NewStore(oci.NewORASClient(), afero.NewOsFs(), "/tmp/kokumi-pull-cache"),
		logger:         logger,
		approvalWriter: writer,
	}
	deps.pipeline = artifact.NewPipeline(deps.store)

	// Impersonation layer: all user-facing operations execute as the mapped
	// ServiceAccount so Kubernetes RBAC is the single source of truth.
	deps.impersonator = newImpersonator(cfg, scheme, installNamespace)

	var tokenTTL time.Duration
	if v := strings.TrimSpace(getenv("KOKUMI_TOKEN_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			tokenTTL = d
		}
	}

	// Build the auth manager up front so it is never nil when cache handlers fire.
	deps.authMgr = newAuthManager(ctx, k8sCache, writer, installNamespace, tokenTTL)

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
	saInformer := informers.sa

	// saList reads ServiceAccounts in the install namespace from the informer
	// cache; used per request to resolve identity -> ServiceAccount mappings.
	deps.saList = func() []*corev1.ServiceAccount {
		list := &corev1.ServiceAccountList{}
		if err := k8sCache.List(ctx, list, client.InNamespace(installNamespace)); err != nil {
			logger.Error(err, "Failed to list ServiceAccounts from cache")
			return nil
		}
		out := make([]*corev1.ServiceAccount, 0, len(list.Items))
		for i := range list.Items {
			out = append(out, &list.Items[i])
		}
		return out
	}

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

	// Kitchen changes reload the authenticator (plus SSE refresh). Auth
	// Secrets cannot be watched (get-only RBAC on named Secrets), so the
	// authManager polls them on a short ticker instead.
	kitchenHandler := toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ any) { refreshAll(); deps.authMgr.refresh(ctx) },
		UpdateFunc: func(_, _ any) { refreshAll(); deps.authMgr.refresh(ctx) },
		DeleteFunc: func(_ any) { refreshAll(); deps.authMgr.refresh(ctx) },
	}

	if err := registerWatchers(
		orderInformer, prepInformer, servingInformer, menuInformer, pantryInformer,
		kitchenInformer, saInformer,
		refreshAll, kitchenHandler,
	); err != nil {
		return nil, err
	}

	// Poll the resolved auth Secrets (admin/OIDC/token signing key) so rotated
	// credentials are picked up without a restart; Kitchen events already
	// trigger immediate refreshes via kitchenHandler.
	startAuthSecretPoller(ctx, deps.authMgr)

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

// startAuthSecretPoller re-resolves the auth Secrets on a ticker so rotated
// admin/OIDC/token credentials are honored without a server restart. RBAC for
// this path is get-only on the named Secrets (server-auth-reader Role).
func startAuthSecretPoller(ctx context.Context, mgr *authManager) {
	go func() {
		ticker := time.NewTicker(authSecretPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mgr.refresh(ctx)
			}
		}
	}()
}

// registerWatchers wires the SSE-refresh handler onto resource informers and the
// auth handler onto the Kitchen informer (split out to keep startK8sWatcher small).
func registerWatchers(
	order, prep, serving, menu, pantry, kitchen, sa cache.Informer,
	refreshAll func(),
	kitchenHandler toolscache.ResourceEventHandlerFuncs,
) error {
	sseHandler := toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ any) { refreshAll() },
		UpdateFunc: func(_, _ any) { refreshAll() },
		DeleteFunc: func(_ any) { refreshAll() },
	}
	// ServiceAccount changes can alter identity mappings; no SSE refresh needed.
	noopHandler := toolscache.ResourceEventHandlerFuncs{}
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

		{sa, noopHandler, "ServiceAccount"},
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
	sa      cache.Informer
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
	if out.sa, err = c.GetInformer(ctx, &corev1.ServiceAccount{}); err != nil {
		return out, fmt.Errorf("getting ServiceAccount informer: %w", err)
	}
	return out, nil
}
