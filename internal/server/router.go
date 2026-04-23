package server

import (
	"io/fs"
	"net/http"
)

func addRoutes(
	mux *http.ServeMux,
	h *hub,
	deps *apiDeps,
) {
	mux.HandleFunc("GET /api/v1/info", handleInfo)
	mux.HandleFunc("GET /api/v1/events", handleEventsStream(h))
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", handleReadyz)

	mux.HandleFunc("GET /api/v1/registry/default", handleGetDefaultRegistry())
	mux.HandleFunc("GET /api/v1/registry/tags", handleListRegistryTags(deps))

	// Order CRUD
	mux.HandleFunc("GET /api/v1/orders", handleListOrders(deps))
	mux.HandleFunc("POST /api/v1/orders", handleCreateOrder(deps))
	mux.HandleFunc("POST /api/v1/orders/preview", handlePreviewOrder(deps))
	mux.HandleFunc("GET /api/v1/orders/{namespace}/{name}", handleGetOrder(deps))
	mux.HandleFunc("PUT /api/v1/orders/{namespace}/{name}", handleUpdateOrder(deps))
	mux.HandleFunc("PUT /api/v1/orders/{namespace}/{name}/edits", handleUpdateOrderEdits(deps))
	mux.HandleFunc("DELETE /api/v1/orders/{namespace}/{name}", handleDeleteOrder(deps))

	// Menu CRUD (cluster-scoped)
	mux.HandleFunc("GET /api/v1/menus", handleListMenus(deps))
	mux.HandleFunc("POST /api/v1/menus", handleCreateMenu(deps))
	mux.HandleFunc("GET /api/v1/menus/{name}", handleGetMenu(deps))
	mux.HandleFunc("PUT /api/v1/menus/{name}", handleUpdateMenu(deps))
	mux.HandleFunc("DELETE /api/v1/menus/{name}", handleDeleteMenu(deps))

	// Preparations scoped to a Order
	mux.HandleFunc("GET /api/v1/orders/{namespace}/{name}/preparations", handleListPreparations(deps))

	// Promote / rollback a Preparation
	mux.HandleFunc("POST /api/v1/orders/{namespace}/{name}/promote", handlePromote(deps))

	// Preparation manifest (rendered YAML from OCI)
	mux.HandleFunc("GET /api/v1/preparations/{namespace}/{name}/manifest", handleGetPreparationManifest(deps))

	distFS, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		panic("embedded web/dist not found: " + err.Error())
	}
	mux.Handle("/", http.FileServer(http.FS(distFS)))
}
