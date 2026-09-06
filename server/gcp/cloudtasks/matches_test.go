package cloudtasks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	"github.com/stackshy/cloudemu/v2/server/gcp/cloudtasks"
)

func TestMatchesClaimsOnlyQueues(t *testing.T) {
	h := cloudtasks.New(cloudemu.NewGCP().CloudTasks)

	cases := []struct {
		path string
		want bool
	}{
		{"/v2/projects/p/locations/us/queues", true},
		{"/v2/projects/p/locations/us/queues/q1", true},
		{"/v2/projects/p/locations/us/queues/q1:pause", true},
		{"/v2/projects/p/locations/us/queues/q1:setIamPolicy", true},
		// Disjoint from Cloud Run (jobs|services, also /v2/) and the /v1/ family.
		{"/v2/projects/p/locations/us/jobs", false},
		{"/v2/projects/p/locations/us/services", false},
		{"/v1/projects/p/locations/us/queues", false},
		{"/v1/projects/p/locations/us/jobs", false},
	}

	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, c.path, nil)
		if got := h.Matches(r); got != c.want {
			t.Errorf("Matches(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestIntEnumTolerance drives the raw REST endpoint with state encoded as its
// protojson integer (GAPIC clients marshal enums as numbers), and confirms the
// stored + echoed value is the canonical string name.
func TestIntEnumTolerance(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{CloudTasks: cloud.CloudTasks})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// state 2 == PAUSED in google.cloud.tasks.v2.Queue.State. A create can't set
	// state (output-only), but the normalizer must still rewrite the int so a
	// GAPIC client that sends it never trips the decoder, and the create default
	// (RUNNING) wins.
	body := map[string]any{
		"name":  "projects/p/locations/us/queues/inttest",
		"state": 2,
	}

	buf, _ := json.Marshal(body)
	url := ts.URL + "/v2/projects/p/locations/us/queues"

	resp, err := http.Post(url, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got struct {
		State string `json:"state"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.State != "RUNNING" {
		t.Fatalf("state = %q, want RUNNING (create default, int 2 normalized without error)", got.State)
	}

	q, err := cloud.CloudTasks.GetQueue(context.Background(), "projects/p/locations/us/queues/inttest")
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}

	if q.State != "RUNNING" {
		t.Fatalf("stored state = %q, want RUNNING", q.State)
	}
}
