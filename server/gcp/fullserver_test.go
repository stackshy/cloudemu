package gcp_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// fullServer boots the complete GCP server with EVERY handler registered (as
// the `cloudemu serve --providers gcp` binary does), so cross-handler dispatch
// collisions surface — the kind single-driver package tests can't catch.
func fullServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := gcpserver.New(gcpserver.DriversFrom(cloudemu.NewGCP()))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func do(t *testing.T, ts *httptest.Server, method, path, body string) (int, string) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

// TestFullServerLROOperationPolling guards the #321 E2E fix: in the full server
// (alloydb/gke register before artifactregistry/eventarc/memorystore and used
// to shadow location operations), a shared LRO handler must resolve every
// location-scoped operation poll to done, not 404.
func TestFullServerLROOperationPolling(t *testing.T) {
	ts := fullServer(t)

	// artifactregistry: create returns an op named .../operations/op-r1.
	if code, _ := do(t, ts, http.MethodPost,
		"/v1/projects/demo/locations/us/repositories?repositoryId=r1", `{"format":"MAVEN"}`); code != http.StatusOK {
		t.Fatalf("AR create: %d", code)
	}

	if code, body := do(t, ts, http.MethodGet,
		"/v1/projects/demo/locations/us/operations/op-r1", ""); code != http.StatusOK || !strings.Contains(body, `"done":true`) {
		t.Fatalf("AR op poll: code=%d body=%s (want 200 done:true)", code, body)
	}

	// eventarc.
	do(t, ts, http.MethodPost,
		"/v1/projects/demo/locations/us-central1/triggers?triggerId=t1",
		`{"eventFilters":[{"attribute":"type","value":"x"}],"destination":{"cloudRun":{"service":"s","region":"us-central1"}}}`)

	if code, body := do(t, ts, http.MethodGet,
		"/v1/projects/demo/locations/us-central1/operations/op-t1", ""); code != http.StatusOK || !strings.Contains(body, `"done":true`) {
		t.Fatalf("eventarc op poll: code=%d body=%s", code, body)
	}

	// gke (a legitimate location-operations owner) must still resolve.
	do(t, ts, http.MethodPost, "/v1/projects/demo/locations/us-central1/clusters",
		`{"cluster":{"name":"k1","initialNodeCount":1}}`)

	// GKE's container.Operation uses a `status` field, not the longrunning
	// `done` bool — the shared handler must satisfy both.
	if code, body := do(t, ts, http.MethodGet,
		"/v1/projects/demo/locations/us-central1/operations/operation-00000001", ""); code != http.StatusOK ||
		!strings.Contains(body, `"status":"DONE"`) {
		t.Fatalf("gke op poll: code=%d body=%s (want status DONE)", code, body)
	}

	// ...and gke's own cluster GET must not be swallowed by the LRO handler.
	if code, _ := do(t, ts, http.MethodGet,
		"/v1/projects/demo/locations/us-central1/clusters/k1", ""); code != http.StatusOK {
		t.Fatalf("gke cluster GET: %d", code)
	}
}

// TestFullServerFirestoreCreate guards the #321 fix: a document write into a
// not-yet-existent collection auto-creates it (real Firestore), and a duplicate
// explicit id is ALREADY_EXISTS.
func TestFullServerFirestoreCreate(t *testing.T) {
	ts := fullServer(t)

	const path = "/v1/projects/demo/databases/(default)/documents/users?documentId=alice"
	body := `{"fields":{"name":{"stringValue":"Alice"}}}`

	if code, b := do(t, ts, http.MethodPost, path, body); code != http.StatusOK {
		t.Fatalf("create doc in new collection: %d %s", code, b)
	}

	if code, _ := do(t, ts, http.MethodPost, path, body); code != http.StatusConflict {
		t.Fatalf("duplicate create: %d, want 409", code)
	}
}

// TestFullServerComputeDelete guards the #321 fix: instance delete removes the
// resource (GET after is 404), not a TERMINATED tombstone.
func TestFullServerComputeDelete(t *testing.T) {
	ts := fullServer(t)

	const zone = "/compute/v1/projects/demo/zones/us-central1-a/instances"

	do(t, ts, http.MethodPost, zone,
		`{"name":"vm1","machineType":"zones/us-central1-a/machineTypes/e2-medium"}`)

	if code, _ := do(t, ts, http.MethodGet, zone+"/vm1", ""); code != http.StatusOK {
		t.Fatalf("GET before delete: %d", code)
	}

	do(t, ts, http.MethodDelete, zone+"/vm1", "")

	if code, _ := do(t, ts, http.MethodGet, zone+"/vm1", ""); code != http.StatusNotFound {
		t.Fatalf("GET after delete: %d, want 404", code)
	}
}

// TestFullServerGCSDoesNotSwallowAPIPaths guards the #321 fix: an unclaimed
// API-version path is NOT misrouted to GCS as a bogus bucket lookup.
func TestFullServerGCSDoesNotSwallowAPIPaths(t *testing.T) {
	ts := fullServer(t)

	code, body := do(t, ts, http.MethodGet, "/v1/roles", "")
	if strings.Contains(body, "bucket") {
		t.Errorf("/v1/roles misrouted to GCS: code=%d body=%s", code, body)
	}
}
