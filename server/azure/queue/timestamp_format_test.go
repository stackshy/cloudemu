package queue_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestQueueTimestampsUseGMTSuffix asserts the RFC1123 timestamps in the Queue
// Storage wire responses carry the "GMT" zone suffix real Azure emits, not Go's
// default "UTC" suffix from time.RFC1123. Strict non-Go SDK date parsers (Python,
// .NET, Java) reject "UTC" and require "GMT"; the Blob handler already uses the
// GMT-only http.TimeFormat, so the Queue handler must match.
func TestQueueTimestampsUseGMTSuffix(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{QueueStorage: cloudP.QueueStorage})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// Create queue.
	put(t, ts, http.MethodPut, "/q1", "")

	// Enqueue: the response XML carries InsertionTime/ExpirationTime/TimeNextVisible.
	enqBody := put(t, ts, http.MethodPost, "/q1/messages",
		"<QueueMessage><MessageText>aGVsbG8=</MessageText></QueueMessage>")
	assertNoUTCSuffix(t, "enqueue", enqBody)

	// Dequeue: the response XML carries the same timestamp elements.
	deqBody := get(t, ts, "/q1/messages")
	assertNoUTCSuffix(t, "dequeue", deqBody)
}

func assertNoUTCSuffix(t *testing.T, op, body string) {
	t.Helper()

	if strings.Contains(body, " UTC<") {
		t.Errorf("%s response has a Go-style \" UTC\" timestamp suffix (Azure emits \"GMT\"): %s", op, body)
	}

	if !strings.Contains(body, " GMT<") {
		t.Errorf("%s response is missing a \"GMT\" timestamp suffix: %s", op, body)
	}
}

func put(t *testing.T, ts *httptest.Server, method, path, body string) string {
	t.Helper()

	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, _ := io.ReadAll(resp.Body)

	return string(data)
}

func get(t *testing.T, ts *httptest.Server, path string) string {
	t.Helper()

	resp, err := ts.Client().Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, _ := io.ReadAll(resp.Body)

	return string(data)
}
