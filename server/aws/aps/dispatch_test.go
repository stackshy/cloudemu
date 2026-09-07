package aps_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/aps"
)

// TestMatches verifies the handler claims exactly the APS path shapes and
// ARN-scoped /tags paths, and never a sibling service's tag ARN.
func TestMatches(t *testing.T) {
	h := aps.New(nil)

	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/workspaces", true},                                // ListWorkspaces
		{http.MethodPost, "/workspaces", true},                               // CreateWorkspace
		{http.MethodGet, "/workspaces/ws-abc", true},                         // DescribeWorkspace
		{http.MethodDelete, "/workspaces/ws-abc", true},                      // DeleteWorkspace
		{http.MethodPost, "/workspaces/ws-abc/alias", true},                  // UpdateWorkspaceAlias
		{http.MethodPost, "/workspaces/ws-abc/logging", true},                // CreateLoggingConfiguration
		{http.MethodGet, "/workspaces/ws-abc/rulegroupsnamespaces", true},    // ListRuleGroupsNamespaces
		{http.MethodPut, "/workspaces/ws-abc/rulegroupsnamespaces/r1", true}, // PutRuleGroupsNamespace
		{http.MethodGet, "/workspaces/ws-abc/alertmanager/definition", true}, // DescribeAlertManagerDefinition
		{http.MethodGet, "/tags/arn:aws:aps:us-east-1:1:workspace%2Fws-abc", true},
		{http.MethodPost, "/tags/arn:aws:aps:us-east-1:1:workspace%2Fws-abc", true},
		{http.MethodGet, "/tags/arn:aws:airflow:us-east-1:1:environment%2Fe", false}, // sibling MWAA ARN
		{http.MethodGet, "/tags/arn:aws:appflow:us-east-1:1:flow%2Ff", false},        // sibling AppFlow ARN
		{http.MethodGet, "/workspaces/ws-abc/alertmanager", false},                   // incomplete alertmanager shape
		{http.MethodGet, "/workspaces/ws-abc/unknown", false},                        // unknown sub-resource
		{http.MethodGet, "/workspaces/ws-abc/rulegroupsnamespaces/r1/extra", false},  // too deep
		{http.MethodGet, "/some-bucket", false},                                      // arbitrary bucket op
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := h.Matches(r); got != tc.want {
			t.Errorf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
