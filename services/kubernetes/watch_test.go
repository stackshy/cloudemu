// Direct broadcaster + streamWatch unit tests. The HTTP-level end-to-end
// path is exercised by TestSDKEKSDataPlane_InformerObservesAddAndDelete in
// server/aws/eks — that's where the full real-client-go Watch scenario
// runs. These tests bypass HTTP entirely and hit the in-package primitives,
// which keeps them fast (~1ms each) and avoids the chunked-transfer
// teardown races that http.Server.Close() exhibits when subscribers don't
// close their connections deterministically.

package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// httpClient and newRequestWithContext are tiny helpers used by the
// HTTP-level watch test. Defined here so the test file doesn't need to
// touch net/http directly for what is otherwise an in-package unit test.
func httpClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
}

func newRequestWithContext(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Close = true

	return req, nil
}

// nonFlushingWriter is a ResponseWriter that intentionally does NOT
// implement http.Flusher, exercising streamWatch's defensive bail-out.
type nonFlushingWriter struct {
	rec *httptest.ResponseRecorder
}

func (n *nonFlushingWriter) Header() http.Header         { return n.rec.Header() }
func (n *nonFlushingWriter) Write(b []byte) (int, error) { return n.rec.Write(b) }
func (n *nonFlushingWriter) WriteHeader(code int)        { n.rec.WriteHeader(code) }

// errorOnWriteFlusher is a ResponseWriter+Flusher whose Write always
// errors, so streamWatch's enc.Encode fails on the first snapshot event.
type errorOnWriteFlusher struct {
	header http.Header
}

func (e *errorOnWriteFlusher) Header() http.Header {
	if e.header == nil {
		e.header = http.Header{}
	}

	return e.header
}

func (*errorOnWriteFlusher) Write([]byte) (int, error) {
	return 0, errWriteFailed
}

func (*errorOnWriteFlusher) WriteHeader(int) {}
func (*errorOnWriteFlusher) Flush()          {}

var errWriteFailed = &writeError{}

type writeError struct{}

func (*writeError) Error() string { return "simulated write failure" }

func TestBroadcaster_PublishToSubscriber(t *testing.T) {
	b := newBroadcaster()
	sub := b.subscribe("ns-a")

	b.publish(EventAdded, "ns-a", "obj-1")

	ev := mustReceive(t, sub, time.Second)
	if ev.Type != EventAdded || ev.Object != "obj-1" {
		t.Fatalf("got %+v, want {ADDED, obj-1}", ev)
	}
}

func TestBroadcaster_NamespaceFilter(t *testing.T) {
	b := newBroadcaster()

	scoped := b.subscribe("ns-a")
	cluster := b.subscribe("") // all namespaces

	b.publish(EventAdded, "ns-a", "in-a")
	b.publish(EventAdded, "ns-b", "in-b")

	// Scoped sub only sees ns-a events.
	ev := mustReceive(t, scoped, time.Second)
	if ev.Object != "in-a" {
		t.Fatalf("scoped first event: got %+v, want in-a", ev)
	}

	if got, ok := tryReceive(scoped, 50*time.Millisecond); ok {
		t.Fatalf("scoped sub saw cross-namespace event: %+v", got)
	}

	// Cluster-wide sub sees both.
	seen := map[string]bool{}

	for i := 0; i < 2; i++ {
		ev := mustReceive(t, cluster, time.Second)

		obj, ok := ev.Object.(string)
		if !ok {
			t.Fatalf("cluster-wide event object is not string: %+v", ev.Object)
		}

		seen[obj] = true
	}

	if !seen["in-a"] || !seen["in-b"] {
		t.Fatalf("cluster-wide sub missed events: seen=%v", seen)
	}
}

func TestBroadcaster_DropsOnFullChannel(t *testing.T) {
	b := newBroadcaster()
	sub := b.subscribe("")

	// Fill the subscriber's channel past its buffer.
	for i := 0; i < watchSubscriberBuffer+8; i++ {
		b.publish(EventAdded, "", i)
	}

	drained := 0

	for {
		select {
		case <-sub.ch:
			drained++
		default:
			// Channel drained — verify we got exactly the buffer's worth
			// (publisher dropped the overflow rather than blocking).
			if drained != watchSubscriberBuffer {
				t.Fatalf("drained %d events, want %d (= buffer size)", drained, watchSubscriberBuffer)
			}

			return
		}
	}
}

func TestBroadcaster_PrunesClosedSubscribers(t *testing.T) {
	b := newBroadcaster()
	alive := b.subscribe("")
	stale := b.subscribe("")
	close(stale.done) // simulate streamWatch returning

	b.publish(EventAdded, "", "x")

	// alive should get the event.
	ev := mustReceive(t, alive, time.Second)
	if ev.Object != "x" {
		t.Fatalf("alive sub: got %+v", ev)
	}

	// stale should have been pruned on the publish pass; len(b.subs) == 1.
	b.mu.Lock()
	got := len(b.subs)
	b.mu.Unlock()

	if got != 1 {
		t.Fatalf("subs after prune: got %d, want 1", got)
	}
}

func TestBroadcaster_ConcurrentPublishersAndSubscribers(t *testing.T) {
	b := newBroadcaster()

	const (
		subs       = 4
		eventsPerN = 32
	)

	subscribers := make([]*subscriber, subs)
	for i := range subscribers {
		subscribers[i] = b.subscribe("")
	}

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		for i := 0; i < eventsPerN; i++ {
			b.publish(EventAdded, "", i)
		}
	}()

	wg.Wait()

	// Every subscriber should see all events (buffer is bigger than count).
	for i, sub := range subscribers {
		got := 0

	drain:
		for {
			select {
			case <-sub.ch:
				got++
			case <-time.After(100 * time.Millisecond):
				break drain
			}
		}

		if got != eventsPerN {
			t.Fatalf("subscriber %d: got %d events, want %d", i, got, eventsPerN)
		}
	}
}

// TestWatchHandlersOverHTTP exercises each watchXxx dispatcher through
// the full HTTP stack — keeps per-function coverage honest. The tight
// context deadline (100ms) makes streamWatch return via ctx.Done() before
// httptest.Server.Close() needs to wait for it.
func TestWatchHandlersOverHTTP(t *testing.T) {
	for _, path := range []string{
		// Namespaced watches
		"/api/v1/namespaces/default/configmaps",
		"/api/v1/namespaces/default/pods",
		"/api/v1/namespaces/default/secrets",
		"/api/v1/namespaces/default/serviceaccounts",
		"/api/v1/namespaces/default/services",
		"/api/v1/namespaces/default/endpoints",
		"/apis/apps/v1/namespaces/default/deployments",
		// Cluster-scoped / all-namespaces watches
		"/api/v1/namespaces",
		"/api/v1/configmaps",
		"/api/v1/pods",
		"/api/v1/secrets",
		"/api/v1/serviceaccounts",
		"/api/v1/services",
		"/api/v1/endpoints",
		"/apis/apps/v1/deployments",
	} {
		t.Run(path, func(t *testing.T) {
			api := NewAPIServer()
			uid, _ := api.RegisterCluster()
			ts := httptest.NewServer(api)
			ts.Config.SetKeepAlivesEnabled(false)
			api.SetBaseURL(ts.URL)

			defer func() {
				ts.CloseClientConnections()
				ts.Close()
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			fullURL := ts.URL + "/k8s/" + uid + path + "?watch=true"

			req, _ := newRequestWithContext(ctx, fullURL)

			resp, err := httpClient().Do(req)
			if err != nil {
				// context deadline tripped before headers came back — also acceptable
				return
			}

			if resp.StatusCode != 200 {
				resp.Body.Close()
				t.Fatalf("watch %s: status %d", path, resp.StatusCode)
			}

			// Drain until the server ends the response (ctx deadline fires
			// on the server side via the request context).
			buf := make([]byte, 1024)

			for {
				if _, err := resp.Body.Read(buf); err != nil {
					break
				}
			}

			resp.Body.Close()
		})
	}
}

// TestStreamWatch_NoFlusher500s exercises the defensive
// flusher-not-supported branch — a ResponseWriter that doesn't implement
// http.Flusher must error out before headers are set so the caller gets
// a proper 500 status.
func TestStreamWatch_NoFlusher500s(t *testing.T) {
	b := newBroadcaster()
	sub := b.subscribe("")

	rec := httptest.NewRecorder()
	w := &nonFlushingWriter{rec: rec}

	// Cancellable ctx so the function returns even if it doesn't bail on
	// the no-flusher path (it should).
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	streamWatch(ctx, w, sub, []string{"x"})

	if rec.Code != 500 {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
}

// TestStreamWatch_EncodeErrorReturns exercises the path where the
// underlying writer fails mid-stream — streamWatch must return without
// trying to encode further events.
func TestStreamWatch_EncodeErrorReturns(t *testing.T) {
	b := newBroadcaster()
	sub := b.subscribe("")

	w := &errorOnWriteFlusher{}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	streamWatch(ctx, w, sub, []string{"item-1"})

	// Defensive: the function should have returned before the deadline,
	// proving it bails on the encode error rather than spinning.
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("streamWatch did not return after Write error; ctx deadline tripped")
	}
}

// TestStreamWatch_InitialSnapshotAndLiveEvents exercises streamWatch
// against an httptest.ResponseRecorder-like fake that captures the
// chunked output. We use httptest.NewRecorder for the snapshot phase
// (it's a Flusher) and a closing context to terminate the loop.
func TestStreamWatch_InitialSnapshotAndLiveEvents(t *testing.T) {
	b := newBroadcaster()
	sub := b.subscribe("")
	rec := httptest.NewRecorder()

	ctx, cancel := context.WithCancel(context.Background())

	// streamWatch runs in a goroutine; we publish a live event then
	// cancel the context to make it return.
	done := make(chan struct{})

	go func() {
		streamWatch(ctx, rec, sub, []string{"seed-1", "seed-2"})
		close(done)
	}()

	// Publish a live event AFTER subscribe is registered (since the caller
	// already subscribed before calling streamWatch).
	b.publish(EventAdded, "", "live")

	// Give streamWatch a moment to drain the snapshot + live event, then
	// cancel.
	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("streamWatch did not return after ctx cancel")
	}

	// Decode the recorded body — should be 3 JSON objects on separate
	// lines (newline added by json.Encoder).
	body := rec.Body.String()
	dec := json.NewDecoder(strings.NewReader(body))

	var events []watchEvent

	for {
		var ev watchEvent
		if err := dec.Decode(&ev); err != nil {
			break
		}

		events = append(events, ev)
	}

	if len(events) != 3 {
		t.Fatalf("events: got %d, want 3 (2 seed + 1 live)\nbody:\n%s", len(events), body)
	}

	expected := []struct {
		typ string
		obj string
	}{
		{EventAdded, "seed-1"},
		{EventAdded, "seed-2"},
		{EventAdded, "live"},
	}

	for i, want := range expected {
		if events[i].Type != want.typ {
			t.Fatalf("event %d type: got %q, want %q", i, events[i].Type, want.typ)
		}

		got, ok := events[i].Object.(string)
		if !ok {
			t.Fatalf("event %d object is not string: %+v", i, events[i].Object)
		}

		if got != want.obj {
			t.Fatalf("event %d object: got %q, want %q", i, got, want.obj)
		}
	}

	// Subscriber should have been auto-unsubscribed (sub.done closed).
	select {
	case <-sub.done:
	default:
		t.Fatal("streamWatch did not close sub.done on return")
	}
}

// helpers

func mustReceive(t *testing.T, sub *subscriber, deadline time.Duration) watchEvent {
	t.Helper()

	select {
	case ev := <-sub.ch:
		return ev
	case <-time.After(deadline):
		t.Fatalf("subscriber: no event within %s", deadline)

		return watchEvent{}
	}
}

func tryReceive(sub *subscriber, deadline time.Duration) (watchEvent, bool) {
	select {
	case ev := <-sub.ch:
		return ev, true
	case <-time.After(deadline):
		return watchEvent{}, false
	}
}
