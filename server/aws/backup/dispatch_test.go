package backup_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	backupsrv "github.com/stackshy/cloudemu/v2/server/aws/backup"
)

func newHandler() *backupsrv.Handler {
	return backupsrv.New(cloudemu.NewAWS().Backup)
}

func TestMatches(t *testing.T) {
	h := newHandler()

	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPut, "/backup-vaults/my-vault", true},
		{http.MethodGet, "/backup-vaults/my-vault", true},
		{http.MethodDelete, "/backup-vaults/my-vault", true},
		{http.MethodGet, "/backup-vaults", true},
		{http.MethodPut, "/backup-vaults/my-vault/vault-lock", true},
		{http.MethodPut, "/backup-vaults/my-vault/access-policy", true},
		{http.MethodPut, "/backup/plans/", true},
		{http.MethodGet, "/backup/plans/PLAN123/", true},
		{http.MethodGet, "/backup/plans/PLAN123/versions/", true},
		{http.MethodPut, "/backup/plans/PLAN123/selections/", true},
		{http.MethodPost, "/tags/arn:aws:backup:us-east-1:123456789012:backup-vault:my-vault", true},
		{http.MethodGet, "/tags/arn:aws:backup:us-east-1:123456789012:backup-plan:PLAN123", true},
		{http.MethodPost, "/untag/arn:aws:backup:us-east-1:123456789012:backup-plan:PLAN123", true},
		// A non-Backup ARN on the shared /tags path falls through to another handler.
		{http.MethodGet, "/tags/arn:aws:sns:us-east-1:123456789012:topic", false},
		// A bare /backup path (no /plans) is not claimed — it belongs to S3.
		{http.MethodGet, "/backup", false},
		{http.MethodGet, "/backup/template/json/toPlan", false},
		// Unrelated S3-style paths are not claimed.
		{http.MethodGet, "/example-bucket/key", false},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := h.Matches(r); got != tc.want {
			t.Errorf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestUnknownPathNotFound(t *testing.T) {
	h := newHandler()

	r := httptest.NewRequest(http.MethodGet, "/backup-vaults/v1/unknown/deep/nest", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := newHandler()

	r := httptest.NewRequest(http.MethodPatch, "/backup-vaults/my-vault", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}
