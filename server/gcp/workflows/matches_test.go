package workflows

import (
	"net/http"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	workflowsprovider "github.com/stackshy/cloudemu/v2/providers/gcp/workflows"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
)

func newHandler() *Handler {
	mock := workflowsprovider.New(config.NewOptions(config.WithProjectID("p")))

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
		{"workflows collection", "/v1/projects/p/locations/us-central1/workflows", true},
		{"workflow item", "/v1/projects/p/locations/us-central1/workflows/wf", true},
		{"operations (standalone owns them)", "/v1/projects/p/locations/us-central1/operations/op-1", true},
		{"environments space (composer)", "/v1/projects/p/locations/us-central1/environments/e", false},
		{"deliveryPipelines space (clouddeploy)", "/v1/projects/p/locations/us-central1/deliveryPipelines/dp", false},
		{"instances space (memorystore/filestore)", "/v1/projects/p/locations/us-central1/instances/i", false},
		{"jobs space (scheduler)", "/v1/projects/p/locations/us-central1/jobs/j", false},
		{"regions space (dataproc)", "/v1/projects/p/regions/us-central1/workflows", false},
		{"bare location", "/v1/projects/p/locations/us-central1", false},
		{"non-v1 path", "/dns/v1/projects/p/locations/us-central1/workflows", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.Matches(request(http.MethodGet, tc.path)); got != tc.want {
				t.Fatalf("Matches(%s) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestMatchesYieldsOperationsToSharedPoller verifies that once a shared LRO
// registry is wired, this handler stops claiming operation paths so the shared
// poller owns them (and does not steal a sibling's operation).
func TestMatchesYieldsOperationsToSharedPoller(t *testing.T) {
	h := newHandler()
	h.SetOperationRegistry(lro.NewRegistry())

	opPath := "/v1/projects/p/locations/us-central1/operations/op-1"
	if h.Matches(request(http.MethodGet, opPath)) {
		t.Fatalf("Matches claimed operations path with a shared registry wired")
	}

	// Resource paths are still claimed.
	if !h.Matches(request(http.MethodGet, "/v1/projects/p/locations/us-central1/workflows/wf")) {
		t.Fatalf("Matches stopped claiming its own resource path")
	}
}
