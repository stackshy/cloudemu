package firestore

import (
	"net/http"
	"testing"
)

// TestAdminMatchesDisambiguation locks the routing contract between the admin
// handler and the greedy data-plane handler: the admin handler claims the
// databases / operations / indexes shapes and defers every .../documents path
// (and colon custom-methods) to the data plane.
func TestAdminMatchesDisambiguation(t *testing.T) {
	admin := NewAdmin()
	data := New(nil)

	cases := []struct {
		method    string
		path      string
		wantAdmin bool
	}{
		// Admin-owned.
		{http.MethodPost, "/v1/projects/p/databases", true},
		{http.MethodGet, "/v1/projects/p/databases", true},
		{http.MethodGet, "/v1/projects/p/databases/db1", true},
		{http.MethodPatch, "/v1/projects/p/databases/db1", true},
		{http.MethodDelete, "/v1/projects/p/databases/db1", true},
		{http.MethodGet, "/v1/projects/p/databases/db1/operations/op-1", true},
		{http.MethodPost, "/v1/projects/p/databases/db1/collectionGroups/cities/indexes", true},
		{http.MethodGet, "/v1/projects/p/databases/db1/collectionGroups/cities/indexes/idx-1", true},

		// Data-plane-owned: admin must defer.
		{http.MethodPost, "/v1/projects/p/databases/db1/documents/cities", false},
		{http.MethodGet, "/v1/projects/p/databases/db1/documents/cities/SF", false},
		{http.MethodPost, "/v1/projects/p/databases/db1/documents:commit", false},
		{http.MethodPost, "/v1/projects/p/databases/db1/documents:runQuery", false},
		{http.MethodPost, "/v1/projects/p/databases/db1:exportDocuments", false},
		// collectionGroups/.../fields is not implemented here -> defer.
		{http.MethodGet, "/v1/projects/p/databases/db1/collectionGroups/cities/fields/foo", false},
	}

	for _, c := range cases {
		r, _ := http.NewRequest(c.method, c.path, nil)

		if got := admin.Matches(r); got != c.wantAdmin {
			t.Errorf("admin.Matches(%s %s)=%v want %v", c.method, c.path, got, c.wantAdmin)
		}

		// The data-plane handler greedily matches every /v1/projects/ path, so
		// registration order (admin first) is what actually separates them.
		if !data.Matches(r) {
			t.Errorf("data-plane.Matches(%s %s)=false; expected greedy match", c.method, c.path)
		}
	}
}
