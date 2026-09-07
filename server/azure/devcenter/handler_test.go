package devcenter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/devcenter"
	devcentersrv "github.com/stackshy/cloudemu/v2/server/azure/devcenter"
)

const (
	apiVer   = "?api-version=2024-02-01"
	basePath = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.DevCenter/devcenters/"
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
		ProvisioningState      string `json:"provisioningState"`
		DevCenterURI           string `json:"devCenterUri"`
		DisplayName            string `json:"displayName"`
		ProjectCatalogSettings *struct {
			CatalogItemSyncEnableStatus string `json:"catalogItemSyncEnableStatus"`
		} `json:"projectCatalogSettings"`
	} `json:"properties"`
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := devcenter.New(config.NewOptions())
	h := devcentersrv.New(mock)
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
	"location": "Central US",
	"tags": {"env": "dev"},
	"identity": {"type": "SystemAssigned"},
	"properties": {
		"displayName": "Contoso Dev Center",
		"projectCatalogSettings": {"catalogItemSyncEnableStatus": "Enabled"}
	}
}`

func TestWireCreateAndComputedFields(t *testing.T) {
	srv := newServer(t)
	path := basePath + "dc1" + apiVer

	code, raw := do(t, srv, http.MethodPut, path, createBody)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", code, raw)
	}

	got := decode(t, raw)
	if got.Type != "Microsoft.DevCenter/devcenters" {
		t.Errorf("type = %q", got.Type)
	}

	if got.Location != "Central US" {
		t.Errorf("location = %q, want Central US", got.Location)
	}

	if !strings.HasSuffix(got.Properties.DevCenterURI, "-dc1.centralus.devcenter.azure.com") {
		t.Errorf("devCenterUri = %q, want ...-dc1.centralus.devcenter.azure.com", got.Properties.DevCenterURI)
	}

	if got.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", got.Properties.ProvisioningState)
	}

	if got.Properties.DisplayName != "Contoso Dev Center" {
		t.Errorf("displayName = %q", got.Properties.DisplayName)
	}

	if got.Properties.ProjectCatalogSettings == nil ||
		got.Properties.ProjectCatalogSettings.CatalogItemSyncEnableStatus != "Enabled" {
		t.Errorf("projectCatalogSettings = %+v", got.Properties.ProjectCatalogSettings)
	}

	if got.Identity == nil || got.Identity.PrincipalID == "" || got.Identity.TenantID == "" {
		t.Errorf("identity ids = %+v", got.Identity)
	}
}

func TestWireGetIsByteStable(t *testing.T) {
	srv := newServer(t)
	path := basePath + "dc1" + apiVer

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
	path := basePath + "dc1" + apiVer

	_, raw := do(t, srv, http.MethodPut, path, createBody)
	before := decode(t, raw)

	// PATCH mutates tags + the catalog toggle. Everything else must survive.
	patch := `{"tags": {"env": "prod"}, "properties": {"projectCatalogSettings": {"catalogItemSyncEnableStatus": "Disabled"}}}`

	code, praw := do(t, srv, http.MethodPatch, path, patch)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d (%s)", code, praw)
	}

	after := decode(t, praw)
	if after.Tags["env"] != "prod" || len(after.Tags) != 1 {
		t.Errorf("tags = %+v (want replaced)", after.Tags)
	}

	if after.Properties.ProjectCatalogSettings.CatalogItemSyncEnableStatus != "Disabled" {
		t.Errorf("catalog toggle = %q, want Disabled", after.Properties.ProjectCatalogSettings.CatalogItemSyncEnableStatus)
	}

	// Untouched fields preserved.
	if after.Properties.DisplayName != "Contoso Dev Center" {
		t.Errorf("displayName drifted: %q", after.Properties.DisplayName)
	}

	if after.Properties.DevCenterURI != before.Properties.DevCenterURI {
		t.Errorf("devCenterUri drifted on patch: %q vs %q", after.Properties.DevCenterURI, before.Properties.DevCenterURI)
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
	path := basePath + "dc1" + apiVer

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

func TestWireMatches(t *testing.T) {
	h := devcentersrv.New(devcenter.New(config.NewOptions()))

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, basePath+"g1"+apiVer, nil)
	if !h.Matches(req) {
		t.Errorf("handler should match devcenter path")
	}

	other, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/subscriptions/s/resourceGroups/r/providers/Microsoft.Storage/storageAccounts/x"+apiVer, nil)
	if h.Matches(other) {
		t.Errorf("handler should not match storage path")
	}
}
