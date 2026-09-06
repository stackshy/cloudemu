package batch_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newBatchServer returns a live httptest server backed by a fresh Batch mock.
func newBatchServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{Batch: cloud.Batch}))
	t.Cleanup(ts.Close)

	return ts
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()

	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}

	return resp
}

// TestClientExceptionShape verifies the restJson1 error envelope: HTTP 400,
// X-Amzn-Errortype: ClientException, a {"message":...} body, and no cerrors code
// prefix leaking into the message.
func TestClientExceptionShape(t *testing.T) {
	ts := newBatchServer(t)

	// Missing computeEnvironmentName -> ClientException.
	resp := post(t, ts.URL+"/v1/createcomputeenvironment", `{"type":"MANAGED","computeResources":{"type":"FARGATE","maxvCpus":4}}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}

	if got := resp.Header.Get("X-Amzn-Errortype"); got != "ClientException" {
		t.Fatalf("want X-Amzn-Errortype ClientException, got %q", got)
	}

	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	if body.Message == "" {
		t.Fatalf("empty error message")
	}

	if strings.Contains(body.Message, "InvalidArgument") || strings.Contains(body.Message, "NotFound") {
		t.Fatalf("cerrors code prefix leaked into message: %q", body.Message)
	}
}

// TestUnknownPathFallsThrough confirms Matches does not claim a non-Batch /v1/
// path, so it never shadows other handlers or the S3 catch-all.
func TestUnknownPathFallsThrough(t *testing.T) {
	ts := newBatchServer(t)

	resp := post(t, ts.URL+"/v1/notabatchop", `{}`)
	defer resp.Body.Close()

	// The Batch handler must not have answered with its ClientException envelope.
	if resp.Header.Get("X-Amzn-Errortype") == "ClientException" &&
		resp.StatusCode == http.StatusBadRequest {
		t.Fatalf("Batch handler claimed a non-Batch /v1/ path")
	}
}
