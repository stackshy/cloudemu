package certificatemanager

import (
	"net/http"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	certmanagerprovider "github.com/stackshy/cloudemu/v2/providers/gcp/certificatemanager"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
)

func newHandler() *Handler {
	mock := certmanagerprovider.New(config.NewOptions(config.WithProjectID("p")))

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
		{"certificates collection", "/v1/projects/p/locations/global/certificates", true},
		{"certificate item", "/v1/projects/p/locations/global/certificates/c", true},
		{"certificateMaps collection", "/v1/projects/p/locations/global/certificateMaps", true},
		{"certificateMap item", "/v1/projects/p/locations/global/certificateMaps/m", true},
		{"dnsAuthorizations collection", "/v1/projects/p/locations/global/dnsAuthorizations", true},
		{"dnsAuthorization item", "/v1/projects/p/locations/global/dnsAuthorizations/a", true},
		{"environments space (composer)", "/v1/projects/p/locations/us-central1/environments/e", false},
		{"streams space (datastream)", "/v1/projects/p/locations/us-central1/streams/s", false},
		{"deliveryPipelines space (clouddeploy)", "/v1/projects/p/locations/us-central1/deliveryPipelines/d", false},
		{"instances space (memorystore/filestore)", "/v1/projects/p/locations/us-central1/instances/i", false},
		{"clusters space (gke)", "/v1/projects/p/locations/us-central1/clusters/c", false},
		{"regions space (dataproc)", "/v1/projects/p/regions/us-central1/certificates", false},
		{"bare location", "/v1/projects/p/locations/global", false},
		{"non-v1 path", "/dns/v1/projects/p/locations/global/certificates", false},
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

	opPath := "/v1/projects/p/locations/global/operations/op-1"
	if !standalone.Matches(request(http.MethodGet, opPath)) {
		t.Fatalf("standalone handler should claim its own operation polls")
	}

	shared := newHandler()
	shared.SetOperationRegistry(lro.NewRegistry())

	if shared.Matches(request(http.MethodGet, opPath)) {
		t.Fatalf("handler with shared registry must yield operation polls to the poller")
	}
}
