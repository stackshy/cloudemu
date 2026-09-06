package gcp_test

// Dataproc/Compute /regions/ shadow-check.
//
// Dataproc serves /v1/projects/{p}/regions/{r}/{clusters|operations}. Regional
// Compute (subnetworks, addresses, the bare regions.get) also uses a /regions/{r}/
// segment, but under the separate /compute/v1/ prefix, so the two must never
// collide. This drives the FULL production server and asserts:
//   - a Dataproc regional clusters path is claimed by Dataproc (200 list, not a
//     Firestore catch-all 404); and
//   - a Compute regional path still routes to Compute, unaffected by Dataproc's
//     registration.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func TestDataprocComputeRegionsShadow(t *testing.T) {
	srv := gcpserver.NewFromProvider(cloudemu.NewGCP())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cases := []struct {
		name        string
		path        string
		wantStatus  int
		mustNotHave string // substring that would betray the wrong handler claimed it
	}{
		{
			name:       "dataproc claims regional clusters",
			path:       "/v1/projects/demo/regions/us-central1/clusters",
			wantStatus: http.StatusOK,
			// A Firestore catch-all miss answers 404 NOT_FOUND; Dataproc answers 200.
			mustNotHave: "NOT_FOUND",
		},
		{
			name:       "compute still claims regional subnetworks",
			path:       "/compute/v1/projects/demo/regions/us-central1/subnetworks",
			wantStatus: http.StatusOK,
			// Dataproc's error reason would appear if it wrongly claimed this.
			mustNotHave: "unrecognized Dataproc path",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+tc.path, nil) //nolint:noctx // short-lived test request

			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()

			raw, _ := io.ReadAll(resp.Body)
			body := string(raw)

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("GET %s = %d, want %d (body %s)", tc.path, resp.StatusCode, tc.wantStatus, body)
			}

			if strings.Contains(body, tc.mustNotHave) {
				t.Fatalf("GET %s served by wrong handler: body %s", tc.path, body)
			}
		})
	}
}
