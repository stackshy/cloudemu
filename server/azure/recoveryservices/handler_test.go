package recoveryservices_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/recoveryservices"
	recoveryservicessrv "github.com/stackshy/cloudemu/v2/server/azure/recoveryservices"
)

const (
	apiVer    = "?api-version=2023-04-01"
	vaultBase = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RecoveryServices/vaults/"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := recoveryservices.New(config.NewOptions())
	srv := httptest.NewServer(recoveryservicessrv.New(mock))
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

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, raw)
	}

	return m
}

const vaultBody = `{
	"location": "West US",
	"tags": {"env": "dev"},
	"sku": {"name": "Standard", "tier": "Standard"},
	"identity": {"type": "SystemAssigned"},
	"properties": {"publicNetworkAccess": "Enabled"}
}`

func createVault(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()

	code, raw := do(t, srv, http.MethodPut, vaultBase+"vault1"+apiVer, vaultBody)
	if code != http.StatusCreated {
		t.Fatalf("create vault status = %d, body=%s", code, raw)
	}

	return decode(t, raw)
}

func TestCreateVaultAndByteStableGet(t *testing.T) {
	srv := newServer(t)
	created := createVault(t, srv)

	if created["type"] != "Microsoft.RecoveryServices/vaults" {
		t.Errorf("type = %v", created["type"])
	}

	identity, ok := created["identity"].(map[string]any)
	if !ok || identity["principalId"] == "" || identity["tenantId"] == "" {
		t.Fatalf("identity not populated: %v", created["identity"])
	}

	code1, raw1 := do(t, srv, http.MethodGet, vaultBase+"vault1"+apiVer, "")
	code2, raw2 := do(t, srv, http.MethodGet, vaultBase+"vault1"+apiVer, "")
	if code1 != http.StatusOK || code2 != http.StatusOK {
		t.Fatalf("get status: %d %d", code1, code2)
	}

	if !bytes.Equal(raw1, raw2) {
		t.Errorf("GET not byte-stable:\n1=%s\n2=%s", raw1, raw2)
	}

	props := decode(t, raw1)["properties"].(map[string]any)
	if props["provisioningState"] != "Succeeded" {
		t.Errorf("provisioningState = %v", props["provisioningState"])
	}

	if props["publicNetworkAccess"] != "Enabled" {
		t.Errorf("properties did not round-trip: %v", props)
	}
}

func TestPatchVaultTagsReplace(t *testing.T) {
	srv := newServer(t)
	createVault(t, srv)

	code, raw := do(t, srv, http.MethodPatch, vaultBase+"vault1"+apiVer, `{"tags":{"team":"ops"}}`)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d, body=%s", code, raw)
	}

	tags := decode(t, raw)["tags"].(map[string]any)
	if _, ok := tags["env"]; ok {
		t.Error("PATCH must REPLACE tags")
	}

	if tags["team"] != "ops" {
		t.Errorf("tags = %v", tags)
	}
}

func TestListVaults(t *testing.T) {
	srv := newServer(t)
	createVault(t, srv)

	code, raw := do(t, srv, http.MethodGet,
		"/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RecoveryServices/vaults"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}

	value := decode(t, raw)["value"].([]any)
	if len(value) != 1 {
		t.Errorf("list len = %d", len(value))
	}
}

func TestBackupConfigGetDefaultAndPut(t *testing.T) {
	srv := newServer(t)
	createVault(t, srv)

	cfgPath := vaultBase + "vault1/backupconfig/vaultconfig" + apiVer

	code, raw := do(t, srv, http.MethodGet, cfgPath, "")
	if code != http.StatusOK {
		t.Fatalf("get config status = %d, body=%s", code, raw)
	}

	if decode(t, raw)["type"] != "Microsoft.RecoveryServices/vaults/backupconfig" {
		t.Errorf("config type = %v", decode(t, raw)["type"])
	}

	code, raw = do(t, srv, http.MethodPut, cfgPath, `{"properties":{"softDeleteFeatureState":"Disabled"}}`)
	if code != http.StatusOK {
		t.Fatalf("put config status = %d, body=%s", code, raw)
	}

	props := decode(t, raw)["properties"].(map[string]any)
	if props["softDeleteFeatureState"] != "Disabled" {
		t.Errorf("soft-delete not applied: %v", props)
	}

	if props["storageModelType"] != "GeoRedundant" {
		t.Errorf("config merge dropped default key: %v", props)
	}
}

func TestBackupStorageConfigPatch(t *testing.T) {
	srv := newServer(t)
	createVault(t, srv)

	path := vaultBase + "vault1/backupstorageconfig/vaultstorageconfig" + apiVer

	code, raw := do(t, srv, http.MethodPatch, path, `{"properties":{"storageType":"LocallyRedundant"}}`)
	if code != http.StatusOK {
		t.Fatalf("patch storage config status = %d, body=%s", code, raw)
	}

	props := decode(t, raw)["properties"].(map[string]any)
	if props["storageType"] != "LocallyRedundant" {
		t.Errorf("storage type not applied: %v", props)
	}
}

func TestBackupPolicyLifecycle(t *testing.T) {
	srv := newServer(t)
	createVault(t, srv)

	polPath := vaultBase + "vault1/backupPolicies/pol1" + apiVer
	polBody := `{"properties":{"backupManagementType":"AzureIaasVM","schedulePolicy":{"scheduleRunFrequency":"Daily"}}}`

	code, raw := do(t, srv, http.MethodPut, polPath, polBody)
	if code != http.StatusCreated {
		t.Fatalf("create policy status = %d, body=%s", code, raw)
	}

	if decode(t, raw)["type"] != "Microsoft.RecoveryServices/vaults/backupPolicies" {
		t.Errorf("policy type = %v", decode(t, raw)["type"])
	}

	code, raw = do(t, srv, http.MethodGet, polPath, "")
	if code != http.StatusOK {
		t.Fatalf("get policy status = %d", code)
	}

	props := decode(t, raw)["properties"].(map[string]any)
	if props["backupManagementType"] != "AzureIaasVM" {
		t.Errorf("policy props did not round-trip: %v", props)
	}

	code, raw = do(t, srv, http.MethodGet, vaultBase+"vault1/backupPolicies"+apiVer, "")
	if code != http.StatusOK || len(decode(t, raw)["value"].([]any)) != 1 {
		t.Fatalf("list policies status=%d body=%s", code, raw)
	}

	code, _ = do(t, srv, http.MethodDelete, polPath, "")
	if code != http.StatusOK {
		t.Errorf("delete policy status = %d", code)
	}
}

func TestPolicyUnderMissingVaultParentNotFound(t *testing.T) {
	srv := newServer(t)

	code, _ := do(t, srv, http.MethodPut, vaultBase+"ghost/backupPolicies/pol1"+apiVer, `{"properties":{}}`)
	if code != http.StatusNotFound {
		t.Errorf("want 404 for policy under missing vault, got %d", code)
	}
}

func TestDeleteVaultCascade(t *testing.T) {
	srv := newServer(t)
	createVault(t, srv)

	if code, _ := do(t, srv, http.MethodPut,
		vaultBase+"vault1/backupPolicies/pol1"+apiVer, `{"properties":{}}`); code != http.StatusCreated {
		t.Fatalf("seed policy status = %d", code)
	}

	code, _ := do(t, srv, http.MethodDelete, vaultBase+"vault1"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("delete vault status = %d", code)
	}

	code, _ = do(t, srv, http.MethodGet, vaultBase+"vault1"+apiVer, "")
	if code != http.StatusNotFound {
		t.Errorf("deleted vault GET = %d, want 404", code)
	}

	code, _ = do(t, srv, http.MethodGet, vaultBase+"vault1/backupPolicies/pol1"+apiVer, "")
	if code != http.StatusNotFound {
		t.Errorf("cascaded policy GET = %d, want 404", code)
	}

	code, _ = do(t, srv, http.MethodDelete, vaultBase+"vault1"+apiVer, "")
	if code != http.StatusNoContent {
		t.Errorf("idempotent delete = %d, want 204", code)
	}
}
