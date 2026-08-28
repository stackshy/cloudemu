package keyvault_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	vaultSub    = "sub-kv"
	vaultRG     = "rg-kv"
	vaultAPIVer = "2023-07-01"
)

// newVaultServer builds a fully-wired Azure server (ARM + Resource Graph) from a
// fresh provider.
func newVaultServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts := httptest.NewServer(azureserver.NewFromProvider(cloudemu.NewAzure()))
	t.Cleanup(ts.Close)

	return ts
}

func vaultURL(base, sub, rg, name string) string {
	u := base + "/subscriptions/" + sub + "/resourceGroups/" + rg +
		"/providers/Microsoft.KeyVault/vaults"
	if name != "" {
		u += "/" + name
	}

	return u + "?api-version=" + vaultAPIVer
}

// doJSON issues an ARM request and returns the status and decoded body.
func doJSON(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}

		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var decoded map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}

	return resp.StatusCode, decoded
}

func vaultCreateBody() map[string]any {
	return map[string]any{
		"location": "westus2",
		"tags":     map[string]any{"env": "test"},
		"properties": map[string]any{
			"tenantId": "11111111-1111-1111-1111-111111111111",
			"sku":      map[string]any{"family": "A", "name": "premium"},
			"accessPolicies": []any{
				map[string]any{
					"tenantId": "11111111-1111-1111-1111-111111111111",
					"objectId": "22222222-2222-2222-2222-222222222222",
					"permissions": map[string]any{
						"keys":    []any{"get", "list"},
						"secrets": []any{"get"},
					},
				},
			},
			"enableRbacAuthorization":   false,
			"enableSoftDelete":          true,
			"softDeleteRetentionInDays": float64(90),
			"enablePurgeProtection":     true,
			"publicNetworkAccess":       "Enabled",
		},
	}
}

func TestVaultARMLifecycle(t *testing.T) {
	ts := newVaultServer(t)
	base := ts.URL

	// Create.
	status, body := doJSON(t, http.MethodPut, vaultURL(base, vaultSub, vaultRG, "vault1"), vaultCreateBody())
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%v", status, body)
	}

	if body["type"] != "Microsoft.KeyVault/vaults" {
		t.Errorf("type = %v, want Microsoft.KeyVault/vaults", body["type"])
	}

	if body["name"] != "vault1" {
		t.Errorf("name = %v, want vault1", body["name"])
	}

	props, _ := body["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("no properties in create response: %v", body)
	}

	if props["provisioningState"] != "Succeeded" {
		t.Errorf("provisioningState = %v, want Succeeded", props["provisioningState"])
	}

	if sku, _ := props["sku"].(map[string]any); sku == nil || sku["name"] != "premium" {
		t.Errorf("sku = %v, want premium", props["sku"])
	}

	if props["vaultUri"] != "https://vault1.vault.azure.net/" {
		t.Errorf("vaultUri = %v", props["vaultUri"])
	}

	if pol, _ := props["accessPolicies"].([]any); len(pol) != 1 {
		t.Errorf("accessPolicies = %v, want 1 entry", props["accessPolicies"])
	}

	// Get.
	status, got := doJSON(t, http.MethodGet, vaultURL(base, vaultSub, vaultRG, "vault1"), nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d, want 200", status)
	}

	gp, _ := got["properties"].(map[string]any)
	if gp["tenantId"] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("get tenantId = %v", gp["tenantId"])
	}

	if gp["enablePurgeProtection"] != true {
		t.Errorf("get enablePurgeProtection = %v, want true", gp["enablePurgeProtection"])
	}

	// A second PUT (replace) returns 200, not 201.
	status, _ = doJSON(t, http.MethodPut, vaultURL(base, vaultSub, vaultRG, "vault1"), vaultCreateBody())
	if status != http.StatusOK {
		t.Fatalf("replace status = %d, want 200", status)
	}

	// List by resource group.
	status, list := doJSON(t, http.MethodGet, vaultURL(base, vaultSub, vaultRG, ""), nil)
	if status != http.StatusOK {
		t.Fatalf("list-by-rg status = %d, want 200", status)
	}

	if vals, _ := list["value"].([]any); len(vals) != 1 {
		t.Errorf("list-by-rg = %v, want 1 vault", list["value"])
	}

	// List by subscription.
	subURL := base + "/subscriptions/" + vaultSub + "/providers/Microsoft.KeyVault/vaults?api-version=" + vaultAPIVer
	status, subList := doJSON(t, http.MethodGet, subURL, nil)
	if status != http.StatusOK {
		t.Fatalf("list-by-sub status = %d, want 200", status)
	}

	if vals, _ := subList["value"].([]any); len(vals) != 1 {
		t.Errorf("list-by-sub = %v, want 1 vault", subList["value"])
	}

	// Delete.
	status, _ = doJSON(t, http.MethodDelete, vaultURL(base, vaultSub, vaultRG, "vault1"), nil)
	if status != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", status)
	}

	// Get after delete → 404.
	status, _ = doJSON(t, http.MethodGet, vaultURL(base, vaultSub, vaultRG, "vault1"), nil)
	if status != http.StatusNotFound {
		t.Errorf("get-after-delete status = %d, want 404", status)
	}
}

func TestVaultARMScopeMismatch(t *testing.T) {
	ts := newVaultServer(t)
	base := ts.URL

	status, _ := doJSON(t, http.MethodPut, vaultURL(base, vaultSub, vaultRG, "vault2"), vaultCreateBody())
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", status)
	}

	// GET via a different resource group must 404 (the id would contradict the path).
	status, _ = doJSON(t, http.MethodGet, vaultURL(base, vaultSub, "other-rg", "vault2"), nil)
	if status != http.StatusNotFound {
		t.Errorf("cross-rg get status = %d, want 404", status)
	}
}

// TestVaultARMUnmodeledPropertyPreserved exercises #409 gap #4 through the vault
// resource: an unmodeled request property under properties survives PUT->GET via
// the server's echoUnmodeledProperties overlay.
func TestVaultARMUnmodeledPropertyPreserved(t *testing.T) {
	ts := newVaultServer(t)
	base := ts.URL

	body := vaultCreateBody()
	props, _ := body["properties"].(map[string]any)
	props["networkAcls"] = map[string]any{
		"defaultAction": "Deny",
		"bypass":        "AzureServices",
	}

	status, created := doJSON(t, http.MethodPut, vaultURL(base, vaultSub, vaultRG, "vault3"), body)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", status)
	}

	if !hasNetworkACLs(created) {
		t.Errorf("unmodeled networkAcls dropped on create response: %v", created["properties"])
	}

	status, got := doJSON(t, http.MethodGet, vaultURL(base, vaultSub, vaultRG, "vault3"), nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d, want 200", status)
	}

	if !hasNetworkACLs(got) {
		t.Errorf("unmodeled networkAcls not preserved on GET: %v", got["properties"])
	}
}

func hasNetworkACLs(resource map[string]any) bool {
	props, _ := resource["properties"].(map[string]any)
	acls, _ := props["networkAcls"].(map[string]any)

	return acls["defaultAction"] == "Deny"
}

// TestVaultInResourceGraph verifies an ARM-created vault surfaces in Resource
// Graph as microsoft.keyvault/vaults.
func TestVaultInResourceGraph(t *testing.T) {
	ts := newVaultServer(t)
	base := ts.URL

	status, _ := doJSON(t, http.MethodPut, vaultURL(base, vaultSub, vaultRG, "vault-arg"), vaultCreateBody())
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", status)
	}

	argURL := base + "/providers/Microsoft.ResourceGraph/resources?api-version=2021-03-01"
	query := map[string]any{
		"subscriptions": []string{vaultSub},
		"query":         "Resources | where type == 'microsoft.keyvault/vaults'",
	}

	status, resp := doJSON(t, http.MethodPost, argURL, query)
	if status != http.StatusOK {
		t.Fatalf("ARG status = %d, want 200; body=%v", status, resp)
	}

	data, _ := resp["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("ARG returned %d rows, want 1; resp=%v", len(data), resp)
	}

	row, _ := data[0].(map[string]any)
	if row["type"] != "microsoft.keyvault/vaults" {
		t.Errorf("ARG row type = %v, want microsoft.keyvault/vaults", row["type"])
	}

	if row["name"] != "vault-arg" {
		t.Errorf("ARG row name = %v, want vault-arg", row["name"])
	}
}
