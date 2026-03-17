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

	// Order CRUD
	mux.HandleFunc("GET /api/v1/orders", handleListOrders(deps))
	mux.HandleFunc("POST /api/v1/orders", handleCreateOrder(deps))
	mux.HandleFunc("GET /api/v1/orders/{namespace}/{name}", handleGetOrder(deps))
	mux.HandleFunc("PUT /api/v1/orders/{namespace}/{name}", handleUpdateOrder(deps))
	mux.HandleFunc("DELETE /api/v1/orders/{namespace}/{name}", handleDeleteOrder(deps))

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
