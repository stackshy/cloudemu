package fis_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	fissrv "github.com/stackshy/cloudemu/v2/server/aws/fis"
)

func newHandler() *fissrv.Handler {
	return fissrv.New(cloudemu.NewAWS().FIS)
}

func TestMatches(t *testing.T) {
	h := newHandler()

	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/experimentTemplates", true},
		{http.MethodGet, "/experimentTemplates", true},
		{http.MethodGet, "/experimentTemplates/EXT123", true},
		{http.MethodPatch, "/experimentTemplates/EXT123", true},
		{http.MethodDelete, "/experimentTemplates/EXT123", true},
		{http.MethodPost, "/experiments", true},
		{http.MethodGet, "/experiments/EXP123", true},
		{http.MethodDelete, "/experiments/EXP123", true},
		{http.MethodPost, "/tags/arn:aws:fis:us-east-1:123456789012:experiment-template/EXT123", true},
		{http.MethodGet, "/tags/arn:aws:fis:us-east-1:123456789012:experiment/EXP123", true},
		// A non-FIS ARN on the shared /tags path falls through.
		{http.MethodGet, "/tags/arn:aws:sns:us-east-1:123456789012:topic", false},
		// Unrelated paths are not claimed.
		{http.MethodGet, "/example-bucket/key", false},
		{http.MethodGet, "/experimentTemplates/EXT123/targetAccountConfigurations/1", false},
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

	r := httptest.NewRequest(http.MethodGet, "/experimentTemplates/EXT1/unknown/deep", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := newHandler()

	r := httptest.NewRequest(http.MethodPut, "/experimentTemplates/EXT1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}
