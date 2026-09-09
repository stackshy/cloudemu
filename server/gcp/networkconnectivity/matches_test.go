package networkconnectivity

import (
	"net/http"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	nccprovider "github.com/stackshy/cloudemu/v2/providers/gcp/networkconnectivity"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
)

func newHandler() *Handler {
	mock := nccprovider.New(config.NewOptions(config.WithProjectID("p")))

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
		{"hubs collection (global)", "/v1/projects/p/locations/global/hubs", true},
		{"hub item (global)", "/v1/projects/p/locations/global/hubs/h", true},
		{"spokes collection (regional)", "/v1/projects/p/locations/us-central1/spokes", true},
		{"spoke item (regional)", "/v1/projects/p/locations/us-central1/spokes/s", true},
		{"environments space (composer)", "/v1/projects/p/locations/us-central1/environments/e", false},
		{"deliveryPipelines space (clouddeploy)", "/v1/projects/p/locations/us-central1/deliveryPipelines/d", false},
		{"streams space (datastream)", "/v1/projects/p/locations/us-central1/streams/s", false},
		{"clusters space (gke)", "/v1/projects/p/locations/us-central1/clusters/c", false},
		{"jobs space (scheduler)", "/v1/projects/p/locations/us-central1/jobs/j", false},
		{"regions space (dataproc)", "/v1/projects/p/regions/us-central1/hubs", false},
		{"bare location", "/v1/projects/p/locations/global", false},
		{"non-v1 path", "/dns/v1/projects/p/locations/global/hubs", false},
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
// its own polls, while in an assembled server the shared poller wins (so it
// never regresses another service's operations into a 404).
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
