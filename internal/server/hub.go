package server

import (
	"bytes"
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
// types on a single connection. The last known value per event type is
// replayed to newly connecting clients so they receive current state
// immediately, without waiting for the next Kubernetes change.
type hub struct {
	mu          sync.Mutex
	types       []string // event types in first-publish order
	last        map[string]Event
	subscribers map[*subscriber]struct{}
}

func newHub() *hub {
	return &hub{
		last:        make(map[string]Event),
		subscribers: make(map[*subscriber]struct{}),
	}
}

// subscriber buffers the newest pending event per type. A slow client skips
// intermediate snapshots of a type but always ends on its current value, and
// a fast publisher never blocks on it.
type subscriber struct {
	mu      sync.Mutex
	types   []string // pending event types in publish order
	pending map[string]Event
	notify  chan struct{}
}

// offer queues ev, replacing a pending event of the same type, and signals
// the writer.
func (s *subscriber) offer(ev Event) {
	s.mu.Lock()
	if _, ok := s.pending[ev.Type]; !ok {
		s.types = append(s.types, ev.Type)
	}
	s.pending[ev.Type] = ev
	s.mu.Unlock()

	select {
	case s.notify <- struct{}{}:
	default:
		// A signal is already pending; the writer picks this event up with it.
	}
}

// ready is signalled when events are pending.
func (s *subscriber) ready() <-chan struct{} {
	return s.notify
}

// drain returns the pending events in publish order and clears them.
func (s *subscriber) drain() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.types))
	for i, t := range s.types {
		out[i] = s.pending[t]
	}
	s.types = s.types[:0]
	clear(s.pending)
	return out
}

// subscribe registers a new subscriber with all last-known events pending, so
// the client receives current state without waiting for the next change.
func (h *hub) subscribe() *subscriber {
	s := &subscriber{
		pending: make(map[string]Event),
		notify:  make(chan struct{}, 1),
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range h.types {
		s.offer(h.last[t])
	}
	h.subscribers[s] = struct{}{}
	return s
}

// unsubscribe removes a subscriber.
func (h *hub) unsubscribe(s *subscriber) {
	h.mu.Lock()
	delete(h.subscribers, s)
	h.mu.Unlock()
}

// publish marshals payload as an event of the given type, stores it as the
// latest value for that type, and offers it to all current subscribers. A
// payload equal to the latest one is not sent again. Returns an error only if
// marshalling fails.
func (h *hub) publish(eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling event %q: %w", eventType, err)
	}
	ev := Event{Type: eventType, Data: data}

	h.mu.Lock()
	defer h.mu.Unlock()
	prev, seen := h.last[eventType]
	if seen && bytes.Equal(prev.Data, data) {
		return nil
	}
	if !seen {
		h.types = append(h.types, eventType)
	}
	h.last[eventType] = ev
	for s := range h.subscribers {
		s.offer(ev)
	}
	return nil
}
