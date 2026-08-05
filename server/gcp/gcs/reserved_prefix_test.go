package gcs

import "testing"

// TestIsReservedAPIPrefix guards the review fix: API version/service segments
// are reserved (so API paths aren't misrouted to GCS as bucket lookups), but a
// real bucket that merely starts with "v"+digit (e.g. "v2-assets") is not.
func TestIsReservedAPIPrefix(t *testing.T) {
	reserved := []string{"v1", "v3", "v1beta4", "v2beta", "sql", "compute", "dns", "upload"}
	for _, s := range reserved {
		if !isReservedAPIPrefix(s) {
			t.Errorf("isReservedAPIPrefix(%q) = false, want true", s)
		}
	}

	buckets := []string{"v2-assets", "v1data", "my-bucket", "vault", "video", "v"}
	for _, s := range buckets {
		if isReservedAPIPrefix(s) {
			t.Errorf("isReservedAPIPrefix(%q) = true, want false (real bucket name)", s)
		}
	}
}
