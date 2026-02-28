package server

import "embed"

// staticFiles holds the compiled React app, embedded at build time.
//
// The web/dist directory is populated by:
//   - Dockerfile: automatically via the ui-builder stage
//   - Local development: run `make ui-build` before `make build-server`
//
//go:embed all:web/dist
var staticFiles embed.FS
