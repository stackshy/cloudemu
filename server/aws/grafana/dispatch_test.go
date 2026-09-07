package grafana_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/grafana"
)

// TestMatches verifies the handler claims exactly the Grafana path shapes and
// ARN-scoped /tags paths, and never a sibling service's tag ARN.
func TestMatches(t *testing.T) {
	h := grafana.New(nil)

	const gArn = "/tags/arn:aws:grafana:us-east-1:1:%2Fworkspaces%2Fg-0123456789"

	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/workspaces", true},                                        // ListWorkspaces
		{http.MethodPost, "/workspaces", true},                                       // CreateWorkspace
		{http.MethodGet, "/workspaces/g-0123456789", true},                           // DescribeWorkspace
		{http.MethodPut, "/workspaces/g-0123456789", true},                           // UpdateWorkspace
		{http.MethodDelete, "/workspaces/g-0123456789", true},                        // DeleteWorkspace
		{http.MethodGet, "/workspaces/g-0123456789/configuration", true},             // DescribeWorkspaceConfiguration
		{http.MethodPut, "/workspaces/g-0123456789/configuration", true},             // UpdateWorkspaceConfiguration
		{http.MethodGet, "/workspaces/g-0123456789/authentication", true},            // DescribeWorkspaceAuthentication
		{http.MethodPost, "/workspaces/g-0123456789/authentication", true},           // UpdateWorkspaceAuthentication
		{http.MethodGet, gArn, true},                                                 // ListTagsForResource
		{http.MethodPost, gArn, true},                                                // TagResource
		{http.MethodGet, "/tags/arn:aws:airflow:us-east-1:1:environment%2Fx", false}, // sibling MWAA ARN
		{http.MethodGet, "/tags/arn:aws:appflow:us-east-1:1:flow%2Fmy-flow", false},  // sibling AppFlow ARN
		{http.MethodPut, "/workspaces", false},                                       // create is POST, not PUT
		{http.MethodGet, "/workspaces/g-0123456789/unknown", false},                  // unknown sub-resource
		{http.MethodGet, "/workspaces/g-0123456789/configuration/extra", false},      // deeper path is not an op
		{http.MethodGet, "/some-bucket", false},                                      // arbitrary bucket op
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := h.Matches(r); got != tc.want {
			t.Errorf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
