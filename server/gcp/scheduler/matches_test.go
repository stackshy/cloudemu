package scheduler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	"github.com/stackshy/cloudemu/v2/server/gcp/scheduler"
)

func TestMatchesClaimsOnlyJobs(t *testing.T) {
	h := scheduler.New(cloudemu.NewGCP().Scheduler)

	cases := []struct {
		path string
		want bool
	}{
		{"/v1/projects/p/locations/us/jobs", true},
		{"/v1/projects/p/locations/us/jobs/nightly", true},
		{"/v1/projects/p/locations/us/jobs/nightly:pause", true},
		// Disjoint from Memorystore, Eventarc, GKE, Cloud Run (v2).
		{"/v1/projects/p/locations/us/instances", false},
		{"/v1/projects/p/locations/us/triggers", false},
		{"/v1/projects/p/locations/us/clusters", false},
		{"/v2/projects/p/locations/us/jobs", false},
		{"/v1/projects/p/secrets/s", false},
	}

	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, c.path, nil)
		if got := h.Matches(r); got != c.want {
			t.Errorf("Matches(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestIntEnumTolerance drives the raw REST endpoint with httpMethod encoded as
// its protojson integer (GAPIC clients marshal enums as numbers), and confirms
// the stored + echoed value is the canonical string name.
func TestIntEnumTolerance(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Scheduler: cloud.Scheduler})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// httpMethod 5 == DELETE in google.cloud.scheduler.v1.HttpMethod.
	body := map[string]any{
		"name":     "projects/p/locations/us/jobs/inttest",
		"schedule": "* * * * *",
		"httpTarget": map[string]any{
			"uri":        "https://e.example",
			"httpMethod": 5,
		},
	}

	buf, _ := json.Marshal(body)
	url := ts.URL + "/v1/projects/p/locations/us/jobs"

	resp, err := http.Post(url, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got struct {
		HTTPTarget struct {
			HTTPMethod string `json:"httpMethod"`
		} `json:"httpTarget"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.HTTPTarget.HTTPMethod != "DELETE" {
		t.Fatalf("httpMethod = %q, want DELETE (normalized from int 5)", got.HTTPTarget.HTTPMethod)
	}

	// And a Get reflects the canonical name too.
	j, err := cloud.Scheduler.GetJob(context.Background(), "projects/p/locations/us/jobs/inttest")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if j.HTTPTarget.HTTPMethod != "DELETE" {
		t.Fatalf("stored httpMethod = %q, want DELETE", j.HTTPTarget.HTTPMethod)
	}
}
