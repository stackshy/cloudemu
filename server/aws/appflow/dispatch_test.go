package appflow_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/appflow"
)

// TestMatches verifies the handler claims exactly the AppFlow operation paths
// and ARN-scoped /tags paths, and never the S3 catch-all territory.
func TestMatches(t *testing.T) {
	h := appflow.New(nil)

	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/create-flow", true},
		{http.MethodPost, "/describe-flow", true},
		{http.MethodPost, "/update-flow", true},
		{http.MethodPost, "/delete-flow", true},
		{http.MethodPost, "/list-flows", true},
		{http.MethodPost, "/start-flow", true},
		{http.MethodPost, "/stop-flow", true},
		{http.MethodPost, "/create-connector-profile", true},
		{http.MethodPost, "/describe-connector-profiles", true},
		{http.MethodPost, "/delete-connector-profile", true},
		{http.MethodGet, "/tags/arn:aws:appflow:us-east-1:1:flow%2Fmy-flow", true},     // ListTagsForResource
		{http.MethodPost, "/tags/arn:aws:appflow:us-east-1:1:flow%2Fmy-flow", true},    // TagResource
		{http.MethodGet, "/tags/arn:aws:vpc-lattice:us-east-1:1:service%2Fsvc", false}, // other-service ARN
		{http.MethodGet, "/tags", false},                                               // S3 ListObjects on bucket "tags"
		{http.MethodPost, "/create-flow/extra", false},                                 // deeper path is not an op
		{http.MethodGet, "/create-flow", false},                                        // GET on bucket "create-flow": ops are POST-only
		{http.MethodPost, "/some-bucket", false},                                       // arbitrary bucket op
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := h.Matches(r); got != tc.want {
			t.Errorf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
