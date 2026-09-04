package tablestorage_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestBatchParseErrorHasNoInternalPrefix guards the $batch parse-failure path
// (batch.go): a malformed batch must surface the plain OData error message, not
// the internal cerrors code-taxonomy prefix ("InvalidArgument:"). aztables and
// other clients read the message verbatim, so a leaked prefix is a real
// divergence from Azure Table storage.
func TestBatchParseErrorHasNoInternalPrefix(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{TableStorage: cloudP.TableStorage})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// multipart/mixed without a boundary -> parseBatch returns a *cerrors.Error
	// ("missing multipart boundary"), which flows through writeError at batch.go:44.
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/$batch", strings.NewReader("irrelevant body"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "multipart/mixed")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	got := string(body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, got)
	}
	for _, prefix := range []string{"InvalidArgument:", "NotFound:", "FailedPrecondition:", "AlreadyExists:"} {
		if strings.Contains(got, prefix) {
			t.Fatalf("batch parse error leaked internal prefix %q: %s", prefix, got)
		}
	}
	// Sanity: the real message content still reaches the client.
	if !strings.Contains(got, "boundary") {
		t.Fatalf("expected the boundary error message in the response, got: %s", got)
	}
}
