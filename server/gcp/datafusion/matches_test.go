package datafusion

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	datafusionprovider "github.com/stackshy/cloudemu/v2/providers/gcp/datafusion"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	dfdriver "github.com/stackshy/cloudemu/v2/services/datafusion/driver"
)

func newHandler() *Handler {
	mock := datafusionprovider.New(config.NewOptions(config.WithProjectID("p")))

	return New(mock)
}

func request(method, path, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r, _ = http.NewRequest(method, "http://x"+path, nil)
	} else {
		r, _ = http.NewRequest(method, "http://x"+path, bytes.NewBufferString(body))
	}

	return r
}

// TestMatchesNarrowing verifies the handler claims only genuinely-Data-Fusion
// traffic on the /instances path it shares with Memorystore/Filestore.
func TestMatchesNarrowing(t *testing.T) {
	h := newHandler()

	base := "/v1/projects/p/locations/us-central1"

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   bool
	}{
		{"create with datafusion type", http.MethodPost, base + "/instances", `{"type":"BASIC"}`, true},
		{"create with numeric enum type", http.MethodPost, base + "/instances", `{"type":2}`, true},
		{"create with unspecified numeric type falls through", http.MethodPost, base + "/instances", `{"type":0}`, false},
		{"create with unspecified string type falls through", http.MethodPost, base + "/instances", `{"type":"TYPE_UNSPECIFIED"}`, false},
		{"create redis body (no type) falls through", http.MethodPost, base + "/instances", `{"tier":"BASIC","memorySizeGb":1}`, false},
		{"create filestore body (no type) falls through", http.MethodPost, base + "/instances", `{"tier":"STANDARD","fileShares":[{}]}`, false},
		{"restart verb always claimed", http.MethodPost, base + "/instances/df:restart", "", true},
		{"item get of unowned falls through", http.MethodGet, base + "/instances/df", "", false},
		{"bare list with nothing owned falls through", http.MethodGet, base + "/instances", "", false},
		{"clusters space (gke)", http.MethodGet, base + "/clusters/c", "", false},
		{"environments space (composer)", http.MethodGet, base + "/environments/e", "", false},
		{"bare location", http.MethodGet, base, "", false},
		{"non-v1 path", http.MethodGet, "/dns/v1/projects/p/locations/us-central1/instances", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.Matches(request(tc.method, tc.path, tc.body)); got != tc.want {
				t.Fatalf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// TestMatchesOwnedItem verifies an item/list request is claimed once this store
// owns the instance (so Redis/Filestore items on the shared path fall through,
// but Data Fusion's own do not).
func TestMatchesOwnedItem(t *testing.T) {
	mock := datafusionprovider.New(config.NewOptions(config.WithProjectID("p")))
	h := New(mock)
	base := "/v1/projects/p/locations/us-central1"

	if h.Matches(request(http.MethodGet, base+"/instances/df", "")) {
		t.Fatalf("unowned item must not be claimed")
	}

	if _, _, err := mock.CreateInstance(context.Background(),
		&dfdriver.Config{Project: "p", Location: "us-central1", ID: "df"}); err != nil {
		t.Fatalf("seed create: %v", err)
	}

	if !h.Matches(request(http.MethodGet, base+"/instances/df", "")) {
		t.Fatalf("owned item must be claimed")
	}

	if !h.Matches(request(http.MethodGet, base+"/instances", "")) {
		t.Fatalf("bare list must be claimed once an instance is owned")
	}
}

// TestMatchesOperationsYieldToPoller verifies the handler claims operation polls
// only when it has no shared LRO registry: a standalone package server answers
// its own polls, while in an assembled server the shared poller wins (so it
// never regresses another service's operations into a 404).
func TestMatchesOperationsYieldToPoller(t *testing.T) {
	standalone := newHandler()

	opPath := "/v1/projects/p/locations/us-central1/operations/op-1"
	if !standalone.Matches(request(http.MethodGet, opPath, "")) {
		t.Fatalf("standalone handler should claim its own operation polls")
	}

	shared := newHandler()
	shared.SetOperationRegistry(lro.NewRegistry())

	if shared.Matches(request(http.MethodGet, opPath, "")) {
		t.Fatalf("handler with shared registry must yield operation polls to the poller")
	}
}
