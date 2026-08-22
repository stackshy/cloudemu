package functions_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// recordingEngine is a config.FunctionEngine that records what the wire+provider
// plumbing hands it, so a server-level test can assert the zipdeploy PUT drives
// Deploy and the invoke drives Invoke — without a real runtime (no Docker).
type recordingEngine struct {
	deploys []config.FunctionDeployment
	invoked []string
	result  config.FunctionResult
}

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract.
func (e *recordingEngine) Deploy(_ context.Context, fn config.FunctionDeployment) error {
	e.deploys = append(e.deploys, fn)

	return nil
}

func (e *recordingEngine) Invoke(_ context.Context, name string, _ []byte) (config.FunctionResult, error) {
	e.invoked = append(e.invoked, name)

	return e.result, nil
}

func (e *recordingEngine) Remove(_ context.Context, _ string) error { return nil }

func zipDeployURL(name string) string {
	return sitesURL(name) + "/extensions/zipdeploy"
}

// siteBodyWithHandler is a site PUT body carrying the reserved _CLOUDEMU_HANDLER
// app setting plus an ordinary one, so the test can check the reserved key is
// consumed and the ordinary one survives.
const siteBodyWithHandler = `{
    "kind":"functionapp",
    "location":"eastus",
    "properties":{
        "siteConfig":{
            "linuxFxVersion":"Python|3.12",
            "appSettings":[
                {"name":"_CLOUDEMU_HANDLER","value":"function_app.main"},
                {"name":"FOO","value":"bar"}
            ]
        }
    }
}`

func TestZipDeployDrivesEngineAndHandlerSetting(t *testing.T) {
	eng := &recordingEngine{result: config.FunctionResult{Payload: []byte(`{"ran":true}`)}}
	cloud := cloudemu.NewAzure(config.WithFunctionEngine(eng))
	srv := httptest.NewServer(azureserver.New(azureserver.Drivers{Functions: cloud.Functions}))
	t.Cleanup(srv.Close)

	// 1. ARM site create carries no code — the engine is not deployed yet.
	doReq(t, srv.URL, http.MethodPut, sitesURL("app1")+apiVer,
		strings.NewReader(siteBodyWithHandler), http.StatusOK)

	if len(eng.deploys) != 0 {
		t.Fatalf("site create should not deploy code; got %d deploys", len(eng.deploys))
	}

	// 2. zipdeploy PUT carries the raw zip bytes and deploys to the engine.
	doReq(t, srv.URL, http.MethodPut, zipDeployURL("app1"),
		bytes.NewReader([]byte("PK-zip-bytes")), http.StatusOK)

	if len(eng.deploys) != 1 {
		t.Fatalf("zipdeploy should deploy once; got %d", len(eng.deploys))
	}

	dep := eng.deploys[0]
	if string(dep.Code) != "PK-zip-bytes" {
		t.Fatalf("deployed code = %q, want the zip bytes", string(dep.Code))
	}

	// The reserved app setting became the handler entrypoint.
	if dep.Handler != "function_app.main" {
		t.Fatalf("deploy handler = %q, want function_app.main", dep.Handler)
	}

	// It was stripped from the env; the ordinary setting survives.
	if _, leaked := dep.Env["_CLOUDEMU_HANDLER"]; leaked {
		t.Fatalf("reserved handler setting leaked into env: %v", dep.Env)
	}

	if dep.Env["FOO"] != "bar" {
		t.Fatalf("ordinary app setting lost: env = %v", dep.Env)
	}

	// 3. Invoking the deployed function drives the engine, not the echo stub.
	resp := doReq(t, srv.URL, http.MethodPost, "/api/app1",
		strings.NewReader(`{"in":1}`), http.StatusOK)
	if string(resp) != `{"ran":true}` {
		t.Fatalf("invoke body = %q, want engine result", string(resp))
	}

	if len(eng.invoked) != 1 || eng.invoked[0] != "app1" {
		t.Fatalf("engine invoke not driven: %v", eng.invoked)
	}

	// 4. GET the site — the reserved handler key must not be echoed back.
	getBody := doReq(t, srv.URL, http.MethodGet, sitesURL("app1")+apiVer, nil, http.StatusOK)
	if bytes.Contains(getBody, []byte("_CLOUDEMU_HANDLER")) {
		t.Fatalf("reserved handler setting leaked into GET response: %s", getBody)
	}
}

func doReq(t *testing.T, base, method, path string, body io.Reader, wantStatus int) []byte {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, base+path, body)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	out, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d (body: %s)", method, path, resp.StatusCode, wantStatus, out)
	}

	return out
}
