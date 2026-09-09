package accesscontextmanager

import (
	"net/http"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	acmprovider "github.com/stackshy/cloudemu/v2/providers/gcp/accesscontextmanager"
)

func newHandler() *Handler {
	mock := acmprovider.New(config.NewOptions(config.WithProjectID("p")))

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
		{"accessPolicies collection", "/v1/accessPolicies", true},
		{"accessPolicy item", "/v1/accessPolicies/123", true},
		{"accessLevels collection", "/v1/accessPolicies/123/accessLevels", true},
		{"accessLevel item", "/v1/accessPolicies/123/accessLevels/corp", true},
		{"servicePerimeters collection", "/v1/accessPolicies/123/servicePerimeters", true},
		{"servicePerimeter item", "/v1/accessPolicies/123/servicePerimeters/peri", true},
		{"unknown child collection", "/v1/accessPolicies/123/authorizedOrgsDescs", false},
		{"projects space (siblings)", "/v1/projects/p/locations/global/certificates", false},
		{"project operations (shared poller)", "/v1/projects/p/locations/global/operations/op-1", false},
		{"root operation not ours", "/v1/operations/unknown-op", false},
		{"non-v1 path", "/dns/v1/accessPolicies", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.Matches(request(http.MethodGet, tc.path)); got != tc.want {
				t.Fatalf("Matches(%s) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestMatchesClaimsOnlyOwnRootOperations verifies the handler claims a root
// /v1/operations/{id} poll only for an operation it actually minted, so a
// sibling handler's root operations (Cloud Functions gen1) fall through.
func TestMatchesClaimsOnlyOwnRootOperations(t *testing.T) {
	mock := acmprovider.New(config.NewOptions(config.WithProjectID("p")))
	h := New(mock)

	// Foreign root operation: not minted here -> yield to a sibling.
	if h.Matches(request(http.MethodGet, "/v1/operations/cf-op-created-elsewhere")) {
		t.Fatalf("handler must not claim a root operation it did not mint")
	}

	// Mint a real operation via a create, then confirm its poll is claimed.
	rec := doCreatePolicy(t, h, "organizations/1", "t")
	opName := decodeOpName(t, rec)

	if !h.Matches(request(http.MethodGet, "/v1/"+opName)) {
		t.Fatalf("handler must claim its own root operation poll: %s", opName)
	}
}
