package signalr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/signalr"
	signalrsrv "github.com/stackshy/cloudemu/v2/server/azure/signalr"
)

const (
	apiVer   = "?api-version=2023-02-01"
	basePath = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.SignalRService/signalR/"
)

type wireResp struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags"`
	Sku      *struct {
		Name     string `json:"name"`
		Tier     string `json:"tier"`
		Size     string `json:"size"`
		Capacity int    `json:"capacity"`
	} `json:"sku"`
	Identity *struct {
		Type        string `json:"type"`
		PrincipalID string `json:"principalId"`
		TenantID    string `json:"tenantId"`
	} `json:"identity"`
	Properties struct {
		ProvisioningState string `json:"provisioningState"`
		HostName          string `json:"hostName"`
		ExternalIP        string `json:"externalIP"`
		PublicPort        int    `json:"publicPort"`
		ServerPort        int    `json:"serverPort"`
		Cors              *struct {
			AllowedOrigins []string `json:"allowedOrigins"`
		} `json:"cors"`
		Serverless struct {
			ConnectionTimeoutInSeconds int `json:"connectionTimeoutInSeconds"`
		} `json:"serverless"`
	} `json:"properties"`
}

type keysResp struct {
	PrimaryKey                string `json:"primaryKey"`
	SecondaryKey              string `json:"secondaryKey"`
	PrimaryConnectionString   string `json:"primaryConnectionString"`
	SecondaryConnectionString string `json:"secondaryConnectionString"`
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := signalr.New(config.NewOptions())
	h := signalrsrv.New(mock)
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
	"sku": {"name": "Standard_S1", "capacity": 1},
	"identity": {"type": "SystemAssigned"},
	"properties": {"cors": {"allowedOrigins": ["https://example.com"]}}
}`

func TestWireCreateAndComputedFields(t *testing.T) {
	srv := newServer(t)
	path := basePath + "sig1" + apiVer

	code, raw := do(t, srv, http.MethodPut, path, createBody)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", code, raw)
	}

	got := decode(t, raw)
	if got.Type != "Microsoft.SignalRService/signalR" {
		t.Errorf("type = %q", got.Type)
	}

	if got.Properties.HostName != "sig1.service.signalr.net" {
		t.Errorf("hostName = %q", got.Properties.HostName)
	}

	if got.Properties.PublicPort != 443 || got.Properties.ServerPort != 443 {
		t.Errorf("ports = %d/%d", got.Properties.PublicPort, got.Properties.ServerPort)
	}

	if got.Sku == nil || got.Sku.Tier != "Standard" || got.Sku.Size != "S1" {
		t.Errorf("sku = %+v", got.Sku)
	}

	if got.Identity == nil || got.Identity.PrincipalID == "" || got.Identity.TenantID == "" {
		t.Errorf("identity ids = %+v", got.Identity)
	}

	if got.Properties.Cors == nil || len(got.Properties.Cors.AllowedOrigins) != 1 {
		t.Errorf("cors = %+v", got.Properties.Cors)
	}

	if got.Properties.Serverless.ConnectionTimeoutInSeconds != 30 {
		t.Errorf("serverless timeout = %d, want 30", got.Properties.Serverless.ConnectionTimeoutInSeconds)
	}
}

func TestWireGetIsByteStable(t *testing.T) {
	srv := newServer(t)
	path := basePath + "sig1" + apiVer

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
	path := basePath + "sig1" + apiVer

	_, raw := do(t, srv, http.MethodPut, path, createBody)
	before := decode(t, raw)

	patch := `{"sku": {"name": "Standard_S1", "capacity": 3}, "tags": {"env": "prod"}}`

	code, praw := do(t, srv, http.MethodPatch, path, patch)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d (%s)", code, praw)
	}

	after := decode(t, praw)
	if after.Sku.Capacity != 3 {
		t.Errorf("capacity = %d, want 3", after.Sku.Capacity)
	}

	if after.Tags["env"] != "prod" {
		t.Errorf("tags = %+v", after.Tags)
	}

	if after.Properties.HostName != before.Properties.HostName ||
		after.Properties.ExternalIP != before.Properties.ExternalIP {
		t.Errorf("computed fields drifted on patch")
	}

	// cors was not mentioned in the PATCH, so it must survive.
	if after.Properties.Cors == nil || len(after.Properties.Cors.AllowedOrigins) != 1 {
		t.Errorf("cors not preserved on patch: %+v", after.Properties.Cors)
	}
}

func TestWireListKeysIsStable(t *testing.T) {
	srv := newServer(t)
	path := basePath + "sig1" + apiVer

	if code, raw := do(t, srv, http.MethodPut, path, createBody); code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	keysPath := basePath + "sig1/listKeys" + apiVer

	code, raw := do(t, srv, http.MethodPost, keysPath, "")
	if code != http.StatusOK {
		t.Fatalf("listKeys status = %d (%s)", code, raw)
	}

	var first keysResp
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("unmarshal keys: %v", err)
	}

	if first.PrimaryKey == "" || first.PrimaryConnectionString == "" {
		t.Fatalf("empty keys: %+v", first)
	}

	_, raw2 := do(t, srv, http.MethodPost, keysPath, "")
	if !bytes.Equal(raw, raw2) {
		t.Errorf("listKeys not stable:\n a=%s\n b=%s", raw, raw2)
	}
}

func TestWireDeleteIdempotent(t *testing.T) {
	srv := newServer(t)
	path := basePath + "sig1" + apiVer

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
