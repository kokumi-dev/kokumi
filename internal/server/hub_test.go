package server

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func eventTypes(events []Event) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return out
}

func TestHub_replaysInPublishOrder(t *testing.T) {
	h := newHub()
	for _, typ := range []string{eventCounts, eventOrders, eventPreparations, eventServings, eventProducts, eventPantries} {
		require.NoError(t, h.publish(typ, []string{typ}))
	}

	for range 10 {
		sub := h.subscribe()
		<-sub.ready()
		assert.Equal(t,
			[]string{eventCounts, eventOrders, eventPreparations, eventServings, eventProducts, eventPantries},
			eventTypes(sub.drain()))
		h.unsubscribe(sub)
	}
}

func TestHub_skipsIdenticalPayloads(t *testing.T) {
	h := newHub()
	require.NoError(t, h.publish(eventOrders, []string{"a"}))
	sub := h.subscribe()
	<-sub.ready()
	sub.drain()

	require.NoError(t, h.publish(eventOrders, []string{"a"}))
	select {
	case <-sub.ready():
		t.Fatalf("identical payload was sent again: %v", sub.drain())
	default:
	}

	require.NoError(t, h.publish(eventOrders, []string{"a", "b"}))
	<-sub.ready()
	events := sub.drain()
	require.Len(t, events, 1)
	assert.JSONEq(t, `["a","b"]`, string(events[0].Data))
}

func TestHub_slowSubscriberEndsOnLatest(t *testing.T) {
	h := newHub()
	sub := h.subscribe()

	// The subscriber does not read while many snapshots are published.
	for i := range 1000 {
		require.NoError(t, h.publish(eventOrders, i))
		require.NoError(t, h.publish(eventCounts, -i))
	}

	<-sub.ready()
	events := sub.drain()
	require.Len(t, events, 2)
	assert.Equal(t, []string{eventOrders, eventCounts}, eventTypes(events))
	assert.Equal(t, "999", string(events[0].Data))
	assert.Equal(t, "-999", string(events[1].Data))
}

func TestCoalesce_runsOncePerBurstAndAfterTheLastTrigger(t *testing.T) {
	ctx := t.Context()

	var runs atomic.Int32
	trigger := coalesce(ctx, 20*time.Millisecond, func() { runs.Add(1) })

	for range 1000 {
		trigger()
	}
	assert.Eventually(t, func() bool { return runs.Load() == 1 }, time.Second, 5*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, int32(1), runs.Load(), "a burst must produce a single run")

	trigger()
	assert.Eventually(t, func() bool { return runs.Load() == 2 }, time.Second, 5*time.Millisecond)
}

func TestEventsStream_sendsHeartbeatsAndNothingUnreadable(t *testing.T) {
	h := newHub()
	require.NoError(t, h.publish(eventOrders, []string{"secret"}))

	// Without deps the user may read nothing, so only heartbeats may arrive.
	srv := httptest.NewServer(handleEventsStream(h, nil, 20*time.Millisecond))
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var events []string
	scanner := bufio.NewScanner(resp.Body)
	for len(events) < 3 && scanner.Scan() {
		if name, ok := strings.CutPrefix(scanner.Text(), "event: "); ok {
			events = append(events, name)
		}
	}
	require.NoError(t, scanner.Err())
	assert.Equal(t, []string{eventHeartbeat, eventHeartbeat, eventHeartbeat}, events)
}
