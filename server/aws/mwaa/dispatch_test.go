package mwaa_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/mwaa"
)

// TestMatches verifies the handler claims exactly the MWAA path shapes and
// ARN-scoped /tags paths, and never a sibling service's tag ARN.
func TestMatches(t *testing.T) {
	h := mwaa.New(nil)

	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/environments", true},                                           // ListEnvironments
		{http.MethodPut, "/environments/my-env", true},                                    // CreateEnvironment
		{http.MethodGet, "/environments/my-env", true},                                    // GetEnvironment
		{http.MethodPatch, "/environments/my-env", true},                                  // UpdateEnvironment
		{http.MethodDelete, "/environments/my-env", true},                                 // DeleteEnvironment
		{http.MethodPost, "/clitoken/my-env", true},                                       // CreateCliToken
		{http.MethodPost, "/webtoken/my-env", true},                                       // CreateWebLoginToken
		{http.MethodGet, "/tags/arn:aws:airflow:us-east-1:1:environment%2Fmy-env", true},  // ListTagsForResource
		{http.MethodPost, "/tags/arn:aws:airflow:us-east-1:1:environment%2Fmy-env", true}, // TagResource
		{http.MethodGet, "/tags/arn:aws:appflow:us-east-1:1:flow%2Fmy-flow", false},       // sibling AppFlow ARN
		{http.MethodGet, "/clitoken", false},                                              // missing name segment
		{http.MethodGet, "/clitoken/my-env", false},                                       // token ops are POST-only
		{http.MethodGet, "/some-bucket", false},                                           // arbitrary bucket op
		{http.MethodGet, "/environments/my-env/extra", false},                             // deeper path is not an op
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := h.Matches(r); got != tc.want {
			t.Errorf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
