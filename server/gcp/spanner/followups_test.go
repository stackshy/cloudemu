package spanner

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSharedListYieldsMaxResults: with Cloud SQL mounted, an instance list
// paged with Cloud SQL's maxResults is Cloud SQL's even when Spanner owns an
// instance in the project.
func TestSharedListYieldsMaxResults(t *testing.T) {
	h := newHandlerWithInstance(t)
	h.SetSharedPath()

	cases := []struct {
		name, path string
		want       bool
	}{
		{"unpaged", "/v1/projects/p/instances", true},
		{"spanner paged", "/v1/projects/p/instances?pageSize=5", true},
		{"cloud sql paged", "/v1/projects/p/instances?maxResults=5", false},
	}

	for _, tc := range cases {
		if got := h.Matches(req(http.MethodGet, tc.path, "")); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestBackupsListThenDelete: Terraform's force_destroy lists an instance's
// backups (none here) and then deletes the instance, which must then be gone.
func TestBackupsListThenDelete(t *testing.T) {
	h := newHandlerWithInstance(t)

	steps := []struct {
		method, path string
		want         int
		body         string
	}{
		{http.MethodGet, "/v1/projects/p/instances/known/backups", http.StatusOK, "{}\n"},
		{http.MethodGet, "/v1/projects/p/instances/known/backups?filter=state%3AREADY", http.StatusOK, "{}\n"},
		{http.MethodPost, "/v1/projects/p/instances/known/backups", http.StatusMethodNotAllowed, ""},
		{http.MethodDelete, "/v1/projects/p/instances/known", http.StatusOK, ""},
		{http.MethodGet, "/v1/projects/p/instances/known", http.StatusNotFound, ""},
		{http.MethodGet, "/v1/projects/p/instances/known/backups", http.StatusNotFound, ""},
	}

	for _, s := range steps {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req(s.method, s.path, ""))

		if rec.Code != s.want {
			t.Fatalf("%s %s: %d %s, want %d", s.method, s.path, rec.Code, rec.Body.String(), s.want)
		}

		if s.body != "" && rec.Body.String() != s.body {
			t.Errorf("%s %s: body %q, want %q", s.method, s.path, rec.Body.String(), s.body)
		}
	}
}
