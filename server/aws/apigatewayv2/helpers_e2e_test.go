package apigatewayv2_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// wireErr is the decoded shape of an apigatewayv2 error response.
type wireErr struct {
	status  int
	errType string
	message string
}

// doErr issues a request that is expected to fail and returns its status,
// X-Amzn-Errortype header and message.
func doErr(t *testing.T, method, url, body string) wireErr {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var out struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &out)

	return wireErr{status: resp.StatusCode, errType: resp.Header.Get("X-Amzn-Errortype"), message: out.Message}
}

// wantErr asserts a request fails with the given status, error type and message.
func wantErr(t *testing.T, method, url, body string, status int, errType, message string) {
	t.Helper()

	got := doErr(t, method, url, body)
	if got.status != status || got.errType != errType || got.message != message {
		t.Fatalf("%s %s = %d %s %q, want %d %s %q", method, url,
			got.status, got.errType, got.message, status, errType, message)
	}
}

// mustDo issues a request and fails the test unless it returns wantStatus.
func mustDo(t *testing.T, method, url, body string, wantStatus int) map[string]any {
	t.Helper()

	status, out := do(t, method, url, body)
	if status != wantStatus {
		t.Fatalf("%s %s = %d, want %d: %v", method, url, status, wantStatus, out)
	}

	return out
}

// newAPI creates an API of the given protocol and returns its id.
func newAPI(t *testing.T, base, body string) string {
	t.Helper()

	out := mustDo(t, http.MethodPost, base+"/v2/apis", body, http.StatusCreated)

	id, _ := out["apiId"].(string)
	if id == "" {
		t.Fatalf("CreateApi gave no apiId: %v", out)
	}

	return id
}

// newHTTPAPI creates a plain HTTP API and returns its id.
func newHTTPAPI(t *testing.T, base string) string {
	t.Helper()

	return newAPI(t, base, `{"name":"http","protocolType":"HTTP"}`)
}

// newLambdaIntegration adds an AWS_PROXY integration and returns its id.
func newLambdaIntegration(t *testing.T, apiBase string) string {
	t.Helper()

	out := mustDo(t, http.MethodPost, apiBase+"/integrations",
		`{"integrationType":"AWS_PROXY","integrationUri":"arn:aws:lambda:us-east-1:000000000000:function:f",`+
			`"payloadFormatVersion":"2.0"}`, http.StatusCreated)

	id, _ := out["integrationId"].(string)

	return id
}

// items returns the items array of a list response.
func items(out map[string]any) []any {
	its, _ := out["items"].([]any)

	return its
}
