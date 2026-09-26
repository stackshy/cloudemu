package logic_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/logic"
	logicsrv "github.com/stackshy/cloudemu/v2/server/azure/logic"
)

const (
	apiVer   = "?api-version=2019-05-01"
	basePath = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Logic/workflows/"
	wfDef    = `{"contentVersion":"1.0.0.0","triggers":{"manual":{"type":"Request","kind":"Http"}},"actions":{}}`
)

type wireResp struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags"`
	Error    *struct {
		Code string `json:"code"`
	} `json:"error"`
	Properties struct {
		ProvisioningState string          `json:"provisioningState"`
		State             string          `json:"state"`
		Version           string          `json:"version"`
		AccessEndpoint    string          `json:"accessEndpoint"`
		CreatedTime       string          `json:"createdTime"`
		ChangedTime       string          `json:"changedTime"`
		Definition        json.RawMessage `json:"definition"`
		Parameters        json.RawMessage `json:"parameters"`
	} `json:"properties"`
	Value []wireResp `json:"value"`
}

func newServer(t *testing.T) (*httptest.Server, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC))
	h := logicsrv.New(logic.New(config.NewOptions(config.WithClock(fc))))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return srv, fc
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, wireResp, string) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)

	var out wireResp
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s %s: %v: %s", method, path, err, raw)
		}
	}

	return resp.StatusCode, out, string(raw)
}

func TestWireCreateGetReplace(t *testing.T) {
	srv, fc := newServer(t)
	path := basePath + "wf1" + apiVer

	body := `{"location":"eastus","tags":{"env":"dev"},` +
		`"properties":{"definition":` + wfDef + `,"parameters":{"p":{"type":"String","value":"x"}}}}`

	status, created, _ := do(t, srv, http.MethodPut, path, body)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", status)
	}

	if created.Type != "Microsoft.Logic/workflows" || created.Name != "wf1" ||
		created.ID != "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Logic/workflows/wf1" {
		t.Errorf("identity fields: %+v", created)
	}

	p := created.Properties
	if p.ProvisioningState != "Succeeded" || p.State != "Enabled" || p.AccessEndpoint == "" ||
		p.CreatedTime != "2026-03-04T05:06:07Z" || p.Version == "" {
		t.Errorf("computed properties: %+v", p)
	}

	if string(p.Definition) != wfDef {
		t.Errorf("definition not verbatim:\n got %s\nwant %s", p.Definition, wfDef)
	}

	fc.Advance(time.Minute)

	status, replaced, _ := do(t, srv, http.MethodPut, path, `{"location":"eastus","properties":{"state":"Disabled"}}`)
	if status != http.StatusOK {
		t.Fatalf("replace status = %d, want 200", status)
	}

	if replaced.Properties.State != "Disabled" || replaced.Properties.CreatedTime != p.CreatedTime ||
		replaced.Properties.ChangedTime != "2026-03-04T05:07:07Z" || replaced.Properties.AccessEndpoint != p.AccessEndpoint {
		t.Errorf("replace: %+v", replaced.Properties)
	}

	_, got, _ := do(t, srv, http.MethodGet, path, "")
	if got.Properties.State != "Disabled" || got.Properties.Definition != nil {
		t.Errorf("get after replace (PUT is full replace): %+v", got.Properties)
	}
}

func TestWirePatchMergesTagsAndProperties(t *testing.T) {
	srv, _ := newServer(t)
	path := basePath + "wf1" + apiVer

	do(t, srv, http.MethodPut, path, `{"location":"eastus","tags":{"a":"1"},`+
		`"properties":{"definition":`+wfDef+`,"parameters":{"p":{"value":1}}}}`)

	// Tags only: properties untouched.
	status, patched, _ := do(t, srv, http.MethodPatch, path, `{"tags":{"b":"2"}}`)
	if status != http.StatusOK {
		t.Fatalf("patch status = %d", status)
	}

	if len(patched.Tags) != 1 || patched.Tags["b"] != "2" || string(patched.Properties.Definition) != wfDef {
		t.Errorf("tags patch: tags=%v def=%s", patched.Tags, patched.Properties.Definition)
	}

	// One property: the others and the tags survive.
	_, patched, _ = do(t, srv, http.MethodPatch, path, `{"properties":{"state":"Disabled"}}`)
	if patched.Properties.State != "Disabled" || patched.Tags["b"] != "2" ||
		string(patched.Properties.Definition) != wfDef || string(patched.Properties.Parameters) != `{"p":{"value":1}}` {
		t.Errorf("property patch: %+v tags=%v", patched.Properties, patched.Tags)
	}

	// An empty body (what armlogic's Update sends) is a no-op echo.
	before := patched.Properties.Version

	status, patched, _ = do(t, srv, http.MethodPatch, path, "")
	if status != http.StatusOK || patched.Properties.Version != before {
		t.Errorf("empty patch: status=%d version %q -> %q", status, before, patched.Properties.Version)
	}

	if status, _, _ := do(t, srv, http.MethodPatch, path, `{"properties":{"state":"Deleted"}}`); status != http.StatusBadRequest {
		t.Errorf("patch to service-owned state: status=%d, want 400", status)
	}
}

func TestWireEnableDisable(t *testing.T) {
	srv, _ := newServer(t)
	do(t, srv, http.MethodPut, basePath+"wf1"+apiVer, `{"location":"eastus"}`)

	status, _, raw := do(t, srv, http.MethodPost, basePath+"wf1/disable"+apiVer, "")
	if status != http.StatusOK || raw != "" {
		t.Fatalf("disable: status=%d body=%q", status, raw)
	}

	if _, got, _ := do(t, srv, http.MethodGet, basePath+"wf1"+apiVer, ""); got.Properties.State != "Disabled" {
		t.Errorf("state after disable = %q", got.Properties.State)
	}

	if status, _, _ := do(t, srv, http.MethodPost, basePath+"wf1/enable"+apiVer, ""); status != http.StatusOK {
		t.Fatalf("enable status = %d", status)
	}

	if _, got, _ := do(t, srv, http.MethodGet, basePath+"wf1"+apiVer, ""); got.Properties.State != "Enabled" {
		t.Errorf("state after enable = %q", got.Properties.State)
	}

	if status, _, _ := do(t, srv, http.MethodGet, basePath+"wf1/enable"+apiVer, ""); status != http.StatusMethodNotAllowed {
		t.Errorf("GET enable status = %d, want 405", status)
	}
}

func TestWireMissingIs404ResourceNotFound(t *testing.T) {
	srv, _ := newServer(t)

	requests := []struct{ method, path, body string }{
		{http.MethodGet, basePath + "nope" + apiVer, ""},
		{http.MethodPatch, basePath + "nope" + apiVer, `{"tags":{}}`},
		{http.MethodPost, basePath + "nope/enable" + apiVer, ""},
		{http.MethodPost, basePath + "nope/disable" + apiVer, ""},
	}

	for _, rq := range requests {
		status, resp, _ := do(t, srv, rq.method, rq.path, rq.body)
		if status != http.StatusNotFound || resp.Error == nil || resp.Error.Code != "ResourceNotFound" {
			t.Errorf("%s %s: status=%d err=%+v", rq.method, rq.path, status, resp.Error)
		}
	}

	if status, _, _ := do(t, srv, http.MethodDelete, basePath+"nope"+apiVer, ""); status != http.StatusNoContent {
		t.Errorf("delete missing status = %d, want 204", status)
	}
}

func TestWireListAndDelete(t *testing.T) {
	srv, _ := newServer(t)

	do(t, srv, http.MethodPut, basePath+"b"+apiVer, `{"location":"eastus"}`)
	do(t, srv, http.MethodPut, basePath+"a"+apiVer, `{"location":"eastus"}`)
	do(t, srv, http.MethodPut,
		"/subscriptions/sub1/resourceGroups/rg2/providers/Microsoft.Logic/workflows/c"+apiVer, `{"location":"eastus"}`)

	_, byRG, _ := do(t, srv, http.MethodGet, "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Logic/workflows"+apiVer, "")
	if len(byRG.Value) != 2 || byRG.Value[0].Name != "a" {
		t.Errorf("list by rg = %+v", byRG.Value)
	}

	_, bySub, _ := do(t, srv, http.MethodGet, "/subscriptions/sub1/providers/Microsoft.Logic/workflows"+apiVer, "")
	if len(bySub.Value) != 3 {
		t.Errorf("list by sub len = %d, want 3", len(bySub.Value))
	}

	if status, _, _ := do(t, srv, http.MethodDelete, basePath+"a"+apiVer, ""); status != http.StatusOK {
		t.Errorf("delete status = %d, want 200", status)
	}

	if status, _, _ := do(t, srv, http.MethodGet, basePath+"a"+apiVer, ""); status != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want 404", status)
	}
}

func TestMatches(t *testing.T) {
	h := logicsrv.New(logic.New(config.NewOptions()))

	cases := map[string]bool{
		basePath + "wf1":         true,
		basePath + "wf1/enable":  true,
		basePath + "wf1/disable": true,
		"/subscriptions/s/providers/microsoft.logic/workflows": true,
		basePath + "wf1/runs":            false,
		basePath + "wf1/triggers/manual": false,
		"/subscriptions/s/resourceGroups/r/providers/Microsoft.Logic/integrationAccounts/x": false,
		"/subscriptions/s/resourceGroups/r/providers/Microsoft.Web/sites/x":                 false,
	}

	for path, want := range cases {
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		if got := h.Matches(req); got != want {
			t.Errorf("Matches(%s) = %v, want %v", path, got, want)
		}
	}
}
