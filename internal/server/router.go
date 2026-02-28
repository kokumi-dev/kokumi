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

	// Recipe CRUD
	mux.HandleFunc("GET /api/v1/recipes", handleListRecipes(deps))
	mux.HandleFunc("POST /api/v1/recipes", handleCreateRecipe(deps))
	mux.HandleFunc("GET /api/v1/recipes/{namespace}/{name}", handleGetRecipe(deps))
	mux.HandleFunc("PUT /api/v1/recipes/{namespace}/{name}", handleUpdateRecipe(deps))
	mux.HandleFunc("DELETE /api/v1/recipes/{namespace}/{name}", handleDeleteRecipe(deps))

	// Preparations scoped to a Recipe
	mux.HandleFunc("GET /api/v1/recipes/{namespace}/{name}/preparations", handleListPreparations(deps))

	// Promote / rollback a Preparation
	mux.HandleFunc("POST /api/v1/recipes/{namespace}/{name}/promote", handlePromote(deps))

	// Preparation manifest (rendered YAML from OCI)
	mux.HandleFunc("GET /api/v1/preparations/{namespace}/{name}/manifest", handleGetPreparationManifest(deps))

	distFS, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		panic("embedded web/dist not found: " + err.Error())
	}
	mux.Handle("/", http.FileServer(http.FS(distFS)))
}
