package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/kokumi-dev/kokumi/internal/version"
)

// InfoResponse is the response body for GET /api/v1/info.
type InfoResponse struct {
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	AuthProviders []string `json:"authProviders,omitempty"`
}

// handleInfo handles GET /api/v1/info; authProviders tells the UI how to render
// login. An empty provider list means the UI shows the login page with a "no
// login method configured" notice. Without a provider there is no identity,
// hence no mapped ServiceAccount, hence no data. A non-empty list both selects
// the login methods and signals that login is required.
func handleInfo(authMgr *authManager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var providers []string
		if authMgr != nil {
			providers = authMgr.providers()
		}
		if err := json.NewEncoder(w).Encode(InfoResponse{
			Name:          "kokumi",
			Version:       version.Version,
			AuthProviders: providers,
		}); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
		}
	}
}

// handleEventsStream streams all SSE event types on one connection. Events use
// the standard SSE format (event:/data:/blank line); EventSource auto-reconnects and the
// hub replays the latest value of each type on reconnect.
//
// The stream is authorization-aware: the hub's snapshots are produced from the
// server's own informer cache, so before any event is written the handler
// checks (via SelfSubjectAccessReview as the user's mapped ServiceAccounts)
// whether the user may list the resource the event describes. Users without
// list permission for a resource receive no events for it.
func handleEventsStream(h *hub, deps *apiDeps) http.HandlerFunc {
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

		// Resolve the user's readable resources; nil deps or an unresolvable
		// identity yields no permissions, so nothing is streamed.
		readable := map[string]bool{}
		if deps != nil {
			if uc, err := deps.resolveUserClient(r); err == nil {
				for _, res := range []string{"orders", "preparations", "servings", "menus", "pantries"} {
					for _, sa := range uc.sas {
						if allowed, err := deps.impersonator.authorizedFor(r.Context(), sa.Name, "list", res, ""); err == nil && allowed {
							readable[res] = true
							break
						}
					}
				}
			}
		}
		// The counts event aggregates all resources; only users who may list
		// everything receive it.
		allReadable := len(readable) == 5

		// eventResource maps event type -> resource it discloses.
		eventResource := map[string]string{
			eventOrders:       "orders",
			eventPreparations: "preparations",
			eventServings:     "servings",
			eventMenus:        "menus",
			eventPantries:     "pantries",
		}

		ch := h.subscribe()
		defer h.unsubscribe(ch)

		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				res, isResourceEvent := eventResource[ev.Type]
				if isResourceEvent && !readable[res] {
					continue
				}
				if ev.Type == eventCounts && !allReadable {
					continue
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
