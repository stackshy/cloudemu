package servicedirectory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newBetaServer stands up the full GCP wire server so requests route through the
// dispatcher's Matches guard exactly as the google-beta Terraform provider hits
// it at its default /v1beta1/ base path.
func newBetaServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewGCP()
	ts := httptest.NewServer(gcpserver.NewFromProvider(cloud))
	t.Cleanup(ts.Close)

	return ts
}

// doJSON issues a request against ts and decodes the JSON response into out,
// asserting a 200. It fails the test on any transport, status, or decode error.
func doJSON(t *testing.T, ts *httptest.Server, method, path string, body any, out any) {
	t.Helper()

	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}

		rdr = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s %s: status %d body %s", method, path, resp.StatusCode, raw)
	}

	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s %s: %v (body %s)", method, path, err, raw)
		}
	}
}

// TestV1Beta1MetadataRoundTrips drives the /v1beta1/ path the google-beta
// Terraform provider uses and asserts the service/endpoint string map sent under
// the v1beta1 `metadata` key is stored and read back under `metadata` (never
// under the v1 `annotations` key, and never dropped). A dropped or mis-named map
// is exactly what makes `terraform plan` report perpetual drift.
func TestV1Beta1MetadataRoundTrips(t *testing.T) {
	ts := newBetaServer(t)
	const base = "/v1beta1/projects/p/locations/us-central1/namespaces"

	doJSON(t, ts, http.MethodPost, base+"?namespaceId=ns", map[string]any{}, nil)

	// Service created with a v1beta1 `metadata` block.
	var svc map[string]any
	doJSON(t, ts, http.MethodPost, base+"/ns/services?serviceId=svc",
		map[string]any{"metadata": map[string]string{"team": "core"}}, &svc)
	assertMetadata(t, "service create", svc, "team", "core")

	// Read back on /v1beta1 must expose `metadata`, not `annotations`.
	var gotSvc map[string]any
	doJSON(t, ts, http.MethodGet, base+"/ns/services/svc", nil, &gotSvc)
	assertMetadata(t, "service get", gotSvc, "team", "core")

	// A /v1beta1 PATCH with updateMask=metadata must apply and read back clean.
	var patched map[string]any
	doJSON(t, ts, http.MethodPatch, base+"/ns/services/svc?updateMask=metadata",
		map[string]any{"metadata": map[string]string{"team": "platform"}}, &patched)
	assertMetadata(t, "service patch", patched, "team", "platform")

	// Endpoint with address/port/metadata under /v1beta1.
	var ep map[string]any
	doJSON(t, ts, http.MethodPost, base+"/ns/services/svc/endpoints?endpointId=ep",
		map[string]any{"address": "10.0.0.1", "port": 8080, "metadata": map[string]string{"proto": "grpc"}}, &ep)
	assertMetadata(t, "endpoint create", ep, "proto", "grpc")

	var gotEp map[string]any
	doJSON(t, ts, http.MethodGet, base+"/ns/services/svc/endpoints/ep", nil, &gotEp)
	assertMetadata(t, "endpoint get", gotEp, "proto", "grpc")
}

// assertMetadata asserts the resource JSON carries the v1beta1 `metadata` key
// with the expected entry and does NOT leak the v1 `annotations` key.
func assertMetadata(t *testing.T, where string, m map[string]any, key, want string) {
	t.Helper()

	if _, leaked := m["annotations"]; leaked {
		t.Fatalf("%s: v1beta1 response must not carry `annotations`, got %v", where, m)
	}

	meta, ok := m["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("%s: expected `metadata` map, got %v", where, m)
	}

	if got, _ := meta[key].(string); got != want {
		t.Fatalf("%s: metadata[%q] = %q, want %q", where, key, got, want)
	}
}
