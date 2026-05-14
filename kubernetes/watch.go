package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Watch event types per the Kubernetes API contract. Wire format is
// {"type":"<EventType>","object":{...}} sent as one JSON object per chunk.
const (
	EventAdded    = "ADDED"
	EventModified = "MODIFIED"
	EventDeleted  = "DELETED"
)

// watchSubscriberBuffer is the per-subscriber channel capacity. Generous so
// a slow client can fall a few events behind without blocking the publisher;
// if a client falls past this, the publisher drops its events rather than
// stalling other subscribers (real apiserver disconnects slow watchers — we
// just shed load).
const watchSubscriberBuffer = 64

// watchEvent is the wire shape sent on each Watch chunk. object is left as
// any so the encoder picks up the concrete resource type's JSON tags.
type watchEvent struct {
	Type   string `json:"type"`
	Object any    `json:"object"`
}

// subscriber is one connected client waiting for events on a single resource
// kind+namespace tuple. Caller closes done to unsubscribe; the publisher
// drops events into ch and stops once done is closed.
type subscriber struct {
	namespace string // "" matches every namespace
	ch        chan watchEvent
	done      chan struct{}
}

// broadcaster fans out resource mutations to every connected Watch
// subscriber for a given resource kind. One broadcaster per kind (Pods,
// Services, etc.) is owned by ClusterState.
//
// publish never blocks the caller — it drops events on full subscriber
// channels rather than stalling other subscribers or the mutating handler.
type broadcaster struct {
	mu   sync.Mutex
	subs []*subscriber
}

func newBroadcaster() *broadcaster {
	return &broadcaster{}
}

// subscribe registers a fresh subscriber for the given namespace ("" =
// across all namespaces) and returns it. Caller must close sub.done to
// unsubscribe; broadcaster won't reference the channel after that.
func (b *broadcaster) subscribe(namespace string) *subscriber {
	sub := &subscriber{
		namespace: namespace,
		ch:        make(chan watchEvent, watchSubscriberBuffer),
		done:      make(chan struct{}),
	}

	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()

	return sub
}

// publish hands off an event to every subscriber whose namespace filter
// matches. Subscribers that have closed their done channel are pruned in
// the same pass.
func (b *broadcaster) publish(eventType, namespace string, obj any) {
	b.mu.Lock()
	defer b.mu.Unlock()

	keep := b.subs[:0]

	for _, sub := range b.subs {
		select {
		case <-sub.done:
			// Subscriber unsubscribed; drop without warning.
			continue
		default:
		}

		if sub.namespace != "" && sub.namespace != namespace {
			keep = append(keep, sub)

			continue
		}

		select {
		case sub.ch <- watchEvent{Type: eventType, Object: obj}:
		default:
			// Channel full — drop this event for this slow subscriber
			// rather than block the publisher / other subscribers. Real
			// apiserver would disconnect; we lose an event, which is
			// acceptable for a test backend.
		}

		keep = append(keep, sub)
	}

	b.subs = keep
}

// streamWatch handles ?watch=true requests for a given resource kind. It
// emits an initial ADDED event for every existing item (so client-go
// Reflectors see the full state), then streams events from the broadcaster
// until the client disconnects.
//
// initial is the slice of current items at subscribe time. items must be
// JSON-encodable as Kubernetes objects. namespace ("" for cluster-wide /
// all-namespaces) filters subsequent events.
func streamWatch[T any](
	ctx context.Context,
	w http.ResponseWriter,
	b *broadcaster,
	namespace string,
	initial []T,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeStatus(w, http.StatusInternalServerError, metav1.StatusReasonInternalError,
			"k8s api: watch requires a flushable ResponseWriter")

		return
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)

	// Subscribe BEFORE emitting the initial snapshot so we don't miss any
	// event published between snapshot and subscribe. The cost is that the
	// snapshot may overlap with a same-key event from the broadcaster —
	// client-go Reflectors deduplicate by resourceVersion, which is fine.
	sub := b.subscribe(namespace)
	defer close(sub.done)

	for _, item := range initial {
		if err := enc.Encode(watchEvent{Type: EventAdded, Object: item}); err != nil {
			return
		}

		flusher.Flush()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub.ch:
			if !ok {
				return
			}

			if err := enc.Encode(ev); err != nil {
				return
			}

			flusher.Flush()
		}
	}
}
