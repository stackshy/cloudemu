package privateca

import (
	"net/http"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	privatecaprovider "github.com/stackshy/cloudemu/v2/providers/gcp/privateca"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
)

func newHandler() *Handler {
	mock := privatecaprovider.New(config.NewOptions(config.WithProjectID("p")))

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
		{"caPools collection", "/v1/projects/p/locations/us-central1/caPools", true},
		{"caPool item", "/v1/projects/p/locations/us-central1/caPools/pool", true},
		{"authorities collection", "/v1/projects/p/locations/us-central1/caPools/pool/certificateAuthorities", true},
		{"authority item", "/v1/projects/p/locations/us-central1/caPools/pool/certificateAuthorities/ca", true},
		{"authority verb", "/v1/projects/p/locations/us-central1/caPools/pool/certificateAuthorities/ca:enable", true},
		{"certificates collection", "/v1/projects/p/locations/us-central1/caPools/pool/certificates", true},
		{"certificate verb", "/v1/projects/p/locations/us-central1/caPools/pool/certificates/c:revoke", true},
		{"templates collection", "/v1/projects/p/locations/us-central1/certificateTemplates", true},
		{"template item", "/v1/projects/p/locations/us-central1/certificateTemplates/t", true},
		{"location-level certificates (certificatemanager)", "/v1/projects/p/locations/global/certificates", false},
		{"certificateMaps space (certificatemanager)", "/v1/projects/p/locations/global/certificateMaps", false},
		{"environments space (composer)", "/v1/projects/p/locations/us-central1/environments/e", false},
		{"unknown nested collection", "/v1/projects/p/locations/us-central1/caPools/pool/widgets", false},
		{"bare location", "/v1/projects/p/locations/us-central1", false},
		{"non-v1 path", "/dns/v1/projects/p/locations/us-central1/caPools", false},
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
// only when it has no shared LRO registry: a standalone package server answers its
// own polls, while in an assembled server the shared poller wins (so it never
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
