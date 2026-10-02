package azure_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

const cpVaultBody = `{"location":"westus","tags":{"k":"v"},"properties":{` +
	`"tenantId":"00000000-0000-0000-0000-000000000001","sku":{"family":"A","name":"premium"},"accessPolicies":[]}}`

func cpPolicy(obj, app string, secrets ...string) string {
	b, _ := json.Marshal(map[string]any{"properties": map[string]any{"accessPolicies": []any{map[string]any{
		"tenantId": "00000000-0000-0000-0000-000000000001", "objectId": obj, "applicationId": app,
		"permissions": map[string]any{"secrets": secrets},
	}}}})

	return string(b)
}

type cpVault struct {
	Tags       map[string]string `json:"tags"`
	Properties struct {
		SKU            struct{ Name string } `json:"sku"`
		TenantID       string                `json:"tenantId"`
		EnableSoft     bool                  `json:"enableSoftDelete"`
		AccessPolicies []struct {
			ObjectID      string `json:"objectId"`
			ApplicationID string `json:"applicationId"`
			Permissions   struct {
				Secrets []string `json:"secrets"`
			} `json:"permissions"`
		} `json:"accessPolicies"`
	} `json:"properties"`
}

func (c *cpClient) vault(path string) cpVault {
	c.t.Helper()

	code, out := c.get(path)
	if code != http.StatusOK {
		c.t.Fatalf("GET vault: %d %s", code, out)
	}

	var v cpVault
	if err := json.Unmarshal(out, &v); err != nil {
		c.t.Fatal(err)
	}

	return v
}

// TestVaultAccessPoliciesWire is AZKV-01: accessPolicies/{kind} changes only
// the policy list, never the vault's sku, tags or tenant.
func TestVaultAccessPoliciesWire(t *testing.T) {
	c := newCPClient(t)
	kv := cpRes("Microsoft.KeyVault/vaults", "kv1")
	c.mustPut(kv, cpVaultBody)

	steps := []struct {
		kind, body string
		status     int
		secrets    []string // obj-1 secrets after; nil = entry absent
	}{
		{"add", cpPolicy("obj-1", "", "Get"), http.StatusOK, []string{"Get"}},
		{"add", cpPolicy("obj-1", "", "list", "get"), http.StatusOK, []string{"Get", "list"}},
		{"replace", cpPolicy("obj-1", "", "Set"), http.StatusOK, []string{"Set"}},
		{"remove", cpPolicy("obj-1", "", "Set"), http.StatusOK, nil},
		{"merge", cpPolicy("obj-1", "", "Get"), http.StatusBadRequest, nil},
	}

	for _, st := range steps {
		code, out := c.do(http.MethodPut, kv+"/accessPolicies/"+st.kind, st.body)
		if code != st.status {
			t.Fatalf("PUT %s: %d %s", st.kind, code, out)
		}

		v := c.vault(kv)
		if v.Properties.SKU.Name != "premium" || v.Tags["k"] != "v" || v.Properties.TenantID == "" || !v.Properties.EnableSoft {
			t.Errorf("%s: vault properties changed: %+v", st.kind, v)
		}

		var got []string
		for _, p := range v.Properties.AccessPolicies {
			if p.ObjectID == "obj-1" {
				got = p.Permissions.Secrets
			}
		}

		if len(got) != len(st.secrets) {
			t.Errorf("%s: obj-1 secrets = %v, want %v", st.kind, got, st.secrets)
		}
	}

	if code, _ := c.do(http.MethodPut, kv+"/accessPolicies/add", cpPolicy("obj-2", "app-2", "Get")); code != http.StatusOK {
		t.Fatal(code)
	}

	// PATCH tags keeps the applicationId-bearing entry (copyAccessPolicies).
	if code, out := c.do(http.MethodPatch, kv, `{"tags":{"k":"v2"}}`); code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", code, out)
	}

	v := c.vault(kv)
	if len(v.Properties.AccessPolicies) != 1 || v.Properties.AccessPolicies[0].ApplicationID != "app-2" || v.Tags["k"] != "v2" {
		t.Errorf("after PATCH: %+v", v)
	}

	if code, _ := c.do(http.MethodPut, cpRes("Microsoft.KeyVault/vaults", "nope")+"/accessPolicies/add",
		cpPolicy("o", "", "Get")); code != http.StatusNotFound {
		t.Errorf("missing vault: %d", code)
	}
}

// TestVaultPatchInvariants pins R3.1: PATCH cannot disable soft delete and a
// wrong resource group is 404 with the vault untouched.
func TestVaultPatchInvariants(t *testing.T) {
	c := newCPClient(t)
	kv := cpRes("Microsoft.KeyVault/vaults", "kv1")
	c.mustPut(kv, cpVaultBody)

	if code, _ := c.do(http.MethodPatch, kv, `{"properties":{"enableSoftDelete":false}}`); code != http.StatusOK {
		t.Fatal(code)
	}

	if !c.vault(kv).Properties.EnableSoft {
		t.Error("PATCH disabled soft delete")
	}

	_, before := c.get(kv)
	c.mustPut("/subscriptions/"+cpSub+"/resourceGroups/rg2", `{"location":"westus"}`)

	wrong := "/subscriptions/" + cpSub + "/resourceGroups/rg2/providers/Microsoft.KeyVault/vaults/kv1"
	if code, _ := c.do(http.MethodPatch, wrong, `{"tags":{"x":"y"}}`); code != http.StatusNotFound {
		t.Errorf("wrong RG PATCH: %d, want 404", code)
	}

	if _, after := c.get(kv); !bytes.Equal(before, after) {
		t.Errorf("wrong-RG PATCH changed the vault:\n%s\n%s", before, after)
	}
}

// TestDeletedVaultsStub is AZKV-06b: the soft-delete probe azurerm_key_vault
// create makes is 404, and lists are empty.
func TestDeletedVaultsStub(t *testing.T) {
	c := newCPClient(t)
	base := "/subscriptions/" + cpSub + "/providers/Microsoft.KeyVault"

	cases := []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, base + "/locations/westeurope/deletedVaults/kv1", http.StatusNotFound},
		{http.MethodGet, base + "/locations/westeurope/deletedVaults", http.StatusOK},
		{http.MethodGet, base + "/deletedVaults", http.StatusOK},
		{http.MethodPost, base + "/locations/westeurope/deletedVaults/kv1/purge", http.StatusNotFound},
	}

	for _, tc := range cases {
		if code, out := c.do(tc.method, tc.path, ""); code != tc.status {
			t.Errorf("%s %s: %d %s, want %d", tc.method, tc.path, code, out, tc.status)
		}
	}
}

// TestFederatedIdentityCredentialsWire is AZKV-02: credential CRUD under an
// identity, which itself is never changed.
func TestFederatedIdentityCredentialsWire(t *testing.T) {
	c := newCPClient(t)
	id := cpRes("Microsoft.ManagedIdentity/userAssignedIdentities", "id1")
	c.mustPut(id, cpTagged)
	_, before := c.get(id)

	fic := id + "/federatedIdentityCredentials/gh-main"
	body := `{"properties":{"issuer":"https://token.actions.githubusercontent.com",` +
		`"subject":"repo:o/r:ref:refs/heads/main","audiences":["api://AzureADTokenExchange"]}}`

	cases := []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{http.MethodPut, fic, body, http.StatusCreated, `"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials"`},
		{http.MethodPut, fic, body, http.StatusOK, `"subject":"repo:o/r:ref:refs/heads/main"`},
		{http.MethodGet, fic, "", http.StatusOK, `"audiences":["api://AzureADTokenExchange"]`},
		{http.MethodGet, id + "/federatedIdentityCredentials", "", http.StatusOK, `"name":"gh-main"`},
		{http.MethodPut, id + "/federatedIdentityCredentials/dup", body, http.StatusConflict, ""},
		{http.MethodPut, id + "/federatedIdentityCredentials/x", body, http.StatusBadRequest, ""},
		{http.MethodPut, id + "/federatedIdentityCredentials/noaud", `{"properties":{"issuer":"https://a",` +
			`"subject":"s","audiences":[]}}`, http.StatusBadRequest, ""},
		{http.MethodPut, cpRes("Microsoft.ManagedIdentity/userAssignedIdentities", "ghost") +
			"/federatedIdentityCredentials/gh-main", body, http.StatusNotFound, "ParentResourceNotFound"},
		{http.MethodDelete, fic, "", http.StatusOK, ""},
		{http.MethodDelete, fic, "", http.StatusNoContent, ""},
		{http.MethodGet, fic, "", http.StatusNotFound, ""},
	}

	for _, tc := range cases {
		code, out := c.do(tc.method, tc.path, tc.body)
		if code != tc.status || !bytes.Contains(out, []byte(tc.contains)) {
			t.Errorf("%s %s: %d %s, want %d containing %q", tc.method, tc.path, code, out, tc.status, tc.contains)
		}
	}

	if _, after := c.get(id); !bytes.Equal(before, after) {
		t.Errorf("identity changed by its credentials:\n%s\n%s", before, after)
	}
}

// TestSQLRetentionWire is AZDB-01 and AZDB-N1: retention policies are their
// own resources (a DELETE never reaches the database) and connectionPolicies
// round-trips.
func TestSQLRetentionWire(t *testing.T) {
	c := newCPClient(t)
	srv := cpRes("Microsoft.Sql/servers", "sql1")
	db := srv + "/databases/db1"

	c.mustPut(srv, `{"location":"westus","properties":{"administratorLogin":"a",`+
		`"administratorLoginPassword":"P@ssw0rd1234!","version":"12.0"}}`)
	c.mustPut(db, `{"location":"westus","properties":{}}`)

	str := db + "/backupShortTermRetentionPolicies/default"
	cases := []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{http.MethodPut, str, `{"properties":{"retentionDays":14,"diffBackupIntervalInHours":24}}`, http.StatusOK, `"retentionDays":14`},
		{http.MethodPatch, str, `{"properties":{"retentionDays":21}}`, http.StatusOK, `"diffBackupIntervalInHours":24`},
		{http.MethodGet, str, "", http.StatusOK, `"retentionDays":21`},
		{http.MethodPut, str, `{"properties":{"retentionDays":40}}`, http.StatusBadRequest, ""},
		{http.MethodDelete, str, "", http.StatusMethodNotAllowed, ""},
		{http.MethodGet, db, "", http.StatusOK, `"name":"db1"`},
		{http.MethodGet, db + "/backupShortTermRetentionPolicies", "", http.StatusOK, `"retentionDays":21`},
		{http.MethodPut, db + "/backupLongTermRetentionPolicies/default", `{"properties":{"weeklyRetention":"P2W"}}`,
			http.StatusOK, `"monthlyRetention":"PT0S"`},
		{http.MethodGet, db + "/backupLongTermRetentionPolicies/default", "", http.StatusOK, `"weeklyRetention":"P2W"`},
		{http.MethodGet, db + "/backupShortTermRetentionPolicies/other", "", http.StatusNotFound, ""},
		{http.MethodPut, srv + "/connectionPolicies/default", `{"properties":{"connectionType":"Proxy"}}`,
			http.StatusOK, `"connectionType":"Proxy"`},
		{http.MethodGet, srv + "/connectionPolicies/default", "", http.StatusOK, `"connectionType":"Proxy"`},
		{http.MethodPut, srv + "/connectionPolicies/default", `{"properties":{"connectionType":"Fast"}}`,
			http.StatusBadRequest, ""},
	}

	for _, tc := range cases {
		code, out := c.do(tc.method, tc.path, tc.body)
		if code != tc.status || !bytes.Contains(out, []byte(tc.contains)) {
			t.Errorf("%s %s: %d %s, want %d containing %q", tc.method, tc.path, code, out, tc.status, tc.contains)
		}
	}

	if code, _ := c.do(http.MethodDelete, db, ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("delete db: %d", code)
	}

	c.mustPut(db, `{"location":"westus","properties":{}}`)

	if _, out := c.get(str); !bytes.Contains(out, []byte(`"retentionDays":7`)) {
		t.Errorf("recreated database inherited the old policy: %s", out)
	}
}
