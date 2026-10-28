package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/kokumi-dev/kokumi/internal/version"
)

// streamedResources are the resources the event stream discloses lists of.
var streamedResources = []string{
	eventOrders, eventPreparations, eventServings, eventProducts, registriesResource, repositoriesResource, environmentsResource,
}

// countedResources are the resources the counts event discloses the number
// of; see Counts.
var countedResources = []string{eventOrders, eventPreparations, eventServings, eventProducts, registriesResource, repositoriesResource}

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
//
// A heartbeat event every heartbeat interval keeps a quiet stream alive
// through proxies that close idle connections, and lets the client tell a
// quiet stream from a dead one.
func handleEventsStream(h *hub, deps *apiDeps, heartbeat time.Duration) http.HandlerFunc {
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
				for _, res := range streamedResources {
					for _, sa := range uc.sas {
						if allowed, err := deps.impersonator.authorizedFor(r.Context(), sa.Name, "list", res, "", ""); err == nil && allowed {
							readable[res] = true
							break
						}
					}
				}
			}
		}
		// The counts event aggregates the counted resources; only users who
		// may list all of them receive it.
		allReadable := !slices.ContainsFunc(countedResources, func(res string) bool { return !readable[res] })

		// eventResource maps event type -> resource it discloses.
		eventResource := map[string]string{
			eventOrders:       "orders",
			eventPreparations: "preparations",
			eventServings:     "servings",
			eventProducts:     "products",
			eventRegistries:   registriesResource,
			eventRepositories: repositoriesResource,
			eventEnvironments: environmentsResource,
		}

		sub := h.subscribe()
		defer h.unsubscribe(sub)

		ticker := time.NewTicker(heartbeat)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if _, err := fmt.Fprintf(w, "event: %s\ndata: {}\n\n", eventHeartbeat); err != nil {
					return
				}
				flusher.Flush()
			case <-sub.ready():
				for _, ev := range sub.drain() {
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
