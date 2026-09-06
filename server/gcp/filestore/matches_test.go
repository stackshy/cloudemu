package filestore

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
)

func req(t *testing.T, method, path, body string) *http.Request {
	t.Helper()

	var r *http.Request
	if body == "" {
		r, _ = http.NewRequest(method, path, http.NoBody)
	} else {
		r, _ = http.NewRequest(method, path, strings.NewReader(body))
	}

	return r
}

// TestMatchesCreateByContent guards the create-body disambiguation: a Filestore
// create body (fileShares/networks) is claimed; a Memorystore Redis create body
// is not, so it falls through.
func TestMatchesCreateByContent(t *testing.T) {
	h := New(nil)
	h.SetOperationRegistry(lro.NewRegistry())

	const path = "/v1/projects/p/locations/us/instances"

	cases := []struct {
		name string
		body string
		want bool
	}{
		{"fileShares", `{"tier":"BASIC_HDD","fileShares":[{"name":"s","capacityGb":"1024"}]}`, true},
		{"networks", `{"tier":"BASIC_HDD","networks":[{"network":"default"}]}`, true},
		{"redis-body", `{"tier":"BASIC","memorySizeGb":1}`, false},
		{"empty-body", `{}`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.Matches(req(t, http.MethodPost, path, tc.body)); got != tc.want {
				t.Errorf("Matches(POST %s) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// TestMatchesItemByOwnership guards that an item GET/PATCH/DELETE is claimed
// only when this store owns the instance, so Memorystore's item traffic falls
// through.
func TestMatchesItemByOwnership(t *testing.T) {
	h := New(nil)
	h.SetOperationRegistry(lro.NewRegistry())

	const item = "/v1/projects/p/locations/us/instances/x"

	if h.Matches(req(t, http.MethodGet, item, "")) {
		t.Fatalf("Matches(GET item) = true before create, want false")
	}

	if err := h.store.create(instanceName("p", "us", "x"), &instanceModel{}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, m := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		if !h.Matches(req(t, m, item, "")) {
			t.Errorf("Matches(%s item) = false after create, want true", m)
		}
	}
}

// TestMatchesListByOwnership guards that the bare LIST is claimed only when this
// store owns an instance in the addressed location.
func TestMatchesListByOwnership(t *testing.T) {
	h := New(nil)
	h.SetOperationRegistry(lro.NewRegistry())

	const list = "/v1/projects/p/locations/us/instances"

	if h.Matches(req(t, http.MethodGet, list, "")) {
		t.Fatalf("Matches(LIST) = true with empty store, want false (falls through to Memorystore)")
	}

	if err := h.store.create(instanceName("p", "us", "x"), &instanceModel{}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if !h.Matches(req(t, http.MethodGet, list, "")) {
		t.Errorf("Matches(LIST) = false after create, want true")
	}

	// A different location this store does not populate falls through.
	if h.Matches(req(t, http.MethodGet, "/v1/projects/p/locations/eu/instances", "")) {
		t.Errorf("Matches(LIST other-location) = true, want false")
	}
}

// TestMatchesDefersOperations guards that operations paths defer to the shared
// lro.Handler when a registry is wired, and are claimed only when standalone.
func TestMatchesDefersOperations(t *testing.T) {
	const op = "/v1/projects/p/locations/us/operations/op-1"

	wired := New(nil)
	wired.SetOperationRegistry(lro.NewRegistry())

	if wired.Matches(req(t, http.MethodGet, op, "")) {
		t.Errorf("Matches(operations) = true with shared registry, want false")
	}

	standalone := New(nil)
	if !standalone.Matches(req(t, http.MethodGet, op, "")) {
		t.Errorf("Matches(operations) = false standalone, want true")
	}
}
