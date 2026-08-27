package gcp_test

// Dispatch-ordering regression tests (Architecture Theme 2, #590).
//
// server.Server is a first-match-wins dispatcher. In server/gcp/gcp.go New(),
// the Firestore handler is a catch-all: its Matches() claims every path under
// /v1/projects/. Handlers that live in that same URL space but own a distinct
// resource-type segment (Cloud Functions .../functions, Pub/Sub .../topics,
// IAM .../serviceAccounts, Secret Manager .../secrets, …) are each documented
// as "register before Firestore's catch-all" so first-match-wins keeps them on
// the correct package.
//
// These tests drive the FULL production server (NewFromProvider) with those
// ambiguous /v1/projects/ paths and assert the specific handler served (HTTP
// 200 plus the service's list marker). If Firestore caught them it would fail
// to parse the path and answer 404 NOT_FOUND — so moving Firestore earlier (or
// alphabetizing registrations) makes these tests fail.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func fullGCPServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := gcpserver.NewFromProvider(cloudemu.NewGCP())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

// TestSpecificHandlersWinBeforeFirestore asserts the /v1/projects/ handlers with
// a distinct resource-type segment win over the Firestore catch-all.
func TestSpecificHandlersWinBeforeFirestore(t *testing.T) {
	ts := fullGCPServer(t)

	cases := []struct {
		name   string
		path   string
		marker string // substring only the specific handler's 200 response contains
	}{
		{"pubsub_topics_before_firestore", "/v1/projects/demo/topics", "topics"},
		{"cloudfunctions_before_firestore", "/v1/projects/demo/locations/us-central1/functions", "functions"},
		{"iam_serviceaccounts_before_firestore", "/v1/projects/demo/serviceAccounts", "accounts"},
		{"secretmanager_before_firestore", "/v1/projects/demo/secrets", "secrets"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, ts.URL+tc.path, nil) //nolint:noctx // short-lived test request
			if err != nil {
				t.Fatalf("new request: %v", err)
			}

			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}

			defer resp.Body.Close()

			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			body := string(raw)

			// Firestore's catch-all cannot parse these paths and answers
			// 404 NOT_FOUND; a 200 proves the specific handler served.
			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s = %d (want 200); the specific handler must register before Firestore's catch-all. body=%s",
					tc.path, resp.StatusCode, body)
			}

			if !strings.Contains(body, tc.marker) {
				t.Errorf("GET %s response missing marker %q (proves the specific handler, not Firestore, served); body=%s",
					tc.path, tc.marker, body)
			}

			if strings.Contains(body, "NOT_FOUND") {
				t.Errorf("GET %s was answered by the Firestore catch-all (NOT_FOUND) — registration order is broken", tc.path)
			}
		})
	}
}
