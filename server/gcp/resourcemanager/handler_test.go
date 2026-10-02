package resourcemanager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGetProject: projects.get reports the project ACTIVE with a number, which
// Terraform's google_service_networking_connection resolves before it builds
// the network name.
func TestGetProject(t *testing.T) {
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/projects/demo", nil))

	body := rec.Body.String()
	for _, want := range []string{`"projectId":"demo"`, `"projectNumber":"123456789012"`, `"lifecycleState":"ACTIVE"`} {
		if rec.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("GET project = %d %s, want 200 containing %s", rec.Code, body, want)
		}
	}
}

func TestMatches(t *testing.T) {
	h := New()

	cases := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"getIamPolicy", http.MethodPost, "/v1/projects/demo:getIamPolicy", true},
		{"setIamPolicy", http.MethodPost, "/v1/projects/demo:setIamPolicy", true},
		{"testIamPermissions", http.MethodPost, "/v1/projects/demo:testIamPermissions", true},
		// Must NOT claim the iam.googleapis.com serviceAccounts/roles surface.
		{"serviceAccounts collection", http.MethodPost, "/v1/projects/demo/serviceAccounts", false},
		{"roles collection", http.MethodGet, "/v1/projects/demo/roles", false},
		{"sa colon verb", http.MethodPost, "/v1/projects/demo/serviceAccounts/x@y:getIamPolicy", false},
		// Must NOT claim Firestore's project document paths.
		{"firestore docs", http.MethodGet, "/v1/projects/demo/databases/(default)/documents", false},
		// projects.get, which Terraform uses to resolve the project number.
		{"get on project", http.MethodGet, "/v1/projects/demo", true},
		{"get with verb", http.MethodGet, "/v1/projects/demo:getIamPolicy", false},
		// Wrong method / no verb.
		{"delete on project", http.MethodDelete, "/v1/projects/demo", false},
		{"unknown verb", http.MethodPost, "/v1/projects/demo:frobnicate", false},
		{"wrong prefix", http.MethodPost, "/v2/projects/demo:getIamPolicy", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if got := h.Matches(r); got != tc.want {
				t.Fatalf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}
