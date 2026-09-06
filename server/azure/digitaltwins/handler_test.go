package digitaltwins_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/digitaltwins"
	digitaltwinssrv "github.com/stackshy/cloudemu/v2/server/azure/digitaltwins"
)

const (
	apiVer   = "?api-version=2023-01-31"
	basePath = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.DigitalTwins/digitalTwinsInstances/"
)

type wireResp struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Location string `json:"location"`
	Tags     map[string]string
	Identity *struct {
		Type         string `json:"type"`
		PrincipalID  string `json:"principalId"`
		TenantID     string `json:"tenantId"`
		UserAssigned map[string]struct {
			PrincipalID string `json:"principalId"`
			ClientID    string `json:"clientId"`
		} `json:"userAssignedIdentities"`
	} `json:"identity"`
	Properties struct {
		HostName            string `json:"hostName"`
		ProvisioningState   string `json:"provisioningState"`
		PublicNetworkAccess string `json:"publicNetworkAccess"`
		CreatedTime         string `json:"createdTime"`
		LastUpdatedTime     string `json:"lastUpdatedTime"`
	} `json:"properties"`
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := digitaltwins.New(config.NewOptions())
	h := digitaltwinssrv.New(mock)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, wireResp) {
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
		_ = json.Unmarshal(raw, &out)
	}

	return resp.StatusCode, out
}

func TestWireLifecycle(t *testing.T) {
	srv := newServer(t)
	path := basePath + "dt1" + apiVer

	createBody := `{
		"location": "East US",
		"tags": {"env": "dev"},
		"identity": {"type": "SystemAssigned"}
	}`

	status, created := do(t, srv, http.MethodPut, path, createBody)
	if status != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201", status)
	}

	if created.Type != "Microsoft.DigitalTwins/digitalTwinsInstances" {
		t.Errorf("type = %q", created.Type)
	}

	if created.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q", created.Properties.ProvisioningState)
	}

	if created.Properties.HostName != "dt1.api.eastus.digitaltwins.azure.net" {
		t.Errorf("hostName = %q", created.Properties.HostName)
	}

	if created.Identity == nil || created.Identity.PrincipalID == "" || created.Identity.TenantID == "" {
		t.Fatalf("system-assigned identity ids missing: %+v", created.Identity)
	}

	// GET returns identical computed fields (no drift).
	status, got := do(t, srv, http.MethodGet, path, "")
	if status != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", status)
	}

	if got.Properties.HostName != created.Properties.HostName {
		t.Errorf("hostName drift GET: %q vs %q", got.Properties.HostName, created.Properties.HostName)
	}

	if got.Identity.PrincipalID != created.Identity.PrincipalID {
		t.Errorf("principalId drift GET")
	}

	if got.Properties.CreatedTime != created.Properties.CreatedTime {
		t.Errorf("createdTime drift GET")
	}

	// PATCH: change tags. Computed fields must survive.
	patchBody := `{"tags": {"env": "prod"}}`

	status, patched := do(t, srv, http.MethodPatch, path, patchBody)
	if status != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200", status)
	}

	if patched.Tags["env"] != "prod" {
		t.Errorf("PATCH did not apply tags: %+v", patched.Tags)
	}

	if patched.Properties.HostName != created.Properties.HostName ||
		patched.Identity.PrincipalID != created.Identity.PrincipalID ||
		patched.Identity.TenantID != created.Identity.TenantID ||
		patched.Properties.CreatedTime != created.Properties.CreatedTime {
		t.Errorf("PATCH drifted computed fields")
	}

	// PUT again is an update -> 200 (not 201) and preserves computed fields.
	status, reput := do(t, srv, http.MethodPut, path, createBody)
	if status != http.StatusOK {
		t.Errorf("second PUT status = %d, want 200", status)
	}

	if reput.Properties.HostName != created.Properties.HostName {
		t.Errorf("second PUT drifted hostName")
	}

	// DELETE -> 200, then idempotent 204.
	status, _ = do(t, srv, http.MethodDelete, path, "")
	if status != http.StatusOK {
		t.Errorf("DELETE status = %d, want 200", status)
	}

	status, _ = do(t, srv, http.MethodDelete, path, "")
	if status != http.StatusNoContent {
		t.Errorf("second DELETE status = %d, want 204", status)
	}

	status, _ = do(t, srv, http.MethodGet, path, "")
	if status != http.StatusNotFound {
		t.Errorf("GET after delete status = %d, want 404", status)
	}
}

func TestWirePatchOnMissingIs404(t *testing.T) {
	srv := newServer(t)

	status, _ := do(t, srv, http.MethodPatch, basePath+"nope"+apiVer, `{"tags":{"a":"b"}}`)
	if status != http.StatusNotFound {
		t.Errorf("PATCH missing status = %d, want 404", status)
	}
}

func TestWireListByResourceGroup(t *testing.T) {
	srv := newServer(t)

	for _, n := range []string{"a", "b"} {
		status, _ := do(t, srv, http.MethodPut, basePath+n+apiVer, `{"location":"eastus"}`)
		if status != http.StatusCreated {
			t.Fatalf("seed %s: status %d", n, status)
		}
	}

	collection := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.DigitalTwins/digitalTwinsInstances" + apiVer

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+collection, nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var env struct {
		Value []wireResp `json:"value"`
	}

	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode list: %v (%s)", err, raw)
	}

	if len(env.Value) != 2 {
		t.Fatalf("list len = %d, want 2", len(env.Value))
	}
}
