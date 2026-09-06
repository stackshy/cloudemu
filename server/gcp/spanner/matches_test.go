package spanner

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	spannerprovider "github.com/stackshy/cloudemu/v2/providers/gcp/spanner"
	spdriver "github.com/stackshy/cloudemu/v2/services/spanner/driver"
)

func newHandlerWithInstance(t *testing.T) *Handler {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	mock := spannerprovider.New(config.NewOptions(config.WithClock(fc), config.WithProjectID("p")))

	if _, _, err := mock.CreateInstance(context.Background(), spdriver.CreateInstanceConfig{
		Name:   "projects/p/instances/known",
		Config: "projects/p/instanceConfigs/regional-us-central1",
	}); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	return New(mock)
}

func req(method, path, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r, _ = http.NewRequest(method, "http://x"+path, strings.NewReader(body))
	} else {
		r, _ = http.NewRequest(method, "http://x"+path, nil)
	}

	return r
}

func TestMatchesDisambiguation(t *testing.T) {
	h := newHandlerWithInstance(t)

	spannerBody := `{"instanceId":"orders","instance":{"config":"projects/p/instanceConfigs/x"}}`
	cloudsqlBody := `{"name":"orders","databaseVersion":"POSTGRES_15","settings":{"tier":"db-custom-2-8192"}}`

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   bool
	}{
		{"list instances", http.MethodGet, "/v1/projects/p/instances", "", true},
		{"create with spanner body", http.MethodPost, "/v1/projects/p/instances", spannerBody, true},
		{"create with cloudsql body", http.MethodPost, "/v1/projects/p/instances", cloudsqlBody, false},
		{"get owned instance", http.MethodGet, "/v1/projects/p/instances/known", "", true},
		{"get unowned instance", http.MethodGet, "/v1/projects/p/instances/unknown", "", false},
		{"databases of owned", http.MethodGet, "/v1/projects/p/instances/known/databases", "", true},
		{"ddl of owned", http.MethodGet, "/v1/projects/p/instances/known/databases/d/ddl", "", true},
		{"operation of owned", http.MethodGet, "/v1/projects/p/instances/known/operations/op-1", "", true},
		{"databases of unowned", http.MethodGet, "/v1/projects/p/instances/unknown/databases", "", false},
		{"non-instances resource", http.MethodGet, "/v1/projects/p/topics", "", false},
		{"non-v1 path", http.MethodGet, "/sql/v1beta4/projects/p/instances", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.Matches(req(tc.method, tc.path, tc.body)); got != tc.want {
				t.Fatalf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// TestMatchesRestoresBody verifies the create body-peek leaves the request body
// intact for a subsequent ServeHTTP (or a fall-through to Cloud SQL).
func TestMatchesRestoresBody(t *testing.T) {
	h := newHandlerWithInstance(t)
	body := `{"instanceId":"orders","instance":{"config":"c"}}`
	r := req(http.MethodPost, "/v1/projects/p/instances", body)

	if !h.Matches(r) {
		t.Fatalf("expected spanner create to match")
	}

	got, _ := io.ReadAll(r.Body)
	if string(got) != body {
		t.Fatalf("body not restored: got %q", string(got))
	}
}
