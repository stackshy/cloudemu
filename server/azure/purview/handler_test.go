package purview_test

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
	"github.com/stackshy/cloudemu/v2/providers/azure/purview"
	purviewsrv "github.com/stackshy/cloudemu/v2/server/azure/purview"
)

const (
	apiVer   = "?api-version=2021-12-01"
	basePath = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Purview/accounts/"
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
	Sku *struct {
		Name     string `json:"name"`
		Capacity int    `json:"capacity"`
	} `json:"sku"`
	Properties struct {
		ProvisioningState        string `json:"provisioningState"`
		PublicNetworkAccess      string `json:"publicNetworkAccess"`
		ManagedEventHubState     string `json:"managedEventHubState"`
		ManagedResourceGroupName string `json:"managedResourceGroupName"`
		FriendlyName             string `json:"friendlyName"`
		Endpoints                *struct {
			Catalog  string `json:"catalog"`
			Guardian string `json:"guardian"`
			Scan     string `json:"scan"`
		} `json:"endpoints"`
		ManagedResources *struct {
			ResourceGroup     string `json:"resourceGroup"`
			StorageAccount    string `json:"storageAccount"`
			EventHubNamespace string `json:"eventHubNamespace"`
		} `json:"managedResources"`
	} `json:"properties"`
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := purview.New(config.NewOptions())
	h := purviewsrv.New(mock)
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
	"location": "East US",
	"tags": {"env": "dev"},
	"identity": {"type": "SystemAssigned"},
	"properties": {
		"publicNetworkAccess": "Enabled",
		"managedEventHubState": "Enabled"
	}
}`

func TestWireCreateAndComputedFields(t *testing.T) {
	srv := newServer(t)
	path := basePath + "pv1" + apiVer

	code, raw := do(t, srv, http.MethodPut, path, createBody)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", code, raw)
	}

	got := decode(t, raw)
	if got.Type != "Microsoft.Purview/accounts" {
		t.Errorf("type = %q", got.Type)
	}

	if got.Location != "East US" {
		t.Errorf("location = %q, want East US", got.Location)
	}

	if got.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", got.Properties.ProvisioningState)
	}

	if got.Properties.Endpoints == nil ||
		got.Properties.Endpoints.Catalog != "https://pv1.purview.azure.com/catalog" ||
		got.Properties.Endpoints.Scan != "https://pv1.purview.azure.com/scan" ||
		got.Properties.Endpoints.Guardian != "https://pv1.purview.azure.com/guardian" {
		t.Errorf("endpoints = %+v", got.Properties.Endpoints)
	}

	if got.Properties.ManagedResourceGroupName != "managed-rg-pv1" {
		t.Errorf("managedResourceGroupName = %q", got.Properties.ManagedResourceGroupName)
	}

	if got.Properties.ManagedResources == nil ||
		got.Properties.ManagedResources.ResourceGroup != "/subscriptions/sub1/resourceGroups/managed-rg-pv1" ||
		!strings.Contains(got.Properties.ManagedResources.StorageAccount, "/storageAccounts/scan") ||
		!strings.Contains(got.Properties.ManagedResources.EventHubNamespace, "/namespaces/Atlas-") {
		t.Errorf("managedResources = %+v", got.Properties.ManagedResources)
	}

	if got.Sku == nil || got.Sku.Name != "Standard" || got.Sku.Capacity != 1 {
		t.Errorf("sku = %+v, want {Standard 1}", got.Sku)
	}

	if got.Identity == nil || got.Identity.PrincipalID == "" || got.Identity.TenantID == "" {
		t.Errorf("identity ids = %+v", got.Identity)
	}
}

func TestWireGetIsByteStable(t *testing.T) {
	srv := newServer(t)
	path := basePath + "pv1" + apiVer

	if code, raw := do(t, srv, http.MethodPut, path, createBody); code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	_, a := do(t, srv, http.MethodGet, path, "")
	_, b := do(t, srv, http.MethodGet, path, "")

	if !bytes.Equal(a, b) {
		t.Errorf("GET not byte-stable:\n a=%s\n b=%s", a, b)
	}
}

func TestWireListKeys(t *testing.T) {
	srv := newServer(t)
	path := basePath + "pv1" + apiVer

	do(t, srv, http.MethodPut, path, createBody)

	code, raw := do(t, srv, http.MethodPost, basePath+"pv1/listkeys"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("listkeys status = %d (%s)", code, raw)
	}

	var keys struct {
		Primary   string `json:"atlasKafkaPrimaryEndpoint"`
		Secondary string `json:"atlasKafkaSecondaryEndpoint"`
	}
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("unmarshal keys: %v", err)
	}

	if !strings.HasPrefix(keys.Primary, "Endpoint=sb://atlas-") || keys.Primary == keys.Secondary {
		t.Errorf("keys = %+v", keys)
	}

	// A second call returns identical (stable) keys.
	_, raw2 := do(t, srv, http.MethodPost, basePath+"pv1/listkeys"+apiVer, "")
	if !bytes.Equal(raw, raw2) {
		t.Errorf("listkeys not stable")
	}
}

func TestWirePatchMergesAndPreservesComputed(t *testing.T) {
	srv := newServer(t)
	path := basePath + "pv1" + apiVer

	_, raw := do(t, srv, http.MethodPut, path, createBody)
	before := decode(t, raw)

	// PATCH mutates tags + the public-network toggle. Everything else must survive.
	patch := `{"tags": {"env": "prod"}, "properties": {"publicNetworkAccess": "Disabled"}}`

	code, praw := do(t, srv, http.MethodPatch, path, patch)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d (%s)", code, praw)
	}

	after := decode(t, praw)
	if after.Tags["env"] != "prod" || len(after.Tags) != 1 {
		t.Errorf("tags = %+v (want replaced)", after.Tags)
	}

	if after.Properties.PublicNetworkAccess != "Disabled" {
		t.Errorf("publicNetworkAccess = %q, want Disabled", after.Properties.PublicNetworkAccess)
	}

	// Untouched fields preserved.
	if after.Properties.ManagedEventHubState != "Enabled" {
		t.Errorf("managedEventHubState drifted: %q", after.Properties.ManagedEventHubState)
	}

	if after.Properties.Endpoints.Catalog != before.Properties.Endpoints.Catalog {
		t.Errorf("catalog endpoint drifted on patch")
	}

	if after.Properties.ManagedResources.StorageAccount != before.Properties.ManagedResources.StorageAccount {
		t.Errorf("managedResources drifted on patch")
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
	path := basePath + "pv1" + apiVer

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
	h := purviewsrv.New(purview.New(config.NewOptions()))

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, basePath+"g1"+apiVer, nil)
	if !h.Matches(req) {
		t.Errorf("handler should match purview path")
	}

	other, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/subscriptions/s/resourceGroups/r/providers/Microsoft.Storage/storageAccounts/x"+apiVer, nil)
	if h.Matches(other) {
		t.Errorf("handler should not match storage path")
	}
}
