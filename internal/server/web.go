package server

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticFiles holds the compiled React app, embedded at build time.
//
// The web/dist directory is populated by:
//   - Dockerfile: automatically via the ui-builder stage
//   - Local development: run `make ui-build` before `make build-server`
//
//go:embed all:web/dist
var staticFiles embed.FS

// spaHandler serves the UI bundle and falls back to index.html for
// client-side routes so deep links survive a page reload.
func spaHandler(dist fs.FS) http.Handler {
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(dist, name); err == nil {
			files.ServeHTTP(w, r)
			return
		}
		// Missing assets must 404 instead of returning HTML with a 200.
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, dist, "index.html")
	})
}
