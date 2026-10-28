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
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/spf13/afero"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Counts holds the current resource count for each CRD type.
type Counts struct {
	Orders       int `json:"orders"`
	Preparations int `json:"preparations"`
	Servings     int `json:"servings"`
	Products     int `json:"products"`
	Registries   int `json:"registries"`
	Repositories int `json:"repositories"`
}

// SSE event type names.
const (
	eventCounts       = "counts"
	eventOrders       = "orders"
	eventPreparations = "preparations"
	eventServings     = "servings"
	eventProducts     = "products"
	eventRegistries   = "registries"
	eventRepositories = "repositories"
	eventEnvironments = "environments"
	// eventHeartbeat carries no data; it only shows the stream is alive.
	eventHeartbeat = "heartbeat"
)

// sseHeartbeatInterval is how often an SSE stream sends a heartbeat. It stays
// well below the 60 s idle timeout of common proxies such as nginx.
const sseHeartbeatInterval = 15 * time.Second

// refreshInterval bounds how often resource snapshots are rebuilt. A burst of
// informer events within it produces a single snapshot.
const refreshInterval = 100 * time.Millisecond

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
	cfg, err := config.GetConfig()
	if err != nil {
		logger.Info("No Kubernetes config found, API endpoints will return 503", "error", err)
		return nil, nil //nolint:nilnil
	}

	scheme := newScheme()
	installNamespace := namespace.Current(getenv)

	k8sCache, err := cache.New(cfg, cache.Options{
		Scheme: scheme,
		// Restrict Secret and ServiceAccount watches to the server namespace so
		// the namespaced RBAC Role suffices.
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Secret{}: {
				Namespaces: map[string]cache.Config{
					installNamespace: {},
				},
			},
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
		registries:     registry.NewClients(artifact.DefaultRegistryHost),
		store:          artifact.NewStore(oci.NewClient(oci.Options{}), afero.NewOsFs(), "/tmp/kokumi-pull-cache"),
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
	deps.kitchen = func() (*deliveryv1alpha1.Kitchen, error) {
		kitchen := &deliveryv1alpha1.Kitchen{}
		err := k8sCache.Get(ctx, client.ObjectKey{Namespace: installNamespace, Name: deliveryv1alpha1.DefaultKitchenName}, kitchen)
		return kitchen, err
	}
	orderInformer := informers.order
	prepInformer := informers.prep
	servingInformer := informers.serving
	productInformer := informers.product
	registryInformer := informers.registry
	repositoryInformer := informers.repository
	environmentInformer := informers.environment
	kitchenInformer := informers.kitchen
	secretInformer := informers.secret
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

		productList := &deliveryv1alpha1.ProductList{}
		if err := k8sCache.List(ctx, productList); err != nil {
			logger.Error(err, "Failed to list Products from cache")
			return
		}

		registryList := &deliveryv1alpha1.RegistryList{}
		if err := k8sCache.List(ctx, registryList); err != nil {
			logger.Error(err, "Failed to list Registries from cache")
			return
		}

		repositoryList := &deliveryv1alpha1.RepositoryList{}
		if err := k8sCache.List(ctx, repositoryList); err != nil {
			logger.Error(err, "Failed to list Repositories from cache")
			return
		}

		environmentList := &deliveryv1alpha1.EnvironmentList{}
		if err := k8sCache.List(ctx, environmentList); err != nil {
			logger.Error(err, "Failed to list Environments from cache")
			return
		}

		// Lists are published before the lists that refer to them: Orders name
		// their Product and active Preparation; Repositories, Products and
		// Orders name Registries. A client that applies events in arrival
		// order then never shows a reference to an item it has not received
		// yet.
		snapshots := []struct {
			eventType string
			payload   any
		}{
			{eventRegistries, registriesToDTO(registryList.Items)},
			{eventRepositories, repositoriesToDTO(repositoryList.Items)},
			{eventEnvironments, environmentsToDTO(environmentList.Items, installNamespace)},
			{eventProducts, productsToDTO(productList.Items)},
			{eventPreparations, enrichPreparations(prepList.Items, servingList.Items)},
			{eventServings, servingsToDTO(servingList.Items)},
			{eventOrders, enrichOrders(orderList.Items, servingList.Items, productList.Items)},
			{eventCounts, Counts{
				Orders:       len(orderList.Items),
				Preparations: len(prepList.Items),
				Servings:     len(servingList.Items),
				Products:     len(productList.Items),
				Registries:   len(registryList.Items),
				Repositories: len(repositoryList.Items),
			}},
		}
		for _, s := range snapshots {
			if err := h.publish(s.eventType, s.payload); err != nil {
				logger.Error(err, "Failed to publish SSE event", "type", s.eventType)
			}
		}
	}

	// Informer handlers only schedule a refresh; the snapshots are rebuilt
	// sequentially, at most once per refreshInterval.
	scheduleRefresh := coalesce(ctx, refreshInterval, refreshAll)

	// Kitchen changes reload the authenticator (plus SSE refresh). Auth Secrets only
	// affect authentication, so they get a dedicated handler that reloads without a
	// full SSE broadcast; filter to the resolved admin/OIDC/token Secret names to skip unrelated Secrets.
	isAuthSecret := func(obj any) bool {
		o, ok := obj.(client.Object)
		if !ok || o.GetNamespace() != installNamespace {
			return false
		}
		name := o.GetName()
		return name == deps.authMgr.secretName() || name == deps.authMgr.oidcSecretName() || name == deps.authMgr.tokenSecretName()
	}
	kitchenHandler := toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ any) { scheduleRefresh(); deps.authMgr.refresh(ctx) },
		UpdateFunc: func(_, _ any) { scheduleRefresh(); deps.authMgr.refresh(ctx) },
		DeleteFunc: func(_ any) { scheduleRefresh(); deps.authMgr.refresh(ctx) },
	}
	secretHandler := newAuthSecretHandler(deps.authMgr, ctx, isAuthSecret)

	if err := registerWatchers(
		orderInformer, prepInformer, servingInformer, productInformer, registryInformer, repositoryInformer,
		environmentInformer, kitchenInformer, secretInformer, saInformer,
		scheduleRefresh, kitchenHandler, secretHandler,
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
		scheduleRefresh()
	}()

	return deps, nil
}

// coalesce returns a non-blocking trigger for fn. fn runs in a single
// goroutine, interval after the first trigger of a burst. Triggers while it
// waits are covered by that run; triggers while it runs schedule exactly one
// more run, so the last trigger is always followed by a complete run.
func coalesce(ctx context.Context, interval time.Duration, fn func()) func() {
	pending := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-pending:
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
			// This run covers every trigger up to now; only triggers that
			// arrive while fn runs schedule another one.
			select {
			case <-pending:
			default:
			}
			fn()
		}
	}()
	return func() {
		select {
		case pending <- struct{}{}:
		default:
			// A run is already scheduled.
		}
	}
}

// newAuthSecretHandler builds the Secret event handler that reloads auth
// without a full SSE broadcast when an auth-relevant Secret changes.
func newAuthSecretHandler(mgr *authManager, ctx context.Context, isAuthSecret func(any) bool) toolscache.ResourceEventHandlerFuncs {
	return toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if isAuthSecret(obj) {
				mgr.refresh(ctx)
			}
		},
		UpdateFunc: func(_, newObj any) {
			if isAuthSecret(newObj) {
				mgr.refresh(ctx)
			}
		},
		DeleteFunc: func(obj any) {
			if isAuthSecret(obj) {
				mgr.refresh(ctx)
			}
		},
	}
}

// registerWatchers wires the SSE-refresh handler onto resource informers and the
// auth handlers onto Kitchen/Secret informers (split out to keep startK8sWatcher small).
func registerWatchers(
	order, prep, serving, product, registryInformer, repositoryInformer, environment, kitchen, secret, sa cache.Informer,
	scheduleRefresh func(),
	kitchenHandler, secretHandler toolscache.ResourceEventHandlerFuncs,
) error {
	sseHandler := toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ any) { scheduleRefresh() },
		UpdateFunc: func(_, _ any) { scheduleRefresh() },
		DeleteFunc: func(_ any) { scheduleRefresh() },
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
		{product, sseHandler, "Product"},
		{registryInformer, sseHandler, "Registry"},
		{repositoryInformer, sseHandler, "Repository"},
		{environment, sseHandler, "Environment"},
		{kitchen, kitchenHandler, "Kitchen"},
		{secret, secretHandler, "Secret"},
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
	order       cache.Informer
	prep        cache.Informer
	serving     cache.Informer
	product     cache.Informer
	registry    cache.Informer
	repository  cache.Informer
	environment cache.Informer
	kitchen     cache.Informer
	secret      cache.Informer
	sa          cache.Informer
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
	if out.product, err = c.GetInformer(ctx, &deliveryv1alpha1.Product{}); err != nil {
		return out, fmt.Errorf("getting Product informer: %w", err)
	}
	if out.registry, err = c.GetInformer(ctx, &deliveryv1alpha1.Registry{}); err != nil {
		return out, fmt.Errorf("getting Registry informer: %w", err)
	}
	if out.repository, err = c.GetInformer(ctx, &deliveryv1alpha1.Repository{}); err != nil {
		return out, fmt.Errorf("getting Repository informer: %w", err)
	}
	if out.environment, err = c.GetInformer(ctx, &deliveryv1alpha1.Environment{}); err != nil {
		return out, fmt.Errorf("getting Environment informer: %w", err)
	}
	if out.kitchen, err = c.GetInformer(ctx, &deliveryv1alpha1.Kitchen{}); err != nil {
		return out, fmt.Errorf("getting Kitchen informer: %w", err)
	}
	if out.secret, err = c.GetInformer(ctx, &corev1.Secret{}); err != nil {
		return out, fmt.Errorf("getting Secret informer: %w", err)
	}
	if out.sa, err = c.GetInformer(ctx, &corev1.ServiceAccount{}); err != nil {
		return out, fmt.Errorf("getting ServiceAccount informer: %w", err)
	}
	return out, nil
}
