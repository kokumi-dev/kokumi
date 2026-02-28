package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/kokumi-dev/kokumi/internal/version"
)

// InfoResponse is the response body for GET /api/v1/info.
type InfoResponse struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// HandleInfo handles GET /api/v1/info.
func handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(InfoResponse{
		Name:    "kokumi",
		Version: version.Version,
	}); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

// handleEventsStream streams all SSE event types on a single connection.
// Each event is written in the standard SSE format:
//
//	event: <type>
//	data: <json>
//	(blank line)
//
// The browser EventSource API will automatically reconnect if the connection
// drops, and the hub replays the latest value of each event type on reconnect.
func handleEventsStream(h *hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		ch := h.subscribe()
		defer h.unsubscribe(ch)

		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data); err != nil {
					return
				}
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("OK"))
}

func handleReadyz(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("OK"))
}
