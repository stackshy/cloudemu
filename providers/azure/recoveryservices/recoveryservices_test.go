package recoveryservices_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/recoveryservices"
)

func newMock() *recoveryservices.Mock {
	return recoveryservices.New(config.NewOptions())
}

func sptr(v string) *string { return &v }

func standardVault() *recoveryservices.VaultInput {
	return &recoveryservices.VaultInput{
		Tags:     map[string]string{"env": "dev"},
		SkuName:  sptr("Standard"),
		Identity: &recoveryservices.ManagedIdentity{Type: "SystemAssigned"},
	}
}

func createVault(t *testing.T, m *recoveryservices.Mock) recoveryservices.Vault {
	t.Helper()

	v, created, err := m.CreateOrUpdateVault(context.Background(), "sub", "rg", "vault1", "West US", standardVault())
	if err != nil || !created {
		t.Fatalf("create vault: err=%v created=%v", err, created)
	}

	return v
}

func TestCreateVaultComputedFields(t *testing.T) {
	m := newMock()
	v := createVault(t, m)

	if v.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", v.ProvisioningState)
	}

	if v.Etag == "" {
		t.Error("etag not minted")
	}

	if v.SkuName != "Standard" {
		t.Errorf("sku = %q, want Standard", v.SkuName)
	}

	if v.Identity == nil || v.Identity.PrincipalID == "" || v.Identity.TenantID == "" {
		t.Fatalf("system-assigned identity not synthesized: %+v", v.Identity)
	}

	if !strings.HasPrefix(v.ARMID(), "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RecoveryServices/vaults/vault1") {
		t.Errorf("unexpected ARM id %q", v.ARMID())
	}
}

func TestIdentityPrincipalStableAcrossReads(t *testing.T) {
	m := newMock()
	created := createVault(t, m)

	g1, err := m.GetVault(context.Background(), "sub", "rg", "vault1")
	if err != nil {
		t.Fatalf("get1: %v", err)
	}

	g2, err := m.GetVault(context.Background(), "sub", "rg", "vault1")
	if err != nil {
		t.Fatalf("get2: %v", err)
	}

	if created.Identity.PrincipalID != g1.Identity.PrincipalID || g1.Identity.PrincipalID != g2.Identity.PrincipalID {
		t.Errorf("principalId drifted: create=%s g1=%s g2=%s",
			created.Identity.PrincipalID, g1.Identity.PrincipalID, g2.Identity.PrincipalID)
	}

	if created.Etag != g2.Etag {
		t.Errorf("etag drifted: %s vs %s", created.Etag, g2.Etag)
	}
}

func TestUpdateVaultTagsReplace(t *testing.T) {
	m := newMock()
	createVault(t, m)

	in := &recoveryservices.VaultInput{Tags: map[string]string{"team": "ops"}}

	v, err := m.UpdateVault(context.Background(), "sub", "rg", "vault1", in)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if _, ok := v.Tags["env"]; ok {
		t.Error("resource-level PATCH must REPLACE tags, not merge")
	}

	if v.Tags["team"] != "ops" {
		t.Errorf("tags not replaced: %v", v.Tags)
	}
}

func TestUpdateVaultPropertiesMerge(t *testing.T) {
	m := newMock()

	in := standardVault()
	in.Properties = json.RawMessage(`{"publicNetworkAccess":"Enabled"}`)

	if _, _, err := m.CreateOrUpdateVault(context.Background(), "sub", "rg", "vault1", "West US", in); err != nil {
		t.Fatalf("create: %v", err)
	}

	patch := &recoveryservices.VaultInput{Properties: json.RawMessage(`{"monitoringSettings":{"a":1}}`)}

	v, err := m.UpdateVault(context.Background(), "sub", "rg", "vault1", patch)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	var props map[string]json.RawMessage
	if err := json.Unmarshal(v.Properties, &props); err != nil {
		t.Fatalf("unmarshal props: %v", err)
	}

	if _, ok := props["publicNetworkAccess"]; !ok {
		t.Error("PATCH dropped an existing property key")
	}

	if _, ok := props["monitoringSettings"]; !ok {
		t.Error("PATCH did not merge the new property key")
	}
}

func TestUpdateMissingVaultNotFound(t *testing.T) {
	m := newMock()

	_, err := m.UpdateVault(context.Background(), "sub", "rg", "ghost", &recoveryservices.VaultInput{})
	if !cerrors.IsNotFound(err) {
		t.Errorf("want NotFound, got %v", err)
	}
}

func TestListVaults(t *testing.T) {
	m := newMock()
	createVault(t, m)

	if _, _, err := m.CreateOrUpdateVault(context.Background(), "sub", "rg", "vault2", "West US", standardVault()); err != nil {
		t.Fatalf("create vault2: %v", err)
	}

	byRG, err := m.ListVaultsByResourceGroup(context.Background(), "sub", "rg")
	if err != nil || len(byRG) != 2 {
		t.Fatalf("list by rg: err=%v n=%d", err, len(byRG))
	}

	bySub, err := m.ListVaultsBySubscription(context.Background(), "sub")
	if err != nil || len(bySub) != 2 {
		t.Fatalf("list by sub: err=%v n=%d", err, len(bySub))
	}
}

func TestDeleteVaultCascadesChildren(t *testing.T) {
	m := newMock()
	createVault(t, m)

	if _, _, err := m.CreateOrUpdatePolicy(
		context.Background(), "sub", "rg", "vault1", "pol1", json.RawMessage(`{"backupManagementType":"AzureIaasVM"}`),
	); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	if _, err := m.UpdateVaultConfig(
		context.Background(), "sub", "rg", "vault1", json.RawMessage(`{"softDeleteFeatureState":"Disabled"}`),
	); err != nil {
		t.Fatalf("set config: %v", err)
	}

	existed, err := m.DeleteVault(context.Background(), "sub", "rg", "vault1")
	if err != nil || !existed {
		t.Fatalf("delete vault: err=%v existed=%v", err, existed)
	}

	if _, err := m.GetPolicy(context.Background(), "sub", "rg", "vault1", "pol1"); !cerrors.IsNotFound(err) {
		t.Errorf("policy should be cascaded, got %v", err)
	}

	if _, err := m.GetVaultConfig(context.Background(), "sub", "rg", "vault1"); !cerrors.IsNotFound(err) {
		t.Errorf("config parent should be gone, got %v", err)
	}
}

func TestPolicyRoundTripAndParentGuard(t *testing.T) {
	m := newMock()

	props := json.RawMessage(`{"backupManagementType":"AzureIaasVM","schedulePolicy":{"scheduleRunFrequency":"Daily"}}`)

	if _, _, err := m.CreateOrUpdatePolicy(
		context.Background(), "sub", "rg", "ghost", "pol1", props); !cerrors.IsNotFound(err) {
		t.Errorf("policy under missing vault should be NotFound, got %v", err)
	}

	createVault(t, m)

	p, created, err := m.CreateOrUpdatePolicy(context.Background(), "sub", "rg", "vault1", "pol1", props)
	if err != nil || !created {
		t.Fatalf("create policy: err=%v created=%v", err, created)
	}

	if string(p.Properties) != string(props) {
		t.Errorf("policy properties did not round-trip verbatim: %s", p.Properties)
	}

	if p.Etag == "" {
		t.Error("policy etag not minted")
	}

	got, err := m.GetPolicy(context.Background(), "sub", "rg", "vault1", "pol1")
	if err != nil || got.Etag != p.Etag {
		t.Fatalf("get policy: err=%v etag=%s want=%s", err, got.Etag, p.Etag)
	}

	list, err := m.ListPolicies(context.Background(), "sub", "rg", "vault1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list policies: err=%v n=%d", err, len(list))
	}
}

func TestVaultConfigDefaultAndMerge(t *testing.T) {
	m := newMock()
	createVault(t, m)

	def, err := m.GetVaultConfig(context.Background(), "sub", "rg", "vault1")
	if err != nil {
		t.Fatalf("get default config: %v", err)
	}

	if !strings.Contains(string(def.Properties), `"softDeleteFeatureState":"Enabled"`) {
		t.Errorf("default config missing soft-delete Enabled: %s", def.Properties)
	}

	set, err := m.UpdateVaultConfig(
		context.Background(), "sub", "rg", "vault1", json.RawMessage(`{"softDeleteFeatureState":"Disabled"}`))
	if err != nil {
		t.Fatalf("set config: %v", err)
	}

	if !strings.Contains(string(set.Properties), `"softDeleteFeatureState":"Disabled"`) {
		t.Errorf("config not updated: %s", set.Properties)
	}

	if !strings.Contains(string(set.Properties), `"storageModelType":"GeoRedundant"`) {
		t.Errorf("config merge dropped a default key: %s", set.Properties)
	}
}

func TestStorageConfigDefaultAndMerge(t *testing.T) {
	m := newMock()
	createVault(t, m)

	c, err := m.UpdateStorageConfig(
		context.Background(), "sub", "rg", "vault1", json.RawMessage(`{"storageType":"LocallyRedundant"}`))
	if err != nil {
		t.Fatalf("set storage config: %v", err)
	}

	if !strings.Contains(string(c.Properties), `"storageType":"LocallyRedundant"`) {
		t.Errorf("storage type not updated: %s", c.Properties)
	}

	if !strings.Contains(string(c.Properties), `"crossRegionRestoreFlag":false`) {
		t.Errorf("storage config merge dropped a default key: %s", c.Properties)
	}
}

func TestConfigOnMissingVaultNotFound(t *testing.T) {
	m := newMock()

	if _, err := m.GetVaultConfig(context.Background(), "sub", "rg", "ghost"); !cerrors.IsNotFound(err) {
		t.Errorf("want NotFound, got %v", err)
	}

	if _, err := m.GetStorageConfig(context.Background(), "sub", "rg", "ghost"); !cerrors.IsNotFound(err) {
		t.Errorf("want NotFound, got %v", err)
	}
}

func TestPurgeResourceGroup(t *testing.T) {
	m := newMock()
	createVault(t, m)

	if _, _, err := m.CreateOrUpdatePolicy(
		context.Background(), "sub", "rg", "vault1", "pol1", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	if _, _, err := m.CreateOrUpdateVault(
		context.Background(), "sub", "other", "vault2", "West US", standardVault()); err != nil {
		t.Fatalf("create sibling: %v", err)
	}

	if err := m.PurgeResourceGroup(context.Background(), "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if _, err := m.GetVault(context.Background(), "sub", "rg", "vault1"); !cerrors.IsNotFound(err) {
		t.Errorf("purged vault should be gone, got %v", err)
	}

	if _, err := m.GetVault(context.Background(), "sub", "other", "vault2"); err != nil {
		t.Errorf("sibling in other RG should survive: %v", err)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	m := newMock()
	createVault(t, m)

	if _, _, err := m.CreateOrUpdatePolicy(
		context.Background(), "sub", "rg", "vault1", "pol1", json.RawMessage(`{"backupManagementType":"AzureIaasVM"}`),
	); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	if _, err := m.UpdateStorageConfig(
		context.Background(), "sub", "rg", "vault1", json.RawMessage(`{"storageType":"ZoneRedundant"}`)); err != nil {
		t.Fatalf("set storage: %v", err)
	}

	data, err := m.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(context.Background(), data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	v, err := restored.GetVault(context.Background(), "sub", "rg", "vault1")
	if err != nil {
		t.Fatalf("restored get vault: %v", err)
	}

	if v.Identity == nil || v.Identity.PrincipalID == "" {
		t.Error("restored vault lost identity")
	}

	if _, err := restored.GetPolicy(context.Background(), "sub", "rg", "vault1", "pol1"); err != nil {
		t.Errorf("restored policy missing: %v", err)
	}

	sc, err := restored.GetStorageConfig(context.Background(), "sub", "rg", "vault1")
	if err != nil || !strings.Contains(string(sc.Properties), "ZoneRedundant") {
		t.Errorf("restored storage config wrong: err=%v props=%s", err, sc.Properties)
	}
}
