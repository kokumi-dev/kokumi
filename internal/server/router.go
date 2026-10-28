package server

import (
	"io/fs"
	"net/http"
)

func addRoutes(
	mux *http.ServeMux,
	h *hub,
	deps *apiDeps,
	authMgr *authManager,
	installNamespace string,
) {
	mux.HandleFunc("GET /api/v1/info", handleInfo(authMgr))
	mux.HandleFunc("GET /api/v1/events", handleEventsStream(h, deps, sseHeartbeatInterval))
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", handleReadyz)

	if authMgr != nil {
		mux.HandleFunc("POST /api/v1/auth/login", handleLogin(authMgr))
		mux.HandleFunc("POST /api/v1/auth/refresh", handleRefresh(authMgr))
		mux.HandleFunc("POST /api/v1/auth/logout", handleLogout(authMgr))
		mux.HandleFunc("GET /api/v1/auth/oidc/start", handleOIDCStart(authMgr))
		mux.HandleFunc("GET /api/v1/auth/oidc/callback", handleOIDCCallback(authMgr))
	}

	mux.HandleFunc("GET /api/v1/oci/default", handleGetDefaultRegistry())
	mux.HandleFunc("GET /api/v1/oci/tags", handleListRegistryTags(deps))
	mux.HandleFunc("GET /api/v1/oci/artifact", handleGetRegistryArtifact(deps))
	mux.HandleFunc("GET /api/v1/oci/chart-info", handleGetChartInfo(deps))

	// Settings (singleton Kitchen/default)
	mux.HandleFunc("GET /api/v1/settings", handleGetSettings(deps, installNamespace))
	mux.HandleFunc("PUT /api/v1/settings", handlePutSettings(deps, installNamespace))
	mux.HandleFunc("GET /api/v1/whoami", handleWhoami(deps, installNamespace))
	mux.HandleFunc("GET /api/v1/namespaces/{namespace}/capabilities", handleNamespaceCapabilities(deps))
	mux.HandleFunc("GET /api/v1/signing", handleGetSigning(deps))

	// Order CRUD
	mux.HandleFunc("GET /api/v1/orders", handleListOrders(deps))
	mux.HandleFunc("POST /api/v1/orders", handleCreateOrder(deps))
	mux.HandleFunc("POST /api/v1/orders/preview", handlePreviewOrder(deps))
	mux.HandleFunc("GET /api/v1/orders/{namespace}/{name}", handleGetOrder(deps))
	mux.HandleFunc("PUT /api/v1/orders/{namespace}/{name}", handleUpdateOrder(deps))
	mux.HandleFunc("PUT /api/v1/orders/{namespace}/{name}/edits", handleUpdateOrderEdits(deps))
	mux.HandleFunc("DELETE /api/v1/orders/{namespace}/{name}", handleDeleteOrder(deps))

	// Product CRUD
	mux.HandleFunc("GET /api/v1/products", handleListProducts(deps))
	mux.HandleFunc("POST /api/v1/products", handleCreateProduct(deps))
	mux.HandleFunc("POST /api/v1/products/preview", handlePreviewProduct(deps))
	mux.HandleFunc("GET /api/v1/products/{namespace}/{name}", handleGetProduct(deps))
	mux.HandleFunc("GET /api/v1/products/{namespace}/{name}/source/artifact", handleGetProductSourceArtifact(deps))
	mux.HandleFunc("GET /api/v1/products/{namespace}/{name}/vulnerabilities", handleGetProductVulnerabilities(deps))
	mux.HandleFunc("GET /api/v1/products/{namespace}/{name}/sbom", handleGetProductSBOM(deps))
	mux.HandleFunc("PUT /api/v1/products/{namespace}/{name}", handleUpdateProduct(deps))
	mux.HandleFunc("DELETE /api/v1/products/{namespace}/{name}", handleDeleteProduct(deps))

	// Registry CRUD
	mux.HandleFunc("GET /api/v1/registries", handleListRegistries(deps))
	mux.HandleFunc("POST /api/v1/registries", handleCreateRegistry(deps))
	mux.HandleFunc("GET /api/v1/registries/{namespace}/{name}", handleGetRegistry(deps))
	mux.HandleFunc("PUT /api/v1/registries/{namespace}/{name}", handleUpdateRegistry(deps))
	mux.HandleFunc("DELETE /api/v1/registries/{namespace}/{name}", handleDeleteRegistry(deps))

	// Repository CRUD
	mux.HandleFunc("GET /api/v1/repositories", handleListRepositories(deps))
	mux.HandleFunc("POST /api/v1/repositories", handleCreateRepository(deps))
	mux.HandleFunc("GET /api/v1/repositories/{namespace}/{name}", handleGetRepository(deps))
	mux.HandleFunc("GET /api/v1/repositories/{namespace}/{name}/artifact", handleGetRepositoryArtifact(deps))
	mux.HandleFunc("PUT /api/v1/repositories/{namespace}/{name}", handleUpdateRepository(deps))
	mux.HandleFunc("DELETE /api/v1/repositories/{namespace}/{name}", handleDeleteRepository(deps))

	// Preparations scoped to a Order
	mux.HandleFunc("GET /api/v1/orders/{namespace}/{name}/preparations", handleListPreparations(deps))
	mux.HandleFunc("GET /api/v1/orders/{namespace}/{name}/approvals", handleListOrderApprovals(deps))

	// Approvals (votes) on a Preparation
	mux.HandleFunc("GET /api/v1/preparations/{namespace}/{name}/approvals", handleListPreparationApprovals(deps))
	mux.HandleFunc("POST /api/v1/preparations/{namespace}/{name}/approvals", handleSubmitApproval(deps))

	// Promote / rollback a Preparation
	mux.HandleFunc("POST /api/v1/orders/{namespace}/{name}/promote", handlePromote(deps))
	mux.HandleFunc("GET /api/v1/servings/{namespace}/{name}/targets", handleServingTargets(deps))

	// Preparation artifact: its graph, intent and rendered files from OCI
	mux.HandleFunc("GET /api/v1/preparations/{namespace}/{name}/artifact", handleGetPreparationArtifact(deps))
	mux.HandleFunc("GET /api/v1/preparations/{namespace}/{name}/intent", handleGetPreparationIntent(deps))
	mux.HandleFunc("GET /api/v1/preparations/{namespace}/{name}/manifest", handleGetPreparationManifest(deps))
	mux.HandleFunc("GET /api/v1/preparations/{namespace}/{name}/vulnerabilities", handleGetPreparationVulnerabilities(deps))

	distFS, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		panic("embedded web/dist not found: " + err.Error())
	}
	mux.Handle("/", spaHandler(distFS))
}
