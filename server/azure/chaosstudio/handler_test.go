package chaosstudio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/chaosstudio"
	chaosstudiosrv "github.com/stackshy/cloudemu/v2/server/azure/chaosstudio"
)

const (
	apiVer   = "?api-version=2023-11-01"
	basePath = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Chaos/experiments/"
)

type wireResp struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags"`
	Identity *struct {
		Type        string `json:"type"`
		PrincipalID string `json:"principalId"`
		TenantID    string `json:"tenantId"`
	} `json:"identity"`
	Properties struct {
		ProvisioningState string          `json:"provisioningState"`
		Selectors         json.RawMessage `json:"selectors"`
		Steps             json.RawMessage `json:"steps"`
	} `json:"properties"`
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := chaosstudio.New(config.NewOptions())
	h := chaosstudiosrv.New(mock)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, []byte) {
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

	return resp.StatusCode, raw
}

func decode(t *testing.T, raw []byte) wireResp {
	t.Helper()

	var out wireResp
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}

	return out
}

const createBody = `{
	"location": "West US",
	"tags": {"env": "dev"},
	"identity": {"type": "SystemAssigned"},
	"properties": {
		"selectors": [{"id":"Selector1","type":"List","filter":null,"targets":[{"id":"/subscriptions/s/resourceGroups/r/providers/Microsoft.Compute/virtualMachines/vm/providers/Microsoft.Chaos/targets/Microsoft-VirtualMachine","type":"ChaosTarget"}]}],
		"steps": [{"name":"step1","branches":[{"name":"branch1","actions":[{"type":"continuous","name":"urn:csci:microsoft:virtualMachine:shutdown/1.0","selectorId":"Selector1","duration":"PT10M","parameters":[{"key":"abruptShutdown","value":"false"}]}]}]}]
	}
}`

func TestWireCreateAndComputedFields(t *testing.T) {
	srv := newServer(t)
	path := basePath + "exp1" + apiVer

	code, raw := do(t, srv, http.MethodPut, path, createBody)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", code, raw)
	}

	got := decode(t, raw)
	if got.Type != "Microsoft.Chaos/experiments" {
		t.Errorf("type = %q", got.Type)
	}

	if got.Location != "West US" {
		t.Errorf("location = %q, want West US", got.Location)
	}

	if got.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", got.Properties.ProvisioningState)
	}

	if got.Identity == nil || got.Identity.PrincipalID == "" || got.Identity.TenantID == "" {
		t.Errorf("identity ids = %+v", got.Identity)
	}

	// selectors/steps must round-trip so terraform's typed unmarshal succeeds.
	var sel []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(got.Properties.Selectors, &sel); err != nil || len(sel) != 1 {
		t.Fatalf("selectors did not round-trip: %v (%s)", err, got.Properties.Selectors)
	}

	if sel[0].ID != "Selector1" || sel[0].Type != "List" {
		t.Errorf("selector discriminator lost: %+v", sel[0])
	}
}

func TestWireGetIsByteStable(t *testing.T) {
	srv := newServer(t)
	path := basePath + "exp1" + apiVer

	if code, raw := do(t, srv, http.MethodPut, path, createBody); code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	_, a := do(t, srv, http.MethodGet, path, "")
	_, b := do(t, srv, http.MethodGet, path, "")

	if !bytes.Equal(a, b) {
		t.Errorf("GET not byte-stable:\n a=%s\n b=%s", a, b)
	}
}

func TestWirePatchMergesAndPreservesComputed(t *testing.T) {
	srv := newServer(t)
	path := basePath + "exp1" + apiVer

	_, raw := do(t, srv, http.MethodPut, path, createBody)
	before := decode(t, raw)

	// PATCH mutates tags only. Everything else must survive.
	patch := `{"tags": {"env": "prod"}}`

	code, praw := do(t, srv, http.MethodPatch, path, patch)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d (%s)", code, praw)
	}

	after := decode(t, praw)
	if after.Tags["env"] != "prod" || len(after.Tags) != 1 {
		t.Errorf("tags = %+v (want replaced)", after.Tags)
	}

	if !bytes.Equal(after.Properties.Selectors, before.Properties.Selectors) {
		t.Errorf("selectors drifted on patch:\n before=%s\n after=%s", before.Properties.Selectors, after.Properties.Selectors)
	}

	if !bytes.Equal(after.Properties.Steps, before.Properties.Steps) {
		t.Errorf("steps drifted on patch")
	}

	if after.Identity == nil || after.Identity.PrincipalID != before.Identity.PrincipalID {
		t.Errorf("identity drifted/wiped on patch: %+v", after.Identity)
	}
}

func TestWirePatchOnMissingIs404(t *testing.T) {
	srv := newServer(t)
	path := basePath + "nope" + apiVer

	if code, _ := do(t, srv, http.MethodPatch, path, `{"tags":{"a":"b"}}`); code != http.StatusNotFound {
		t.Errorf("patch missing = %d, want 404", code)
	}
}

func TestWireDeleteIdempotent(t *testing.T) {
	srv := newServer(t)
	path := basePath + "exp1" + apiVer

	do(t, srv, http.MethodPut, path, createBody)

	if code, _ := do(t, srv, http.MethodDelete, path, ""); code != http.StatusOK {
		t.Errorf("first delete = %d, want 200", code)
	}

	if code, _ := do(t, srv, http.MethodDelete, path, ""); code != http.StatusNoContent {
		t.Errorf("second delete = %d, want 204", code)
	}

	if code, _ := do(t, srv, http.MethodGet, path, ""); code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", code)
	}
}

func TestWireList(t *testing.T) {
	srv := newServer(t)

	for _, n := range []string{"a", "b"} {
		do(t, srv, http.MethodPut, basePath+n+apiVer, createBody)
	}

	code, raw := do(t, srv, http.MethodGet, basePath+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}

	var out struct {
		Value []wireResp `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}

	if len(out.Value) != 2 {
		t.Errorf("list len = %d, want 2", len(out.Value))
	}
}

func TestWireDefaultArrays(t *testing.T) {
	srv := newServer(t)
	path := basePath + "bare" + apiVer

	_, raw := do(t, srv, http.MethodPut, path, `{"location":"eastus","identity":{"type":"SystemAssigned"}}`)
	got := decode(t, raw)

	if string(got.Properties.Selectors) != "[]" || string(got.Properties.Steps) != "[]" {
		t.Errorf("bare create arrays = %s / %s, want [] / []", got.Properties.Selectors, got.Properties.Steps)
	}
}

func TestWireMatches(t *testing.T) {
	h := chaosstudiosrv.New(chaosstudio.New(config.NewOptions()))

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, basePath+"g1"+apiVer, nil)
	if !h.Matches(req) {
		t.Errorf("handler should match experiment path")
	}

	other, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/subscriptions/s/resourceGroups/r/providers/Microsoft.Storage/storageAccounts/x"+apiVer, nil)
	if h.Matches(other) {
		t.Errorf("handler should not match storage path")
	}
}
