package dataplex

import (
	"net/http"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	dataplexprovider "github.com/stackshy/cloudemu/v2/providers/gcp/dataplex"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
)

func newHandler() *Handler {
	mock := dataplexprovider.New(config.NewOptions(config.WithProjectID("p")))

	return New(mock)
}

func request(method, path string) *http.Request {
	r, _ := http.NewRequest(method, "http://x"+path, nil)

	return r
}

func TestMatchesNarrowing(t *testing.T) {
	h := newHandler()

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"lakes collection", "/v1/projects/p/locations/us-central1/lakes", true},
		{"lake item", "/v1/projects/p/locations/us-central1/lakes/l", true},
		{"zones collection", "/v1/projects/p/locations/us-central1/lakes/l/zones", true},
		{"zone item", "/v1/projects/p/locations/us-central1/lakes/l/zones/z", true},
		{"assets collection", "/v1/projects/p/locations/us-central1/lakes/l/zones/z/assets", true},
		{"asset item", "/v1/projects/p/locations/us-central1/lakes/l/zones/z/assets/a", true},
		{"environments space (composer)", "/v1/projects/p/locations/us-central1/environments/e", false},
		{"streams space (datastream)", "/v1/projects/p/locations/us-central1/streams/s", false},
		{"certificates space (certmanager)", "/v1/projects/p/locations/global/certificates/c", false},
		{"services space (metastore)", "/v1/projects/p/locations/us-central1/services/s", false},
		{"instances space (memorystore/filestore)", "/v1/projects/p/locations/us-central1/instances/i", false},
		{"zones mis-keyword", "/v1/projects/p/locations/us-central1/lakes/l/bogus/z", false},
		{"assets mis-keyword", "/v1/projects/p/locations/us-central1/lakes/l/zones/z/bogus/a", false},
		{"trailing junk", "/v1/projects/p/locations/us-central1/lakes/l/zones/z/assets/a/extra", false},
		{"bare location", "/v1/projects/p/locations/us-central1", false},
		{"non-v1 path", "/dns/v1/projects/p/locations/us-central1/lakes/l", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.Matches(request(http.MethodGet, tc.path)); got != tc.want {
				t.Fatalf("Matches(%s) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestMatchesOperationsYieldToPoller verifies the handler claims operation polls
// only when it has no shared LRO registry: a standalone package server answers
// its own polls, while in an assembled server the shared poller wins (so it never
// regresses another service's operations into a 404).
func TestMatchesOperationsYieldToPoller(t *testing.T) {
	standalone := newHandler()

	opPath := "/v1/projects/p/locations/us-central1/operations/op-1"
	if !standalone.Matches(request(http.MethodGet, opPath)) {
		t.Fatalf("standalone handler should claim its own operation polls")
	}

	shared := newHandler()
	shared.SetOperationRegistry(lro.NewRegistry())

	if shared.Matches(request(http.MethodGet, opPath)) {
		t.Fatalf("handler with shared registry must yield operation polls to the poller")
	}
}
