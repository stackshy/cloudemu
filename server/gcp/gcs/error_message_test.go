package gcs_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestGCSErrorMessagesOmitCodePrefix guards writeErr (server/gcp/gcs) against
// baking cloudemu's internal cerrors code name (e.g. "notFound: ...",
// "conflict: ...") into the wire error message an SDK caller sees. Real GCS
// never prefixes its error messages with an internal error-taxonomy name.
func TestGCSErrorMessagesOmitCodePrefix(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Storage: cloudP.GCS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	t.Run("NotFound Get missing bucket", func(t *testing.T) {
		resp, err := ts.Client().Get(ts.URL + "/storage/v1/b/does-not-exist")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status=%d, want 404", resp.StatusCode)
		}

		assertGCSErrorHasNoCodePrefix(t, resp)
	})

	t.Run("AlreadyExists duplicate bucket create", func(t *testing.T) {
		createURL := ts.URL + "/storage/v1/b?" + url.Values{"project": {"p1"}}.Encode()

		body := strings.NewReader(`{"name": "errmsg-dupe"}`)
		if resp, err := ts.Client().Post(createURL, "application/json", body); err != nil {
			t.Fatal(err)
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("first create status=%d, want 200", resp.StatusCode)
			}
		}

		resp, err := ts.Client().Post(createURL, "application/json", strings.NewReader(`{"name": "errmsg-dupe"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusConflict {
			t.Errorf("status=%d, want 409", resp.StatusCode)
		}

		assertGCSErrorHasNoCodePrefix(t, resp)
	})
}

// assertGCSErrorHasNoCodePrefix decodes a GCS JSON error envelope and fails if
// its message contains one of cloudemu's internal canonical error-code names
// followed by a colon — the shape err.Error() produces for a *cerrors.Error,
// as opposed to cerrors.Message(err).
func assertGCSErrorHasNoCodePrefix(t *testing.T, resp *http.Response) {
	t.Helper()

	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}

	for _, prefix := range []string{"NotFound:", "AlreadyExists:", "InvalidArgument:", "FailedPrecondition:", "Internal:"} {
		if strings.Contains(out.Error.Message, prefix) {
			t.Errorf("wire error message %q leaks internal code prefix %q", out.Error.Message, prefix)
		}
	}
}
