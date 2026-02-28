package server

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Event is a named SSE event with a pre-serialised JSON payload.
// Marshalling once at publish time means no per-subscriber re-serialisation.
type Event struct {
	Type string
	Data json.RawMessage
}

// hub is a multiplexed SSE broadcaster. Every subscriber receives all event
// types on a single channel. The last known value per event type is replayed
// to newly connecting clients so they receive current state immediately,
// without waiting for the next Kubernetes change.
type hub struct {
	mu          sync.Mutex
	last        map[string]Event
	subscribers map[chan Event]struct{}
}

func newHub() *hub {
	return &hub{
		last:        make(map[string]Event),
		subscribers: make(map[chan Event]struct{}),
	}
}

// subscribe registers a new subscriber and returns its receive channel.
// All last-known events are immediately queued so the client receives current
// state without waiting for the next change event.
func (h *hub) subscribe() chan Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Buffer holds the full replay set plus one slot for the first live event.
	ch := make(chan Event, len(h.last)+1)
	for _, ev := range h.last {
		ch <- ev
	}
	h.subscribers[ch] = struct{}{}
	return ch
}

// unsubscribe removes a subscriber and closes its channel.
func (h *hub) unsubscribe(ch chan Event) {
	h.mu.Lock()
	delete(h.subscribers, ch)
	close(ch)
	h.mu.Unlock()
}

// publish marshals payload as an event of the given type, stores it as the
// latest value for that type, and fans it out to all current subscribers.
// Slow subscribers are silently dropped rather than blocking the broadcaster.
// Returns an error only if marshalling fails.
func (h *hub) publish(eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling event %q: %w", eventType, err)
	}
	ev := Event{Type: eventType, Data: data}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.last[eventType] = ev
	for ch := range h.subscribers {
		select {
		case ch <- ev:
		default:
			// drop if the subscriber channel is full
		}
	}
	return nil
}
