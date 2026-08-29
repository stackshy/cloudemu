// WatchList (streaming-list) protocol tests. kubectl 1.30+ and modern client-go
// informers default to the WatchList protocol
// (sendInitialEvents=true&resourceVersionMatch=NotOlderThan&allowWatchBookmarks=true&watch=true):
// the server streams current state as ADDED events, then a terminal BOOKMARK
// carrying metadata.annotations["k8s.io/initial-events-end"]="true". client-go
// blocks until it sees that annotated bookmark, so its absence hangs
// `kubectl rollout status`, `kubectl get -w`, and `kubectl wait` forever.

package kubernetes_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"
)

const initialEventsEndAnnotation = "k8s.io/initial-events-end"

// wireEvent is the on-the-wire watch event shape: {"type":..,"object":{..}}.
type wireEvent struct {
	Type   string `json:"type"`
	Object struct {
		Kind     string `json:"kind"`
		Metadata struct {
			ResourceVersion string            `json:"resourceVersion"`
			Annotations     map[string]string `json:"annotations"`
		} `json:"metadata"`
	} `json:"object"`
}

// readWatchUntilBookmark streams a watch response, returning the events seen up
// to and including the first BOOKMARK (or all events if the stream/ctx ends).
func readWatchUntilBookmark(t *testing.T, body io.Reader) []wireEvent {
	t.Helper()

	dec := json.NewDecoder(body)

	var events []wireEvent

	for {
		var ev wireEvent
		if err := dec.Decode(&ev); err != nil {
			return events
		}

		events = append(events, ev)

		if ev.Type == "BOOKMARK" {
			return events
		}
	}
}

// watchListURL builds a WatchList request URL for a list path.
func watchListURL(base, path string) string {
	return base + path +
		"?watch=true&sendInitialEvents=true&resourceVersionMatch=NotOlderThan&allowWatchBookmarks=true"
}

func TestWatchList_EmitsInitialEventsEndBookmark(t *testing.T) {
	cases := []struct {
		name       string
		createPath string
		listPath   string
		body       func(name string) []byte
		kind       string
	}{
		{
			name:       "typed_configmaps",
			createPath: "/api/v1/namespaces/default/configmaps",
			listPath:   "/api/v1/namespaces/default/configmaps",
			kind:       "ConfigMap",
			body: func(name string) []byte {
				return []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"` +
					name + `","namespace":"default"}}`)
			},
		},
		{
			name:       "registry_replicasets",
			createPath: "/apis/apps/v1/namespaces/default/replicasets",
			listPath:   "/apis/apps/v1/namespaces/default/replicasets",
			kind:       "ReplicaSet",
			body: func(name string) []byte {
				return []byte(`{"apiVersion":"apps/v1","kind":"ReplicaSet","metadata":{"name":"` +
					name + `","namespace":"default"}}`)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, closeFn := newFixture(t)
			defer closeFn()

			for _, name := range []string{"a", "b"} {
				do(t, http.MethodPost, base+tc.createPath, tc.body(name)).Body.Close()
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, watchListURL(base, tc.listPath), nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("watchlist request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("watchlist status: got %d, want 200", resp.StatusCode)
			}

			events := readWatchUntilBookmark(t, resp.Body)

			// Expect: 2 ADDED then a terminal BOOKMARK.
			if len(events) < 3 {
				t.Fatalf("got %d events, want >=3 (2 ADDED + BOOKMARK): %+v", len(events), events)
			}

			added := 0

			for _, ev := range events[:len(events)-1] {
				if ev.Type != "ADDED" {
					t.Fatalf("pre-bookmark event type: got %q, want ADDED", ev.Type)
				}

				added++
			}

			if added != 2 {
				t.Fatalf("ADDED events before bookmark: got %d, want 2", added)
			}

			last := events[len(events)-1]
			if last.Type != "BOOKMARK" {
				t.Fatalf("terminal event type: got %q, want BOOKMARK", last.Type)
			}

			if got := last.Object.Metadata.Annotations[initialEventsEndAnnotation]; got != "true" {
				t.Fatalf("bookmark %s annotation: got %q, want \"true\" (annotations=%v)",
					initialEventsEndAnnotation, got, last.Object.Metadata.Annotations)
			}

			if last.Object.Kind != tc.kind {
				t.Fatalf("bookmark kind: got %q, want %q", last.Object.Kind, tc.kind)
			}

			rv, err := strconv.Atoi(last.Object.Metadata.ResourceVersion)
			if err != nil || rv <= 0 {
				t.Fatalf("bookmark resourceVersion %q invalid: %v", last.Object.Metadata.ResourceVersion, err)
			}
		})
	}
}

// TestWatch_PlainBookmarkHasNoInitialEventsEndAnnotation is the regression guard:
// a plain `?watch=true&allowWatchBookmarks=true` (no sendInitialEvents) still gets
// a post-sync BOOKMARK, but WITHOUT the initial-events-end annotation — so we
// don't turn every ordinary watch into a WatchList sync.
func TestWatch_PlainBookmarkHasNoInitialEventsEndAnnotation(t *testing.T) {
	base, closeFn := newFixture(t)
	defer closeFn()

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		[]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"a","namespace":"default"}}`)).Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	url := base + "/api/v1/namespaces/default/configmaps?watch=true&allowWatchBookmarks=true"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("watch request: %v", err)
	}
	defer resp.Body.Close()

	events := readWatchUntilBookmark(t, resp.Body)

	last := events[len(events)-1]
	if last.Type != "BOOKMARK" {
		t.Fatalf("terminal event type: got %q, want BOOKMARK", last.Type)
	}

	if _, present := last.Object.Metadata.Annotations[initialEventsEndAnnotation]; present {
		t.Fatalf("plain watch bookmark must NOT carry the %s annotation: %v",
			initialEventsEndAnnotation, last.Object.Metadata.Annotations)
	}
}
