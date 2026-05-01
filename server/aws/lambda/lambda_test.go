package lambda_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu"
	awsprov "github.com/stackshy/cloudemu/providers/aws"
	"github.com/stackshy/cloudemu/server/aws/lambda"
	sdrv "github.com/stackshy/cloudemu/serverless/driver"
)

func newServer(t *testing.T) (*httptest.Server, *awsprov.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := httptest.NewServer(lambda.New(cloud.Lambda))

	t.Cleanup(srv.Close)

	return srv, cloud
}

func TestMatchesPathPrefix(t *testing.T) {
	h := lambda.New(nil)

	want := []string{
		"/2015-03-31/functions",
		"/2015-03-31/functions/foo",
		"/2015-03-31/functions/foo/invocations",
	}

	for _, p := range want {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		if !h.Matches(req) {
			t.Fatalf("Matches(%q) = false, want true", p)
		}
	}
}

func TestMatchesRejectsUnrelatedPaths(t *testing.T) {
	h := lambda.New(nil)

	skip := []string{"/", "/bucket/key", "/2020-08-31/functions"}

	for _, p := range skip {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		if h.Matches(req) {
			t.Fatalf("Matches(%q) = true, want false", p)
		}
	}
}

func TestCreateAndGetFunction(t *testing.T) {
	srv, _ := newServer(t)

	body := `{"FunctionName":"hello","Runtime":"go1.x","Handler":"main","MemorySize":128,"Timeout":30}`

	resp := postJSON(t, srv.URL+"/2015-03-31/functions", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}

	getResp, err := http.Get(srv.URL + "/2015-03-31/functions/hello")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", getResp.StatusCode)
	}

	var got struct {
		Configuration functionShape `json:"Configuration"`
	}

	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Configuration.FunctionName != "hello" {
		t.Fatalf("FunctionName = %q, want hello", got.Configuration.FunctionName)
	}

	if got.Configuration.Runtime != "go1.x" {
		t.Fatalf("Runtime = %q, want go1.x", got.Configuration.Runtime)
	}

	if !strings.Contains(got.Configuration.FunctionArn, ":function:hello") {
		t.Fatalf("FunctionArn = %q, want contains :function:hello", got.Configuration.FunctionArn)
	}
}

func TestGetMissingFunctionReturns404(t *testing.T) {
	srv, _ := newServer(t)

	resp, err := http.Get(srv.URL + "/2015-03-31/functions/missing")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	if got := resp.Header.Get("X-Amzn-Errortype"); got != "ResourceNotFoundException" {
		t.Fatalf("errortype = %q, want ResourceNotFoundException", got)
	}
}

func TestCreateDuplicateReturnsConflict(t *testing.T) {
	srv, _ := newServer(t)

	body := `{"FunctionName":"dup","Runtime":"python3.11","Handler":"app.handler"}`

	first := postJSON(t, srv.URL+"/2015-03-31/functions", body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create = %d, want 201", first.StatusCode)
	}

	second := postJSON(t, srv.URL+"/2015-03-31/functions", body)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second create = %d, want 409", second.StatusCode)
	}
}

func TestListFunctions(t *testing.T) {
	srv, _ := newServer(t)

	for _, name := range []string{"a", "b", "c"} {
		body := `{"FunctionName":"` + name + `","Runtime":"go1.x"}`

		resp := postJSON(t, srv.URL+"/2015-03-31/functions", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: %d", name, resp.StatusCode)
		}
	}

	resp, err := http.Get(srv.URL + "/2015-03-31/functions")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	defer resp.Body.Close()

	var got struct {
		Functions []functionShape `json:"Functions"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.Functions) != 3 {
		t.Fatalf("Functions count = %d, want 3", len(got.Functions))
	}
}

func TestDeleteFunction(t *testing.T) {
	srv, _ := newServer(t)

	body := `{"FunctionName":"goner","Runtime":"go1.x"}`
	if r := postJSON(t, srv.URL+"/2015-03-31/functions", body); r.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", r.StatusCode)
	}

	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodDelete, srv.URL+"/2015-03-31/functions/goner", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", resp.StatusCode)
	}

	gone, err := http.Get(srv.URL + "/2015-03-31/functions/goner")
	if err != nil {
		t.Fatalf("post-delete get: %v", err)
	}

	defer gone.Body.Close()

	if gone.StatusCode != http.StatusNotFound {
		t.Fatalf("post-delete get = %d, want 404", gone.StatusCode)
	}
}

func TestInvokeReturnsHandlerPayload(t *testing.T) {
	srv, cloud := newServer(t)

	body := `{"FunctionName":"echo","Runtime":"go1.x"}`
	if r := postJSON(t, srv.URL+"/2015-03-31/functions", body); r.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", r.StatusCode)
	}

	cloud.Lambda.RegisterHandler("echo", func(_ context.Context, payload []byte) ([]byte, error) {
		return append([]byte(`{"echo":`), append(payload, '}')...), nil
	})

	resp, err := http.Post(srv.URL+"/2015-03-31/functions/echo/invocations",
		"application/json", bytes.NewReader([]byte(`"hi"`)))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got, _ := io.ReadAll(resp.Body)
	if string(got) != `{"echo":"hi"}` {
		t.Fatalf("body = %q, want {\"echo\":\"hi\"}", string(got))
	}

	if resp.Header.Get("X-Amz-Function-Error") != "" {
		t.Fatalf("unexpected X-Amz-Function-Error header on success")
	}
}

func TestInvokeMissingHandlerSignalsError(t *testing.T) {
	srv, _ := newServer(t)

	if r := postJSON(t, srv.URL+"/2015-03-31/functions",
		`{"FunctionName":"nohandler","Runtime":"go1.x"}`); r.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", r.StatusCode)
	}

	resp, err := http.Post(srv.URL+"/2015-03-31/functions/nohandler/invocations",
		"application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	defer resp.Body.Close()

	if resp.Header.Get("X-Amz-Function-Error") == "" {
		t.Fatal("expected X-Amz-Function-Error on no-handler invoke")
	}
}

func TestInvokeOnMissingFunctionReturns404(t *testing.T) {
	srv, _ := newServer(t)

	resp, err := http.Post(srv.URL+"/2015-03-31/functions/missing/invocations",
		"application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestEnvironmentRoundTrip(t *testing.T) {
	srv, _ := newServer(t)

	body := `{"FunctionName":"envfn","Runtime":"go1.x","Environment":{"Variables":{"K":"V"}}}`
	if r := postJSON(t, srv.URL+"/2015-03-31/functions", body); r.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", r.StatusCode)
	}

	getResp, err := http.Get(srv.URL + "/2015-03-31/functions/envfn")
	if err != nil {
		t.Fatalf("get envfn: %v", err)
	}

	defer getResp.Body.Close()

	var got struct {
		Configuration functionShape `json:"Configuration"`
	}

	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Configuration.Environment == nil ||
		got.Configuration.Environment.Variables["K"] != "V" {
		t.Fatalf("environment not preserved: %+v", got.Configuration.Environment)
	}
}

// TestDriverNilDoesNotPanic guards against a regression where the handler
// might dereference a nil driver during routing.
func TestDriverNilDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()

	_ = lambda.New(stubDriver{})
}

type stubDriver struct{ sdrv.Serverless }

// Helpers --------------------------------------------------------------------

type envShape struct {
	Variables map[string]string `json:"Variables"`
}

type functionShape struct {
	FunctionName string    `json:"FunctionName"`
	FunctionArn  string    `json:"FunctionArn"`
	Runtime      string    `json:"Runtime"`
	Handler      string    `json:"Handler"`
	Environment  *envShape `json:"Environment"`
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()

	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}

	return resp
}
