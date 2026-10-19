package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-logr/logr"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

// retryAfterBusy is how long a busy scanner asks the manager to wait.
const retryAfterBusy = 30

// Engine scans the images of a request.
type Engine interface {
	Scan(ctx context.Context, req Request) (*Response, error)
}

// Handler serves the scanner API: POST ScanPath and GET /healthz.
type Handler struct {
	engine Engine
	logger logr.Logger
	// slots bounds the requests scanned at the same time. A request that
	// finds none free is answered 503, and the manager calls again later.
	slots chan struct{}
}

// NewHandler returns a Handler that scans with engine, at most
// maxConcurrent requests at a time.
func NewHandler(engine Engine, maxConcurrent int, logger logr.Logger) *Handler {
	return &Handler{engine: engine, logger: logger, slots: make(chan struct{}, max(maxConcurrent, 1))}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz" && r.Method == http.MethodGet:
		w.WriteHeader(http.StatusOK)
	case r.URL.Path == ScanPath && r.Method == http.MethodPost:
		h.scan(w, r)
	case r.URL.Path == ScanPath:
		respond(w, http.StatusMethodNotAllowed, errorBody("use POST"))
	default:
		respond(w, http.StatusNotFound, errorBody("not found"))
	}
}

func (h *Handler) scan(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxRequestSize)).Decode(&req); err != nil {
		respond(w, http.StatusBadRequest, errorBody("decoding request: "+err.Error()))
		return
	}
	if err := validate(req); err != nil {
		respond(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}

	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterBusy))
		respond(w, http.StatusServiceUnavailable, errorBody("busy scanning other images"))
		return
	}

	h.logger.Info("Scanning images", "images", len(req.Images), "database", req.Database.Ref)
	start := time.Now()
	resp, err := h.engine.Scan(logr.NewContext(r.Context(), h.logger), req)
	if err != nil {
		h.logger.Error(err, "Failed to scan images")
		status := http.StatusInternalServerError
		if errors.Is(err, errBadDatabase) {
			status = http.StatusUnprocessableEntity
		}
		respond(w, status, errorBody(err.Error()))
		return
	}
	h.logger.Info("Scanned images", "images", len(req.Images), "seconds", time.Since(start).Seconds())
	respond(w, http.StatusOK, resp)
}

// validate checks that req names a database by digest and images by digest.
func validate(req Request) error {
	if err := requireDigest(req.Database.Ref); err != nil {
		return fmt.Errorf("database: %w", err)
	}
	if req.JavaDatabase != nil {
		if err := requireDigest(req.JavaDatabase.Ref); err != nil {
			return fmt.Errorf("java database: %w", err)
		}
	}
	if len(req.Images) == 0 {
		return errors.New("no images to scan")
	}
	for _, img := range req.Images {
		if err := requireDigest(img.Ref); err != nil {
			return fmt.Errorf("image: %w", err)
		}
	}
	return nil
}

// requireDigest checks that ref is an OCI reference with a digest.
func requireDigest(ref string) error {
	parsed, err := oci.Parse(ref)
	if err != nil {
		return err
	}
	if parsed.Digest == "" {
		return fmt.Errorf("%q has no digest", ref)
	}
	return nil
}

// errorBody returns the body of an error response.
func errorBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}

// respond writes body as JSON with status.
func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
