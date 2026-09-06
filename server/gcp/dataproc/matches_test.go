package dataproc

import (
	"net/http"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	dataprocprovider "github.com/stackshy/cloudemu/v2/providers/gcp/dataproc"
)

func newHandler() *Handler {
	mock := dataprocprovider.New(config.NewOptions(config.WithProjectID("p")))

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
		{"clusters collection", "/v1/projects/p/regions/us-central1/clusters", true},
		{"cluster item", "/v1/projects/p/regions/us-central1/clusters/c", true},
		{"operation poll", "/v1/projects/p/regions/us-central1/operations/op-1", true},
		{"other region resource", "/v1/projects/p/regions/us-central1/autoscalingPolicies", false},
		{"locations space (functions/gke/etc.)", "/v1/projects/p/locations/us-central1/clusters", false},
		{"instances space (spanner/cloudsql)", "/v1/projects/p/instances/i", false},
		{"compute regional prefix", "/compute/v1/projects/p/regions/us-central1/subnetworks", false},
		{"bare region", "/v1/projects/p/regions/us-central1", false},
		{"non-v1 path", "/dns/v1/projects/p/regions/us-central1/clusters", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.Matches(request(http.MethodGet, tc.path)); got != tc.want {
				t.Fatalf("Matches(%s) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
