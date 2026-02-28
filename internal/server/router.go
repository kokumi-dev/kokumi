package server

import (
	"io/fs"
	"net/http"
)

func addRoutes(
	mux *http.ServeMux,
	h *hub,
) {
	mux.HandleFunc("/api/v1/info", handleInfo)
	mux.HandleFunc("/api/v1/events", handleEventsStream(h))
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/readyz", handleReadyz)

	distFS, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		panic("embedded web/dist not found: " + err.Error())
	}
	mux.Handle("/", http.FileServer(http.FS(distFS)))
}
