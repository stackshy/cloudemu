package kubernetes

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A watch with a labelSelector must stream only matching objects — both in the
// initial snapshot and in live events. Regression guard for the blocker where
// watch streams ignored the selector, polluting informer caches with
// non-matching objects.
func TestWatch_LabelSelectorFiltersStream(t *testing.T) {
	api := NewAPIServer()
	uid, state := api.RegisterCluster()
	ts := httptest.NewServer(api)
	ts.Config.SetKeepAlivesEnabled(false)
	api.SetBaseURL(ts.URL)

	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})

	// Seed one matching (app=a) and one non-matching (app=b) Pod.
	state.mu.Lock()
	for _, p := range []struct{ name, app string }{{"pod-a", "a"}, {"pod-b", "b"}} {
		state.pods["default/"+p.name] = &corev1.Pod{
			TypeMeta:   metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{Name: p.name, Namespace: "default", Labels: map[string]string{"app": p.app}},
		}
	}
	state.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	url := ts.URL + "/k8s/" + uid + "/api/v1/namespaces/default/pods?watch=true&labelSelector=app%3Da"
	req, _ := newRequestWithContext(ctx, url)

	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("watch request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("watch status: %d", resp.StatusCode)
	}

	// Decode streamed events until the deadline closes the body.
	seen := map[string]bool{}
	dec := json.NewDecoder(resp.Body)

	for {
		var ev struct {
			Object struct {
				Metadata struct {
					Name   string            `json:"name"`
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
			} `json:"object"`
		}
		if err := dec.Decode(&ev); err != nil {
			break
		}
		seen[ev.Object.Metadata.Name] = true

		if ev.Object.Metadata.Labels["app"] != "a" {
			t.Fatalf("watch streamed non-matching pod %q (labels=%v)", ev.Object.Metadata.Name, ev.Object.Metadata.Labels)
		}
	}

	if !seen["pod-a"] {
		t.Fatal("watch did not stream the matching pod pod-a")
	}
	if seen["pod-b"] {
		t.Fatal("watch streamed the non-matching pod pod-b")
	}
}
