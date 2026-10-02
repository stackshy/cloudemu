package vcr_test

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stackshy/cloudemu/v2/features/vcr"
)

// TestRecordSetsSafeHeaders checks that a live response passing through the
// recorder carries nosniff and a concrete content type: the handler's own when
// it set one, a non-active default when it did not.
func TestRecordSetsSafeHeaders(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantCT  string
	}{
		{
			name: "handler content type kept",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"name":"<script>x</script>"}`))
			},
			wantCT: "application/json",
		},
		{
			name: "write without header gets default",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("<html><script>x</script></html>"))
			},
			wantCT: "application/octet-stream",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := vcr.New(vcr.Options{Mode: vcr.ModeRecord, CassettePath: filepath.Join(t.TempDir(), "c.json")})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			resp := doRequest(t, v.Wrap(tc.handler, "aws"), http.MethodGet, "/x", "")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}

			if got := resp.Header.Get("Content-Type"); got != tc.wantCT {
				t.Fatalf("Content-Type = %q, want %q", got, tc.wantCT)
			}

			if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
			}
		})
	}
}
