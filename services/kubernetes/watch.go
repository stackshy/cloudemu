package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// serveWatch is the shared body of every typed watch handler: subscribe and
// snapshot under one RLock (the ordering streamWatch's contract requires), then
// stream selector-filtered events. collect runs under the held RLock. apiVersion
// and kind identify the resource so a post-sync BOOKMARK can carry the current
// list-level resourceVersion (client-go WatchList resume insurance).
func serveWatch[T any](
	s *ClusterState, w http.ResponseWriter, r *http.Request,
	b *broadcaster, namespace, apiVersion, kind string, collect func() []T, keep func(T) bool,
) {
	initial := watchSendInitialEvents(r)

	s.mu.RLock()
	sub := b.subscribe(namespace)
	items := collect()
	rv := s.clusterRVLocked()
	s.mu.RUnlock()
	streamWatch(r.Context(), w, sub, items, keep, watchOpts{
		resume:      watchResume(r) && !initial,
		bookmarks:   watchBookmarksEnabled(r) || initial,
		bookmarkObj: typedBookmark(apiVersion, kind, rv, initial),
	})
}

// typedBookmark builds the minimal object a BOOKMARK watch event carries for a
// typed kind: its apiVersion/kind and the current cluster resourceVersion. When
// initialEvents is set (a WatchList streaming-list request), the object also
// carries the k8s.io/initial-events-end annotation that tells a client-go
// reflector the initial state has been fully replayed — without it modern
// kubectl (rollout status / get -w / wait) blocks forever.
func typedBookmark(apiVersion, kind, rv string, initialEvents bool) *metav1.PartialObjectMetadata {
	bm := &metav1.PartialObjectMetadata{
		TypeMeta:   metav1.TypeMeta{APIVersion: apiVersion, Kind: kind},
		ObjectMeta: metav1.ObjectMeta{ResourceVersion: rv},
	}
	if initialEvents {
		bm.Annotations = map[string]string{metav1.InitialEventsAnnotationKey: watchQueryValue}
	}

	return bm
}

// watchOpts carries the per-request watch behaviors parsed from the query.
type watchOpts struct {
	// resume is true when the client passed resourceVersion>0 — it already has
	// the current state, so the initial full-snapshot replay is skipped and only
	// subsequent events are streamed. (There is no watch-cache history, so events
	// strictly between the client's RV and watch establishment are not backfilled
	// — a documented emulation simplification; a client that needs a guarantee
	// relists.)
	resume bool
	// bookmarks is true when the client passed allowWatchBookmarks=true.
	bookmarks bool
	// bookmarkObj, when non-nil and bookmarks is set, is emitted once after the
	// initial sync as a BOOKMARK event carrying the current resourceVersion, so
	// the client can resume from it without a relist. For a WatchList request
	// (sendInitialEvents=true) it also carries the k8s.io/initial-events-end
	// annotation marking the end of the initial-state replay.
	bookmarkObj any
}

// watchResume reports whether the request is resuming from a known
// resourceVersion (anything other than absent or "0").
func watchResume(r *http.Request) bool {
	rv := r.URL.Query().Get("resourceVersion")

	return rv != "" && rv != "0"
}

// watchBookmarksEnabled reports whether the client opted into BOOKMARK events.
func watchBookmarksEnabled(r *http.Request) bool {
	return r.URL.Query().Get("allowWatchBookmarks") == watchQueryValue
}

// watchSendInitialEvents reports whether the request is a WatchList
// streaming-list (`sendInitialEvents=true`): the current state is streamed as
// ADDED events, then a terminal BOOKMARK carrying the initial-events-end
// annotation is emitted. kubectl 1.30+ (rollout status / get -w / wait) and
// modern client-go informers default to this protocol and block until they see
// that annotated bookmark. resourceVersionMatch=NotOlderThan with an empty/"0"
// resourceVersion means "current state then stream", so the initial replay is
// always performed (resume is forced off by the caller).
func watchSendInitialEvents(r *http.Request) bool {
	return r.URL.Query().Get("sendInitialEvents") == watchQueryValue
}

// parseListSelectors extracts the labelSelector and fieldSelector from a list
// or watch request. A malformed labelSelector degrades to match-everything
// (matching the typed list path's existing behavior).
func parseListSelectors(r *http.Request) (sel labels.Selector, fields map[string]string) {
	sel, err := labels.Parse(r.URL.Query().Get("labelSelector"))
	if err != nil {
		sel = labels.Everything()
	}

	fields = parseFieldSelector(r.URL.Query().Get("fieldSelector"))

	return sel, fields
}

// metaFieldsMatch answers the metadata.name / metadata.namespace field
// selectors an object can satisfy from its ObjectMeta alone. Any other field
// key matches nothing — the same fail-closed convention as matchesFields.
func metaFieldsMatch(name, namespace string, fields map[string]string) bool {
	for k, v := range fields {
		switch k {
		case fieldMetadataName:
			if name != v {
				return false
			}
		case fieldMetadataNamespace:
			if namespace != v {
				return false
			}
		default:
			return false
		}
	}

	return true
}

// Watch event types per the Kubernetes API contract. Wire format is
// {"type":"<EventType>","object":{...}} sent as one JSON object per chunk.
const (
	EventAdded    = "ADDED"
	EventModified = "MODIFIED"
	EventDeleted  = "DELETED"
	// EventError carries a Status object (e.g. 410 Gone) that tells a client-go
	// reflector to relist — used when a slow watcher overflowed its buffer.
	EventError = "ERROR"
	// EventBookmark carries an object holding only the latest resourceVersion, so
	// a client that opted in (allowWatchBookmarks=true) can resume from it after a
	// disconnect without a full relist.
	EventBookmark = "BOOKMARK"
)

// expiredWatchStatus is the 410 Gone a real apiserver sends when a watch has
// fallen too far behind; client-go reacts by relisting.
func expiredWatchStatus() *metav1.Status {
	return &metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Code:     http.StatusGone,
		Reason:   metav1.StatusReasonExpired,
		Message:  "watch events were dropped for a slow client; relist required",
	}
}

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
	// overflow is signaled (once) when the publisher had to drop an event
	// because ch was full. streamWatch turns that into a 410 Gone so the client
	// relists rather than running with a silently-divergent cache.
	overflow chan struct{}
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
		overflow:  make(chan struct{}, 1),
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

		// Channel full → don't block the publisher or other subscribers. Signal
		// overflow (once, non-blocking) so streamWatch sends the client a 410
		// Gone and it relists, instead of silently missing events forever.
		select {
		case sub.ch <- watchEvent{Type: eventType, Object: obj}:
		default:
			select {
			case sub.overflow <- struct{}{}:
			default:
			}
		}

		keep = append(keep, sub)
	}

	b.subs = keep
}

// streamWatch handles ?watch=true requests for a given resource kind. It
// emits an initial ADDED event for every item in initial (so client-go
// Reflectors see the full state), then streams events from sub until the
// client disconnects.
//
// CALLER MUST SUBSCRIBE BEFORE TAKING THE SNAPSHOT, BOTH UNDER THE SAME
// state.mu LOCK. That ordering is what closes the otherwise-present race
// between snapshot-and-subscribe — without it, a mutation landing between
// snapshot-release and subscribe-register would be invisible to the
// subscriber (event published with no subscriber yet, state change not in
// snapshot). The handler pattern is:
//
//	s.mu.RLock()
//	sub := broadcaster.subscribe(namespace)   // visible to subsequent publishers
//	initial := <collect snapshot under RLock>
//	s.mu.RUnlock()
//	streamWatch(r.Context(), w, sub, initial)
//
// Any mutation in flight while we hold RLock waits for RUnlock and then
// publishes — the subscriber picks it up from sub.ch. Any mutation that
// completed before our RLock is already in the snapshot.
//
// streamWatch closes sub.done on return so broadcaster.publish can prune
// the subscriber slice on the next publish.
// keep, when non-nil, filters both the initial snapshot and streamed events to
// the objects a client's labelSelector/fieldSelector matches. Without it a
// selective watch (`kubectl get pods -l app=x -w`, or any informer built with a
// selector) would receive non-matching objects — polluting reflector caches and
// firing spurious reconciles, which the reconcile engine amplifies.
//
//nolint:gocyclo // snapshot + stream loop with selector filtering; splitting further would hide the subscribe/snapshot ordering contract.
func streamWatch[T any](
	ctx context.Context,
	w http.ResponseWriter,
	sub *subscriber,
	initial []T,
	keep func(T) bool,
	opts watchOpts,
) {
	defer close(sub.done)

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

	// A resuming watch (resourceVersion>0) already holds the current state, so
	// the full ADDED replay is skipped — only subsequent events are streamed.
	if !opts.resume {
		for _, item := range initial {
			if keep != nil && !keep(item) {
				continue
			}

			if !encodeWatchEvent(enc, flusher, watchEvent{Type: EventAdded, Object: item}) {
				return
			}
		}
	}

	// Emit a single post-sync BOOKMARK so an opted-in client learns the current
	// resourceVersion to resume from. Bypasses keep (a bookmark is not a T).
	if opts.bookmarks && opts.bookmarkObj != nil {
		if !encodeWatchEvent(enc, flusher, watchEvent{Type: EventBookmark, Object: opts.bookmarkObj}) {
			return
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.overflow:
			// We dropped at least one event for this slow watcher. Send a 410
			// Gone (Expired) like a real apiserver so the client-go reflector
			// relists rather than running with a permanently-stale cache, then
			// end the stream.
			encodeWatchEvent(enc, flusher, watchEvent{Type: EventError, Object: expiredWatchStatus()})

			return
		case ev, ok := <-sub.ch:
			if !ok {
				return
			}

			// Streamed objects are published as the same concrete type as
			// initial; apply the same selector filter. A type mismatch (never
			// expected) passes through rather than silently dropping.
			if obj, ok := ev.Object.(T); ok && keep != nil && !keep(obj) {
				continue
			}

			if !encodeWatchEvent(enc, flusher, ev) {
				return
			}
		}
	}
}

// encodeWatchEvent writes one watch event and flushes it, returning false if
// the write failed (client gone) so the caller stops streaming.
func encodeWatchEvent(enc *json.Encoder, flusher http.Flusher, ev watchEvent) bool {
	if err := enc.Encode(ev); err != nil {
		return false
	}

	flusher.Flush()

	return true
}
