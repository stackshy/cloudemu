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
		name       string
		handler    http.HandlerFunc
		wantStatus int
		wantCT     string
	}{
		{
			name: "handler content type kept",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"name":"<script>x</script>"}`))
			},
			wantStatus: http.StatusOK,
			wantCT:     "application/json",
		},
		{
			name: "write without header gets default",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("<html><script>x</script></html>"))
			},
			wantStatus: http.StatusOK,
			wantCT:     "application/octet-stream",
		},
		{
			name: "explicit status without content type gets default",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte("<script>x</script>"))
			},
			wantStatus: http.StatusCreated,
			wantCT:     "application/octet-stream",
		},
		{
			name: "text content type kept",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = w.Write([]byte("<script>x</script>"))
			},
			wantStatus: http.StatusOK,
			wantCT:     "text/plain; charset=utf-8",
		},
		{
			name: "first status wins",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				w.WriteHeader(http.StatusTeapot)
			},
			wantStatus: http.StatusAccepted,
			wantCT:     "application/octet-stream",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := vcr.New(vcr.Options{Mode: vcr.ModeRecord, CassettePath: filepath.Join(t.TempDir(), "c.json")})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			resp := doRequest(t, v.Wrap(tc.handler, "aws"), http.MethodGet, "/x", "")
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
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

// TestRecordKeepsBodyAndStatus checks that the recorder forwards and records
// the handler's bytes unchanged, across several writes, with its status.
func TestRecordKeepsBodyAndStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")

	v, err := vcr.New(vcr.Options{Mode: vcr.ModeRecord, CassettePath: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"a":"<b>",`))
		_, _ = w.Write([]byte(`"c":1}`))
	})

	resp := doRequest(t, v.Wrap(h, "azure"), http.MethodPut, "/x", "")

	const want = `{"a":"<b>","c":1}`
	if got := readBody(t, resp); got != want || resp.StatusCode != http.StatusCreated {
		t.Fatalf("live response = %d %q, want 201 %q", resp.StatusCode, got, want)
	}

	if err := v.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	c, err := vcr.LoadCassette(path)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	if len(c.Interactions) != 1 {
		t.Fatalf("interactions = %d, want 1", len(c.Interactions))
	}

	got := c.Interactions[0].Response
	if string(got.Body) != want || got.Status != http.StatusCreated {
		t.Fatalf("recorded = %d %q, want 201 %q", got.Status, got.Body, want)
	}
}
