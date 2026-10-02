package backupdr

import (
	"net/http"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	backupdrprovider "github.com/stackshy/cloudemu/v2/providers/gcp/backupdr"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
)

func newHandler() *Handler {
	return New(backupdrprovider.New(config.NewOptions(config.WithProjectID("p"))))
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
		{"vaults collection", "/v1/projects/p/locations/us-central1/backupVaults", true},
		{"vault item", "/v1/projects/p/locations/us-central1/backupVaults/v", true},
		{"wildcard location list", "/v1/projects/p/locations/-/backupVaults", true},
		{"nested dataSources (out of scope)", "/v1/projects/p/locations/us-central1/backupVaults/v/dataSources", false},
		{"custom verb (out of scope)", "/v1/projects/p/locations/us-central1/backupVaults:fetchUsable", false},
		{"backupPlans (out of scope)", "/v1/projects/p/locations/us-central1/backupPlans", false},
		{"endpoints space (cloudids)", "/v1/projects/p/locations/us-central1/endpoints/e", false},
		{"instances space (memorystore/filestore)", "/v1/projects/p/locations/us-central1/instances/i", false},
		{"bare location", "/v1/projects/p/locations/us-central1", false},
		{"non-v1 path", "/v2/projects/p/locations/us-central1/backupVaults", false},
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
// its own polls, while in an assembled server the shared poller wins.
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
