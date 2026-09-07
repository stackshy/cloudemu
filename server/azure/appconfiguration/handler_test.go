package appconfiguration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/appconfiguration"
	appconfigsrv "github.com/stackshy/cloudemu/v2/server/azure/appconfiguration"
)

const (
	apiVer   = "?api-version=2023-03-01"
	basePath = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.AppConfiguration/configurationStores/"
)

type wireResp struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags"`
	Sku      *struct {
		Name string `json:"name"`
	} `json:"sku"`
	Identity *struct {
		Type        string `json:"type"`
		PrincipalID string `json:"principalId"`
		TenantID    string `json:"tenantId"`
	} `json:"identity"`
	Properties struct {
		ProvisioningState   string `json:"provisioningState"`
		Endpoint            string `json:"endpoint"`
		CreationDate        string `json:"creationDate"`
		DisableLocalAuth    *bool  `json:"disableLocalAuth"`
		PublicNetworkAccess string `json:"publicNetworkAccess"`
	} `json:"properties"`
}

type apiKey struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Value            string `json:"value"`
	ConnectionString string `json:"connectionString"`
	ReadOnly         bool   `json:"readOnly"`
}

type keysResp struct {
	Value []apiKey `json:"value"`
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := appconfiguration.New(config.NewOptions())
	h := appconfigsrv.New(mock)
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
	"sku": {"name": "standard"},
	"identity": {"type": "SystemAssigned"},
	"properties": {"disableLocalAuth": false, "publicNetworkAccess": "Enabled"}
}`

func TestWireCreateAndComputedFields(t *testing.T) {
	srv := newServer(t)
	path := basePath + "store1" + apiVer

	code, raw := do(t, srv, http.MethodPut, path, createBody)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", code, raw)
	}

	got := decode(t, raw)
	if got.Type != "Microsoft.AppConfiguration/configurationStores" {
		t.Errorf("type = %q", got.Type)
	}

	if got.Properties.Endpoint != "https://store1.azconfig.io" {
		t.Errorf("endpoint = %q", got.Properties.Endpoint)
	}

	if got.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q", got.Properties.ProvisioningState)
	}

	if got.Sku == nil || got.Sku.Name != "standard" {
		t.Errorf("sku = %+v", got.Sku)
	}

	if got.Identity == nil || got.Identity.PrincipalID == "" || got.Identity.TenantID == "" {
		t.Errorf("identity ids = %+v", got.Identity)
	}

	if got.Properties.DisableLocalAuth == nil || *got.Properties.DisableLocalAuth {
		t.Errorf("disableLocalAuth = %+v, want false", got.Properties.DisableLocalAuth)
	}
}

func TestWireGetIsByteStable(t *testing.T) {
	srv := newServer(t)
	path := basePath + "store1" + apiVer

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
	path := basePath + "store1" + apiVer

	_, raw := do(t, srv, http.MethodPut, path, createBody)
	before := decode(t, raw)

	patch := `{"tags": {"env": "prod"}}`

	code, praw := do(t, srv, http.MethodPatch, path, patch)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d (%s)", code, praw)
	}

	after := decode(t, praw)
	if after.Tags["env"] != "prod" {
		t.Errorf("tags = %+v", after.Tags)
	}

	if after.Properties.Endpoint != before.Properties.Endpoint ||
		after.Properties.CreationDate != before.Properties.CreationDate ||
		after.Identity.PrincipalID != before.Identity.PrincipalID {
		t.Errorf("computed fields drifted on patch")
	}

	// sku was not mentioned in the PATCH, so it must survive.
	if after.Sku == nil || after.Sku.Name != "standard" {
		t.Errorf("sku not preserved on patch: %+v", after.Sku)
	}

	// disableLocalAuth was not mentioned, so it must survive.
	if after.Properties.DisableLocalAuth == nil || *after.Properties.DisableLocalAuth {
		t.Errorf("disableLocalAuth not preserved on patch: %+v", after.Properties.DisableLocalAuth)
	}
}

func TestWireListKeysShapeAndStability(t *testing.T) {
	srv := newServer(t)
	path := basePath + "store1" + apiVer

	if code, raw := do(t, srv, http.MethodPut, path, createBody); code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	keysPath := basePath + "store1/listKeys" + apiVer

	code, raw := do(t, srv, http.MethodPost, keysPath, "")
	if code != http.StatusOK {
		t.Fatalf("listKeys status = %d (%s)", code, raw)
	}

	var first keysResp
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("unmarshal keys: %v", err)
	}

	if len(first.Value) != 4 {
		t.Fatalf("keys = %d, want 4", len(first.Value))
	}

	wantNames := map[string]bool{"Primary": false, "Secondary": false, "Primary Read Only": true, "Secondary Read Only": true}
	for _, k := range first.Value {
		wantRO, ok := wantNames[k.Name]
		if !ok {
			t.Errorf("unexpected key name %q", k.Name)
		}

		if k.ReadOnly != wantRO {
			t.Errorf("key %q readOnly = %v, want %v", k.Name, k.ReadOnly, wantRO)
		}

		if k.ID == "" || k.Value == "" {
			t.Errorf("key %q empty id/value", k.Name)
		}

		want := "Endpoint=https://store1.azconfig.io;Id=" + k.ID + ";Secret=" + k.Value
		if k.ConnectionString != want {
			t.Errorf("key %q connectionString = %q, want %q", k.Name, k.ConnectionString, want)
		}
	}

	_, raw2 := do(t, srv, http.MethodPost, keysPath, "")
	if !bytes.Equal(raw, raw2) {
		t.Errorf("listKeys not stable:\n a=%s\n b=%s", raw, raw2)
	}

	// regenerateKey returns a SINGLE ApiKey (not the listKeys envelope) for the
	// key named by the request body's "id".
	regenPath := basePath + "store1/regenerateKey" + apiVer

	code, rraw := do(t, srv, http.MethodPost, regenPath, `{"id":"Primary"}`)
	if code != http.StatusOK {
		t.Fatalf("regenerateKey = %d (%s)", code, rraw)
	}

	var single struct {
		Name     string `json:"name"`
		ID       string `json:"id"`
		Value    string `json:"value"`
		ReadOnly bool   `json:"readOnly"`
	}
	if err := json.Unmarshal(rraw, &single); err != nil {
		t.Fatalf("unmarshal regenerateKey: %v", err)
	}

	if bytes.Contains(rraw, []byte(`"value":[`)) {
		t.Errorf("regenerateKey returned a list envelope, want a single ApiKey: %s", rraw)
	}

	if single.Name != "Primary" || single.ID == "" || single.Value == "" {
		t.Errorf("regenerateKey single key wrong: %+v (%s)", single, rraw)
	}

	// An unknown key id is rejected.
	if code, rraw := do(t, srv, http.MethodPost, regenPath, `{"id":"Nope"}`); code != http.StatusNotFound {
		t.Errorf("regenerateKey unknown id = %d (%s), want 404", code, rraw)
	}
}

func TestWireDeleteIdempotent(t *testing.T) {
	srv := newServer(t)
	path := basePath + "store1" + apiVer

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

	for _, n := range []string{"store-a", "store-b"} {
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
