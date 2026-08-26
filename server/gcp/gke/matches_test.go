// Matches-scoping guard for the operations claim: a named operation poll or
// :cancel is claimed only when the GKE mock recorded that op, so foreign-service
// operations fall through to the shared LRO handler instead of being shadowed.

package gke_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	providergke "github.com/stackshy/cloudemu/v2/providers/gcp/gke"
	gke "github.com/stackshy/cloudemu/v2/server/gcp/gke"
)

func TestHandlerMatchesScopesOperationClaims(t *testing.T) {
	opts := config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))),
		config.WithRegion("us-central1"),
		config.WithProjectID("demo"),
	)
	m := providergke.New(opts)
	h := gke.New(m)

	_, op, err := m.CreateCluster(context.Background(), &providergke.CreateClusterInput{
		Name:     "k1",
		Location: "us-central1",
	})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	base := "/v1/projects/demo/locations/us-central1/operations/"

	cases := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"own op GET", http.MethodGet, base + op.Name, true},
		{"own op cancel", http.MethodPost, base + op.Name + ":cancel", true},
		{"foreign op GET", http.MethodGet, base + "op-foreign", false},
		{"foreign op cancel", http.MethodPost, base + "op-foreign:cancel", false},
		{"operations list", http.MethodGet, "/v1/projects/demo/locations/us-central1/operations", true},
	}

	for _, tc := range cases {
		req, _ := http.NewRequest(tc.method, tc.path, http.NoBody)
		if got := h.Matches(req); got != tc.want {
			t.Errorf("%s: Matches(%s) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
}
