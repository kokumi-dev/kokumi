package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	// endedSessionsPruneInterval is how often a server drops the entries of
	// the record that no session can match any more.
	endedSessionsPruneInterval = 10 * time.Minute
	// pendingEntryTTL is how long an entry this server recorded counts while
	// the watch has not delivered it yet.
	pendingEntryTTL = time.Minute
)

// endedSessions is the record of sessions that ended before their tokens
// expire, kept in the ConfigMap kokumi-server-ended-sessions so that every
// replica refuses them and the record survives restarts. Each entry maps a
// key to the time the session ended (RFC 3339):
//
//   - ksid.<h> ends the Kokumi session with that sid, at a sign-out;
//   - sid.<h> ends the Kokumi sessions of the provider session with that sid;
//   - sub.<h> ends the Kokumi sessions of that provider user that signed in
//     before the entry's time.
//
// <h> is a hash of the identifiers, so the record holds no user names. An
// entry is dropped sessionTTL after its time, when every session it can match
// has ended anyway. Entries are added and dropped by JSON merge patch, so
// replicas never conflict.
type endedSessions struct {
	writer client.Client
	key    types.NamespacedName

	mu      sync.Mutex
	entries map[string]time.Time
	// pending are entries this server recorded, by when, that the watch has
	// not delivered yet.
	pending map[string]time.Time
	changed chan struct{}
}

// newEndedSessions returns the record of the install namespace, written
// through writer.
func newEndedSessions(writer client.Client, namespace string) *endedSessions {
	return &endedSessions{
		writer:  writer,
		key:     types.NamespacedName{Namespace: namespace, Name: deliveryv1alpha1.EndedSessionsConfigMapName},
		entries: map[string]time.Time{},
		pending: map[string]time.Time{},
		changed: make(chan struct{}),
	}
}

// endedSessionKey is the entry of the Kokumi session sid.
func endedSessionKey(sid string) string {
	return "ksid." + entryHash(sid)
}

// endedProviderSessionKey is the entry of the provider session sid of issuer.
func endedProviderSessionKey(issuer, sid string) string {
	return "sid." + entryHash(issuer, sid)
}

// endedProviderUserKey is the entry of the user sub of issuer.
func endedProviderUserKey(issuer, sub string) string {
	return "sub." + entryHash(issuer, sub)
}

// entryHash returns the first 32 hex digits of a SHA-256 over parts,
// separated by NUL.
func entryHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

// ended reports whether the session of t has ended. A nil record ends nothing.
func (e *endedSessions) ended(t *verifiedToken) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.entries[endedSessionKey(t.session.ID)]; ok {
		return true
	}
	id := t.identity
	if id.Provider != providerOIDC {
		return false
	}
	if sid := t.session.ProviderSessionID; sid != "" {
		if _, ok := e.entries[endedProviderSessionKey(id.Issuer, sid)]; ok {
			return true
		}
	}
	at, ok := e.entries[endedProviderUserKey(id.Issuer, id.Subject)]
	return ok && t.session.AuthTime.Before(at)
}

// changes returns a channel that is closed at the next change of the record.
func (e *endedSessions) changes() <-chan struct{} {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.changed
}

// notify wakes the waiters on changes; the caller holds e.mu.
func (e *endedSessions) notify() {
	close(e.changed)
	e.changed = make(chan struct{})
}

// update replaces the entries with those of cm, as the watch delivers it. The
// entries this server recorded and the watch has not delivered yet stay.
func (e *endedSessions) update(cm *corev1.ConfigMap) {
	entries := make(map[string]time.Time, len(cm.Data))
	for key, value := range cm.Data {
		if at, err := time.Parse(time.RFC3339, value); err == nil {
			entries[key] = at
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	for key, recorded := range e.pending {
		_, delivered := entries[key]
		switch {
		case delivered || now.Sub(recorded) > pendingEntryTTL:
			delete(e.pending, key)
		default:
			entries[key] = e.entries[key]
		}
	}
	e.entries = entries
	e.notify()
}

// record adds the entry key with the time at. It counts on this server at
// once, and on the others when their watch delivers it.
func (e *endedSessions) record(ctx context.Context, key string, at time.Time) error {
	if e == nil {
		return errors.New("no record of ended sessions")
	}
	if err := e.patch(ctx, map[string]any{key: at.UTC().Format(time.RFC3339)}); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.entries[key] = at
	e.pending[key] = time.Now()
	e.notify()
	return nil
}

// prune drops the entries that ended more than sessionTTL ago: every session
// they can match has ended by then. Every replica prunes; dropping an entry
// twice changes nothing.
func (e *endedSessions) prune(ctx context.Context, now time.Time, sessionTTL time.Duration) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	expired := map[string]any{}
	for key, at := range e.entries {
		if now.Sub(at) > sessionTTL {
			expired[key] = nil
		}
	}
	e.mu.Unlock()
	if len(expired) == 0 {
		return nil
	}
	return e.patch(ctx, expired)
}

// patch merges data into the ConfigMap's data; a nil value drops its key.
func (e *endedSessions) patch(ctx context.Context, data map[string]any) error {
	body, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		return err
	}
	cm := &corev1.ConfigMap{Name: e.key.Name, Namespace: e.key.Namespace}
	if err := e.writer.Patch(ctx, cm, client.RawPatch(types.MergePatchType, body)); err != nil {
		return fmt.Errorf("patching ConfigMap %s: %w", e.key, err)
	}
	return nil
}
