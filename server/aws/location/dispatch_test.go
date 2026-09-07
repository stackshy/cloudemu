package location_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	locsrv "github.com/stackshy/cloudemu/v2/server/aws/location"
)

func newHandler() *locsrv.Handler {
	return locsrv.New(cloudemu.NewAWS().Location)
}

func TestMatches(t *testing.T) {
	h := newHandler()

	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/maps/v0/maps", true},
		{http.MethodGet, "/maps/v0/maps/example", true},
		{http.MethodPost, "/places/v0/indexes", true},
		{http.MethodPost, "/routes/v0/list-calculators", true},
		{http.MethodPost, "/geofencing/v0/collections", true},
		{http.MethodDelete, "/tracking/v0/trackers/t1", true},
		{http.MethodGet, "/tags/arn:aws:geo:us-east-1:123456789012:map/example", true},
		{http.MethodPost, "/tags/arn:aws:geo:us-east-1:123456789012:tracker/t1", true},
		// A non-Location ARN on the shared /tags path falls through.
		{http.MethodGet, "/tags/arn:aws:sns:us-east-1:123456789012:topic", false},
		// Unrelated paths are not claimed.
		{http.MethodPost, "/maps/v1/maps", false},
		{http.MethodGet, "/example-bucket/key", false},
		// A path-style S3 request to a bucket named like a versioned root, with a
		// non-collection third segment, falls through to S3 (not claimed here).
		{http.MethodGet, "/maps/v0/tile.png", false},
		{http.MethodGet, "/maps/v0/v0/tiles/1.png", false},
		{http.MethodGet, "/routes/v0/some-object-key", false},
		{http.MethodGet, "/maps/v0", false},
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

	r := httptest.NewRequest(http.MethodGet, "/maps/v0/unknown", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := newHandler()

	r := httptest.NewRequest(http.MethodPut, "/maps/v0/maps/example", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}
