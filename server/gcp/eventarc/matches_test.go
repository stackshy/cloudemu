// Matches-scoping guard for the operations claim: a named operations-path
// request (any of GET/POST :cancel/DELETE) is claimed by this handler only
// when it has no shared LRO registry (a standalone package server). When an
// assembled server wires the shared registry via SetOperationRegistry, this
// handler must stop claiming the operations path entirely and defer to the
// shared lro.Handler — which is registered ahead of it and, unlike this
// handler previously did, 404s an operation name it never created instead of
// fabricating success. See server/gcp/lro for the shared handler.

package eventarc_test

import (
	"net/http"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/gcp/eventarc"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
)

func TestHandlerStopsClaimingOperationsWhenRegistryWired(t *testing.T) {
	h := eventarc.New(nil)
	h.SetOperationRegistry(lro.NewRegistry())

	const base = "/v1/projects/demo/locations/us/operations/bogus-op"

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"get", http.MethodGet, base},
		{"cancel", http.MethodPost, base + ":cancel"},
		{"delete", http.MethodDelete, base},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, tc.path, http.NoBody)
			if h.Matches(req) {
				t.Errorf("Matches(%s %s) = true, want false: a wired shared registry means the shared lro handler "+
					"owns this path, not this handler", tc.method, tc.path)
			}
		})
	}

	// The triggers collection is unaffected by the operations-claim guard.
	req, _ := http.NewRequest(http.MethodGet, "/v1/projects/demo/locations/us/triggers", http.NoBody)
	if !h.Matches(req) {
		t.Fatalf("Matches(triggers) = false, want true")
	}
}

func TestHandlerClaimsOperationsWhenStandalone(t *testing.T) {
	h := eventarc.New(nil)

	const base = "/v1/projects/demo/locations/us/operations/op-1"

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"get", http.MethodGet, base},
		{"cancel", http.MethodPost, base + ":cancel"},
		{"delete", http.MethodDelete, base},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, tc.path, http.NoBody)
			if !h.Matches(req) {
				t.Errorf("Matches(%s %s) = false, want true: a standalone handler (no shared registry) "+
					"must keep answering its own operation polls", tc.method, tc.path)
			}
		})
	}
}
