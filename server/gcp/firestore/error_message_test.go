package firestore_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestFirestoreRunQueryInvalidFilterOmitsCodePrefix guards against the wire
// error message for an unsupported structured-query filter operator leaking
// cloudemu's internal cerrors code name (e.g. "InvalidArgument: ...") into the
// message a Firestore client sees. Real Firestore never prefixes its error
// messages with an internal error-taxonomy name.
func TestFirestoreRunQueryInvalidFilterOmitsCodePrefix(t *testing.T) {
	cloudP := cloudemu.NewGCP()

	srv := gcpserver.New(gcpserver.Drivers{Firestore: cloudP.Firestore})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	body := `{
		"structuredQuery": {
			"from": [{"collectionId": "users"}],
			"where": {
				"fieldFilter": {
					"field": {"fieldPath": "age"},
					"op": "BOGUS_OP",
					"value": {"integerValue": "1"}
				}
			}
		}
	}`

	url := ts.URL + "/v1/projects/" + testProject + "/databases/(default)/documents:runQuery"

	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST runQuery: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	assertNoCodePrefix(t, out.Error.Message)
}

// TestFirestoreAggregationQueryInvalidFilterOmitsCodePrefix is the
// runAggregationQuery counterpart: aggregationMatches wraps buildFilterNode's
// error into a NEW cerrors.Error before returning it to writeErr, so the fix
// there is baking the filter error's already-clean Message into the wrapper's
// Message field, not calling ferr.Error() into it.
func TestFirestoreAggregationQueryInvalidFilterOmitsCodePrefix(t *testing.T) {
	cloudP := cloudemu.NewGCP()

	srv := gcpserver.New(gcpserver.Drivers{Firestore: cloudP.Firestore})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	body := `{
		"structuredAggregationQuery": {
			"structuredQuery": {
				"from": [{"collectionId": "users"}],
				"where": {
					"fieldFilter": {
						"field": {"fieldPath": "age"},
						"op": "BOGUS_OP",
						"value": {"integerValue": "1"}
					}
				}
			},
			"aggregations": [{"alias": "count", "count": {}}]
		}
	}`

	url := ts.URL + "/v1/projects/" + testProject + "/databases/(default)/documents:runAggregationQuery"

	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST runAggregationQuery: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	assertNoCodePrefix(t, out.Error.Message)
}

// assertNoCodePrefix fails if msg contains one of cloudemu's internal
// canonical error-code names followed by a colon — the shape err.Error()
// produces for a *cerrors.Error, as opposed to cerrors.Message(err).
func assertNoCodePrefix(t *testing.T, msg string) {
	t.Helper()

	for _, prefix := range []string{"NotFound:", "AlreadyExists:", "InvalidArgument:", "FailedPrecondition:", "Internal:"} {
		if strings.Contains(msg, prefix) {
			t.Errorf("wire error message %q leaks internal code prefix %q", msg, prefix)
		}
	}
}
