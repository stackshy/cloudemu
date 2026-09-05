package resourcemanager

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
		// Wrong method / no verb.
		{"get on project", http.MethodGet, "/v1/projects/demo", false},
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
