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

// TestVaultARMMinimalCreateDefaults verifies a create body that omits the
// soft-delete, RBAC, and enabledFor* flags round-trips (on both the PUT response
// and a follow-up GET) with the defaults real Azure Key Vault stamps on the
// vault: enableSoftDelete=true, softDeleteRetentionInDays=90, and
// enableRbacAuthorization / enabledForDeployment / enabledForDiskEncryption /
// enabledForTemplateDeployment all present=false. A raw armkeyvault SDK user or a
// Terraform azurerm_key_vault refresh would otherwise see these fields absent and
// drift.
func TestVaultARMMinimalCreateDefaults(t *testing.T) {
	ts := newVaultServer(t)
	base := ts.URL

	minimal := map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"tenantId": "11111111-1111-1111-1111-111111111111",
			"sku":      map[string]any{"family": "A", "name": "standard"},
		},
	}

	assertDefaults := func(label string, props map[string]any) {
		if props["enableSoftDelete"] != true {
			t.Errorf("%s enableSoftDelete = %v, want true", label, props["enableSoftDelete"])
		}

		if props["softDeleteRetentionInDays"] != float64(90) {
			t.Errorf("%s softDeleteRetentionInDays = %v, want 90", label, props["softDeleteRetentionInDays"])
		}

		for _, flag := range []string{
			"enableRbacAuthorization",
			"enabledForDeployment",
			"enabledForDiskEncryption",
			"enabledForTemplateDeployment",
		} {
			if props[flag] != false {
				t.Errorf("%s %s = %v, want false (present, not absent)", label, flag, props[flag])
			}
		}
	}

	status, body := doJSON(t, http.MethodPut, vaultURL(base, vaultSub, vaultRG, "minivault"), minimal)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%v", status, body)
	}

	createProps, _ := body["properties"].(map[string]any)
	if createProps == nil {
		t.Fatalf("no properties in create response: %v", body)
	}

	assertDefaults("create", createProps)

	status, got := doJSON(t, http.MethodGet, vaultURL(base, vaultSub, vaultRG, "minivault"), nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d, want 200", status)
	}

	getProps, _ := got["properties"].(map[string]any)
	assertDefaults("get", getProps)
}

// TestVaultARMSoftDeleteCannotBeDisabled verifies Azure's mandatory soft-delete
// over the wire: a create body that explicitly sets enableSoftDelete=false is
// overridden to true, and a later PATCH setting it false cannot revert the vault
// ("Once soft-delete is enabled on a key vault, it can't be disabled.").
func TestVaultARMSoftDeleteCannotBeDisabled(t *testing.T) {
	ts := newVaultServer(t)
	base := ts.URL

	create := map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"tenantId":         "11111111-1111-1111-1111-111111111111",
			"sku":              map[string]any{"family": "A", "name": "standard"},
			"enableSoftDelete": false,
		},
	}

	status, body := doJSON(t, http.MethodPut, vaultURL(base, vaultSub, vaultRG, "sd-vault"), create)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%v", status, body)
	}

	createProps, _ := body["properties"].(map[string]any)
	if createProps["enableSoftDelete"] != true {
		t.Errorf("create enableSoftDelete = %v, want true (explicit false must be forced on)", createProps["enableSoftDelete"])
	}

	status, got := doJSON(t, http.MethodGet, vaultURL(base, vaultSub, vaultRG, "sd-vault"), nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d, want 200", status)
	}

	if gp, _ := got["properties"].(map[string]any); gp["enableSoftDelete"] != true {
		t.Errorf("get enableSoftDelete = %v, want true", gp["properties"])
	}

	// PATCH attempting to turn soft-delete off must be ignored.
	status, patched := doJSON(t, http.MethodPatch, vaultURL(base, vaultSub, vaultRG, "sd-vault"),
		map[string]any{"properties": map[string]any{"enableSoftDelete": false}})
	if status != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%v", status, patched)
	}

	if pp, _ := patched["properties"].(map[string]any); pp["enableSoftDelete"] != true {
		t.Errorf("patch enableSoftDelete = %v, want true (cannot revert)", pp["enableSoftDelete"])
	}

	status, after := doJSON(t, http.MethodGet, vaultURL(base, vaultSub, vaultRG, "sd-vault"), nil)
	if status != http.StatusOK {
		t.Fatalf("get-after-patch status = %d, want 200", status)
	}

	if ap, _ := after["properties"].(map[string]any); ap["enableSoftDelete"] != true {
		t.Errorf("get-after-patch enableSoftDelete = %v, want true", ap["enableSoftDelete"])
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

// TestVaultARMUpdatePartialMerge exercises PATCH — Vaults.Update. Unlike PUT,
// a PATCH carrying only one property must merge onto the stored vault rather
// than replacing it: every other property (tenantId, sku, accessPolicies,
// soft-delete flags) must survive untouched.
func TestVaultARMUpdatePartialMerge(t *testing.T) {
	ts := newVaultServer(t)
	base := ts.URL

	status, _ := doJSON(t, http.MethodPut, vaultURL(base, vaultSub, vaultRG, "vault-patch"), vaultCreateBody())
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", status)
	}

	patchBody := map[string]any{
		"properties": map[string]any{
			"enabledForDeployment": true,
		},
	}

	status, patched := doJSON(t, http.MethodPatch, vaultURL(base, vaultSub, vaultRG, "vault-patch"), patchBody)
	if status != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%v", status, patched)
	}

	props, _ := patched["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("no properties in patch response: %v", patched)
	}

	if props["enabledForDeployment"] != true {
		t.Errorf("enabledForDeployment = %v, want true", props["enabledForDeployment"])
	}

	// Fields the PATCH body never mentioned must be untouched.
	if props["tenantId"] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("patch dropped tenantId: %v", props["tenantId"])
	}

	if sku, _ := props["sku"].(map[string]any); sku == nil || sku["name"] != "premium" {
		t.Errorf("patch dropped sku: %v", props["sku"])
	}

	if pol, _ := props["accessPolicies"].([]any); len(pol) != 1 {
		t.Errorf("patch dropped accessPolicies: %v", props["accessPolicies"])
	}

	if props["enablePurgeProtection"] != true {
		t.Errorf("patch dropped enablePurgeProtection: %v", props["enablePurgeProtection"])
	}

	if tags, _ := patched["tags"].(map[string]any); tags["env"] != "test" {
		t.Errorf("patch dropped untouched tags: %v", patched["tags"])
	}

	// A GET afterward must reflect the same merged state.
	status, got := doJSON(t, http.MethodGet, vaultURL(base, vaultSub, vaultRG, "vault-patch"), nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d, want 200", status)
	}

	gp, _ := got["properties"].(map[string]any)
	if gp["enabledForDeployment"] != true {
		t.Errorf("get after patch enabledForDeployment = %v, want true", gp["enabledForDeployment"])
	}
}

// TestVaultARMUpdateTagsFullReplace confirms a PATCH carrying tags replaces
// the whole tag set (real ARM PATCH semantics), while properties it does not
// mention stay untouched.
func TestVaultARMUpdateTagsFullReplace(t *testing.T) {
	ts := newVaultServer(t)
	base := ts.URL

	status, _ := doJSON(t, http.MethodPut, vaultURL(base, vaultSub, vaultRG, "vault-tags"), vaultCreateBody())
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", status)
	}

	status, patched := doJSON(t, http.MethodPatch, vaultURL(base, vaultSub, vaultRG, "vault-tags"),
		map[string]any{"tags": map[string]any{"team": "core"}})
	if status != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", status)
	}

	tags, _ := patched["tags"].(map[string]any)
	if len(tags) != 1 || tags["team"] != "core" {
		t.Fatalf("patched tags = %v, want exactly {team: core}", patched["tags"])
	}

	props, _ := patched["properties"].(map[string]any)
	if props["tenantId"] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("tags-only patch dropped tenantId: %v", props["tenantId"])
	}
}

// TestVaultARMUpdateMissingVaultIs404 confirms PATCH on a vault name that was
// never created 404s the same way GET/DELETE do.
func TestVaultARMUpdateMissingVaultIs404(t *testing.T) {
	ts := newVaultServer(t)
	base := ts.URL

	status, _ := doJSON(t, http.MethodPatch, vaultURL(base, vaultSub, vaultRG, "no-such-vault"),
		map[string]any{"tags": map[string]any{"a": "b"}})
	if status != http.StatusNotFound {
		t.Errorf("patch-missing status = %d, want 404", status)
	}
}

// TestVaultARMUpdateScopeMismatchIs404 mirrors TestVaultARMScopeMismatch for
// PATCH: a vault addressed through the wrong resource group must 404 rather
// than silently updating (or relocating) it.
func TestVaultARMUpdateScopeMismatchIs404(t *testing.T) {
	ts := newVaultServer(t)
	base := ts.URL

	status, _ := doJSON(t, http.MethodPut, vaultURL(base, vaultSub, vaultRG, "vault-scope-patch"), vaultCreateBody())
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", status)
	}

	status, _ = doJSON(t, http.MethodPatch, vaultURL(base, vaultSub, "other-rg", "vault-scope-patch"),
		map[string]any{"tags": map[string]any{"a": "b"}})
	if status != http.StatusNotFound {
		t.Errorf("cross-rg patch status = %d, want 404", status)
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
