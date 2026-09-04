package gcp_test

// Regression coverage for the operations-ownership bug: a POST or DELETE
// (operations.cancel / operations.delete) against the shared location
// operations path with an UNKNOWN operation name used to fall through the
// shared lro handler (which only claimed GET) onto whichever of
// artifactregistry / eventarc / memorystore / alloydb registered next, and
// each of those greedily answered `{"done":true}` for ANY operation id
// regardless of whether it created it. Real GCP 404s an operation name that
// doesn't exist, on every verb. These tests drive the FULL assembled server
// (as `cloudemu serve` does) so the actual dispatch order — not just the lro
// package in isolation — is what's under test.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestFullServerBogusOperationIs404OnEveryVerb is the core regression: GET,
// POST :cancel, and DELETE on an operation name nobody ever created must all
// 404, not fabricate success.
func TestFullServerBogusOperationIs404OnEveryVerb(t *testing.T) {
	ts := fullServer(t)

	const base = "/v1/projects/demo/locations/us/operations/never-created-this-op"

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
			code, body := do(t, ts, tc.method, tc.path, "")
			if code != http.StatusNotFound {
				t.Fatalf("%s %s = %d %s, want 404 NOT_FOUND", tc.method, tc.path, code, body)
			}

			if strings.Contains(body, `"done":true`) {
				t.Fatalf("%s %s fabricated success instead of 404: %s", tc.method, tc.path, body)
			}
		})
	}
}

// opName reads the "name" field out of a create response body.
func opName(t *testing.T, body string) string {
	t.Helper()

	var v struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil || v.Name == "" {
		t.Fatalf("cannot read operation name from %s (err=%v)", body, err)
	}

	return v.Name
}

// TestFullServerRealOperationsStillResolveAfterFix guards the fix's main
// regression risk: closing the fake-success hole must not break polling a
// REAL operation. artifactregistry and memorystore are a representative
// subset of the four operations-minting handlers — they share the identical
// doneOperation -> h.ops.Register mechanism eventarc and alloydb use (see
// server/gcp/{eventarc,alloydb} which the standalone Matches-gating tests in
// each package cover directly).
func TestFullServerRealOperationsStillResolveAfterFix(t *testing.T) {
	ts := fullServer(t)

	// artifactregistry: create, poll (done), cancel (no-op success on an
	// already-done op, matching real GCP), then delete (removes the record so
	// a later poll 404s).
	_, createBody := do(t, ts, http.MethodPost,
		"/v1/projects/demo/locations/us/repositories?repositoryId=r1", `{"format":"MAVEN"}`)
	arOp := "/v1/" + opName(t, createBody)

	if code, body := do(t, ts, http.MethodGet, arOp, ""); code != http.StatusOK || !strings.Contains(body, `"done":true`) {
		t.Fatalf("AR op GET: code=%d body=%s (want 200 done:true)", code, body)
	}

	if code, body := do(t, ts, http.MethodPost, arOp+":cancel", ""); code != http.StatusOK {
		t.Fatalf("AR op cancel: code=%d body=%s (want 200)", code, body)
	}

	if code, body := do(t, ts, http.MethodDelete, arOp, ""); code != http.StatusOK {
		t.Fatalf("AR op delete: code=%d body=%s (want 200)", code, body)
	}

	if code, _ := do(t, ts, http.MethodGet, arOp, ""); code != http.StatusNotFound {
		t.Fatalf("AR op GET after delete: code=%d (want 404)", code)
	}

	// memorystore: create and poll (done). Only Get is exercised on this one
	// (cancel/delete already proven end to end above via artifactregistry —
	// the shared lro handler applies identically to every registered name).
	_, msBody := do(t, ts, http.MethodPost,
		"/v1/projects/demo/locations/us/instances?instanceId=cache1", `{}`)
	msOp := "/v1/" + opName(t, msBody)

	if code, body := do(t, ts, http.MethodGet, msOp, ""); code != http.StatusOK || !strings.Contains(body, `"done":true`) {
		t.Fatalf("Memorystore op GET: code=%d body=%s (want 200 done:true)", code, body)
	}
}

// TestAlloyDBServerOperationOwnership covers the fourth handler (AlloyDB),
// which can't be enabled alongside GKE in fullServer (identical REST paths),
// so it gets its own minimal server via DriversFromWithAlloyDB.
func TestAlloyDBServerOperationOwnership(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.DriversFromWithAlloyDB(cloud))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// A bogus operation 404s on every verb.
	const base = "/v1/projects/demo/locations/us/operations/never-created-this-op"

	if code, _ := do(t, ts, http.MethodGet, base, ""); code != http.StatusNotFound {
		t.Fatalf("bogus op GET: code=%d, want 404", code)
	}

	if code, _ := do(t, ts, http.MethodPost, base+":cancel", ""); code != http.StatusNotFound {
		t.Fatalf("bogus op cancel: code=%d, want 404", code)
	}

	if code, _ := do(t, ts, http.MethodDelete, base, ""); code != http.StatusNotFound {
		t.Fatalf("bogus op delete: code=%d, want 404", code)
	}

	// A real AlloyDB cluster-create operation still resolves.
	_, createBody := do(t, ts, http.MethodPost,
		"/v1/projects/demo/locations/us/clusters?clusterId=c1", `{}`)
	op := "/v1/" + opName(t, createBody)

	if code, body := do(t, ts, http.MethodGet, op, ""); code != http.StatusOK || !strings.Contains(body, `"done":true`) {
		t.Fatalf("AlloyDB op GET: code=%d body=%s (want 200 done:true)", code, body)
	}
}
