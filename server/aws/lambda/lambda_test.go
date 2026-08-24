package lambda_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	awsprov "github.com/stackshy/cloudemu/v2/providers/aws"
	"github.com/stackshy/cloudemu/v2/server/aws/lambda"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
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
		body := `{"FunctionName":"` + name + `","Runtime":"go1.x","Handler":"main"}`

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

	body := `{"FunctionName":"goner","Runtime":"go1.x","Handler":"main"}`
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

	body := `{"FunctionName":"echo","Runtime":"go1.x","Handler":"main"}`
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

// TestInvokeNoHandlerEchoesStub is a regression guard for issue #319: with no
// Go handler registered, invoke used to return a FunctionError ("no handler
// registered"). The emulator can't run an uploaded zip, so it now returns a
// successful stub that echoes the request payload — invoke is testable.
func TestInvokeNoHandlerEchoesStub(t *testing.T) {
	srv, _ := newServer(t)

	if r := postJSON(t, srv.URL+"/2015-03-31/functions",
		`{"FunctionName":"nohandler","Runtime":"go1.x","Handler":"main"}`); r.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", r.StatusCode)
	}

	resp, err := http.Post(srv.URL+"/2015-03-31/functions/nohandler/invocations",
		"application/json", bytes.NewReader([]byte(`{"hi":1}`)))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invoke status = %d, want 200", resp.StatusCode)
	}

	if resp.Header.Get("X-Amz-Function-Error") != "" {
		t.Fatal("no-handler invoke must not signal a FunctionError")
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"hi":1}` {
		t.Fatalf("stub invoke body = %q, want the echoed payload", string(body))
	}
}

// TestEventSourceMappings is a regression guard for issue #319:
// CreateEventSourceMapping (and the ESM lifecycle) returned 405.
func TestEventSourceMappings(t *testing.T) {
	srv, _ := newServer(t)

	if r := postJSON(t, srv.URL+"/2015-03-31/functions",
		`{"FunctionName":"fx","Runtime":"go1.x","Handler":"main"}`); r.StatusCode != http.StatusCreated {
		t.Fatalf("create fn: %d", r.StatusCode)
	}

	esmURL := srv.URL + esmBasePath
	create := postJSON(t, esmURL,
		`{"FunctionName":"fx","EventSourceArn":"arn:aws:sqs:us-east-1:000000000000:q","BatchSize":5}`)
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create ESM status = %d", create.StatusCode)
	}

	var esm struct {
		UUID  string `json:"UUID"`
		State string `json:"State"`
	}

	decode(t, create, &esm)

	if esm.UUID == "" {
		t.Fatal("CreateEventSourceMapping returned empty UUID")
	}

	// GET by UUID.
	got := doJSON(t, http.MethodGet, esmURL+"/"+esm.UUID, "")
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get ESM status = %d", got.StatusCode)
	}

	// DELETE by UUID.
	if del := doJSON(t, http.MethodDelete, esmURL+"/"+esm.UUID, ""); del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete ESM status = %d", del.StatusCode)
	}
}

const esmBasePath = "/2015-03-31/event-source-mappings"

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

	body := `{"FunctionName":"envfn","Runtime":"go1.x","Handler":"main","Environment":{"Variables":{"K":"V"}}}`
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
	Timeout      int       `json:"Timeout"`
	Version      string    `json:"Version"`
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

func doJSON(t *testing.T, method, url, body string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new %s %s: %v", method, url, err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	return resp
}

// TestConfigurationVersionsAliases is a regression guard for issue #319: the
// Lambda handler previously returned 404 "unsupported Lambda path" for
// UpdateFunctionConfiguration, PublishVersion, and the alias sub-resources.
func TestConfigurationVersionsAliases(t *testing.T) {
	srv, _ := newServer(t)
	base := srv.URL + "/2015-03-31/functions"

	if resp := postJSON(t, base,
		`{"FunctionName":"fn","Runtime":"go1.x","Handler":"main","Timeout":10}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}

	// UpdateFunctionConfiguration.
	resp := doJSON(t, http.MethodPut, base+"/fn/configuration", `{"Timeout":60,"MemorySize":256}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update-configuration status = %d", resp.StatusCode)
	}

	var cfg functionShape
	decode(t, resp, &cfg)

	if cfg.Timeout != 60 {
		t.Fatalf("Timeout = %d, want 60", cfg.Timeout)
	}

	// PublishVersion.
	resp = postJSON(t, base+"/fn/versions", `{"Description":"v1"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish-version status = %d", resp.StatusCode)
	}

	var ver functionShape
	decode(t, resp, &ver)

	if ver.Version != "1" {
		t.Fatalf("Version = %q, want 1", ver.Version)
	}

	// CreateAlias + GetAlias.
	resp = postJSON(t, base+"/fn/aliases", `{"Name":"prod","FunctionVersion":"1"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create-alias status = %d", resp.StatusCode)
	}

	resp = doJSON(t, http.MethodGet, base+"/fn/aliases/prod", "")

	var alias struct {
		Name            string `json:"Name"`
		FunctionVersion string `json:"FunctionVersion"`
		AliasArn        string `json:"AliasArn"`
	}

	decode(t, resp, &alias)

	if alias.Name != "prod" || alias.FunctionVersion != "1" {
		t.Fatalf("get-alias = %+v", alias)
	}

	if !strings.Contains(alias.AliasArn, ":function:fn:prod") {
		t.Fatalf("AliasArn = %q", alias.AliasArn)
	}
}

// TestResourcePolicy is a regression guard for issue #319: AddPermission,
// GetPolicy, and RemovePermission (Terraform's aws_lambda_permission) were
// unreachable. It also verifies the AWS-local policyManager assertion path.
func TestResourcePolicy(t *testing.T) {
	srv, _ := newServer(t)
	base := srv.URL + "/2015-03-31/functions"

	if resp := postJSON(t, base,
		`{"FunctionName":"pf","Runtime":"go1.x","Handler":"main"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}

	// AddPermission.
	resp := postJSON(t, base+"/pf/policy",
		`{"StatementId":"s3invoke","Action":"lambda:InvokeFunction","Principal":"s3.amazonaws.com","SourceArn":"arn:aws:s3:::b"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add-permission status = %d", resp.StatusCode)
	}

	// GetPolicy surfaces the statement.
	resp = doJSON(t, http.MethodGet, base+"/pf/policy", "")

	var got struct {
		Policy string `json:"Policy"`
	}

	decode(t, resp, &got)

	if !strings.Contains(got.Policy, `"Sid":"s3invoke"`) {
		t.Fatalf("policy missing statement: %s", got.Policy)
	}

	// RemovePermission, then GetPolicy must 404.
	if resp := doJSON(t, http.MethodDelete, base+"/pf/policy/s3invoke", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove-permission status = %d", resp.StatusCode)
	}

	if resp := doJSON(t, http.MethodGet, base+"/pf/policy", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get-policy after remove status = %d, want 404", resp.StatusCode)
	}
}

// TestTagging is a regression guard for issue #319: the Lambda tagging API
// (/2017-03-31/tags/{arn}) was unmatched, so it fell through to the S3
// catch-all and returned a 405 + HTML body the SDK couldn't deserialize.
func TestTagging(t *testing.T) {
	srv, _ := newServer(t)

	if resp := postJSON(t, srv.URL+"/2015-03-31/functions",
		`{"FunctionName":"tf","Runtime":"go1.x","Handler":"main"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}

	// The SDK percent-encodes the ARN in the path; mirror that here so the
	// server's URL parser keeps the query string separate.
	tagsURL := srv.URL + "/2017-03-31/tags/" +
		url.PathEscape("arn:aws:lambda:us-east-1:000000000000:function:tf")

	// TagResource.
	if resp := postJSON(t, tagsURL, `{"Tags":{"env":"prod","team":"sls"}}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("tag-resource status = %d", resp.StatusCode)
	}

	// ListTags.
	resp := doJSON(t, http.MethodGet, tagsURL, "")

	var got struct {
		Tags map[string]string `json:"Tags"`
	}

	decode(t, resp, &got)

	if got.Tags["env"] != "prod" || got.Tags["team"] != "sls" {
		t.Fatalf("ListTags = %+v", got.Tags)
	}

	// UntagResource.
	if resp := doJSON(t, http.MethodDelete, tagsURL+"?tagKeys=env", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("untag-resource status = %d", resp.StatusCode)
	}

	var after struct {
		Tags map[string]string `json:"Tags"`
	}

	decode(t, doJSON(t, http.MethodGet, tagsURL, ""), &after)

	if _, has := after.Tags["env"]; has || after.Tags["team"] != "sls" {
		t.Fatalf("after untag = %+v", after.Tags)
	}
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()

	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
