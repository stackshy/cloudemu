package keyvault

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/scope"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

func sampleVaultConfig(name, sub, rg string) driver.KVVaultConfig {
	return driver.KVVaultConfig{
		Name:     name,
		Location: "westus2",
		Scope:    scope.Scope{Subscription: sub, ResourceGroup: rg},
		Tags:     map[string]string{"env": "test"},
		Properties: driver.KVVaultProperties{
			TenantID:              "tenant-1",
			SKU:                   driver.KVVaultSKU{Family: "A", Name: "premium"},
			EnablePurgeProtection: boolPtr(true),
			AccessPolicies: []driver.KVAccessPolicy{{
				TenantID: "tenant-1",
				ObjectID: "obj-1",
				Permissions: driver.KVAccessPermissions{
					Keys:    []string{"get", "list"},
					Secrets: []string{"get"},
				},
			}},
		},
	}
}

func TestCreateOrUpdateVaultRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	info, err := m.CreateOrUpdateVault(ctx, sampleVaultConfig("v1", "sub-1", "rg-1"))
	require.NoError(t, err)
	assert.Equal(t, "v1", info.Name)
	assert.Equal(t, "https://v1.vault.azure.net/", info.Properties.VaultURI)

	got, err := m.GetVault(ctx, "v1")
	require.NoError(t, err)
	assert.Equal(t, "premium", got.Properties.SKU.Name)
	assert.Equal(t, "tenant-1", got.Properties.TenantID)
	require.NotNil(t, got.Properties.EnablePurgeProtection)
	assert.True(t, *got.Properties.EnablePurgeProtection)
	require.Len(t, got.Properties.AccessPolicies, 1)
	assert.Equal(t, []string{"get", "list"}, got.Properties.AccessPolicies[0].Permissions.Keys)
}

// TestCreateVaultMaterializesDefaults verifies that a create body omitting the
// soft-delete and RBAC flags round-trips with the server-side defaults real
// Azure Key Vault stamps on: enableSoftDelete=true, softDeleteRetentionInDays=90,
// enableRbacAuthorization=false. Absent these, a Terraform azurerm_key_vault
// refresh drifts on the unset fields.
func TestCreateVaultMaterializesDefaults(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := driver.KVVaultConfig{
		Name:     "mini",
		Location: "eastus",
		Scope:    scope.Scope{Subscription: "sub-1", ResourceGroup: "rg-1"},
		Properties: driver.KVVaultProperties{
			TenantID: "tenant-1",
			SKU:      driver.KVVaultSKU{Family: "A", Name: "standard"},
		},
	}

	created, err := m.CreateOrUpdateVault(ctx, cfg)
	require.NoError(t, err)

	got, err := m.GetVault(ctx, "mini")
	require.NoError(t, err)

	for _, v := range []*driver.KVVaultInfo{created, got} {
		require.NotNil(t, v.Properties.EnableSoftDelete)
		assert.True(t, *v.Properties.EnableSoftDelete)
		assert.Equal(t, 90, v.Properties.SoftDeleteRetentionInDays)
		require.NotNil(t, v.Properties.EnableRbacAuthorization)
		assert.False(t, *v.Properties.EnableRbacAuthorization)
	}
}

// TestCreateVaultKeepsExplicitDefaultOverrides ensures the default-materialization
// never clobbers values the caller set explicitly (e.g. a 7-day retention or RBAC
// enabled).
func TestCreateVaultKeepsExplicitDefaultOverrides(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := driver.KVVaultConfig{
		Name:     "explicit",
		Location: "eastus",
		Scope:    scope.Scope{Subscription: "sub-1", ResourceGroup: "rg-1"},
		Properties: driver.KVVaultProperties{
			TenantID:                  "tenant-1",
			SKU:                       driver.KVVaultSKU{Family: "A", Name: "standard"},
			SoftDeleteRetentionInDays: 7,
			EnableRbacAuthorization:   boolPtr(true),
		},
	}

	_, err := m.CreateOrUpdateVault(ctx, cfg)
	require.NoError(t, err)

	got, err := m.GetVault(ctx, "explicit")
	require.NoError(t, err)
	assert.Equal(t, 7, got.Properties.SoftDeleteRetentionInDays)
	require.NotNil(t, got.Properties.EnableRbacAuthorization)
	assert.True(t, *got.Properties.EnableRbacAuthorization)
}

// TestCreateVaultForcesSoftDeleteTrue verifies Azure's mandatory soft-delete:
// an explicit enableSoftDelete=false on create is overridden to true (real
// Azure enables soft-delete on every new vault; once on, it cannot be reverted).
func TestCreateVaultForcesSoftDeleteTrue(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := driver.KVVaultConfig{
		Name:     "forced",
		Location: "eastus",
		Scope:    scope.Scope{Subscription: "sub-1", ResourceGroup: "rg-1"},
		Properties: driver.KVVaultProperties{
			TenantID:         "tenant-1",
			SKU:              driver.KVVaultSKU{Family: "A", Name: "standard"},
			EnableSoftDelete: boolPtr(false),
		},
	}

	created, err := m.CreateOrUpdateVault(ctx, cfg)
	require.NoError(t, err)

	got, err := m.GetVault(ctx, "forced")
	require.NoError(t, err)

	for _, v := range []*driver.KVVaultInfo{created, got} {
		require.NotNil(t, v.Properties.EnableSoftDelete)
		assert.True(t, *v.Properties.EnableSoftDelete, "explicit enableSoftDelete=false must be forced to true")
	}

	// A subsequent replace (PUT semantics) that again sets false must not revert.
	cfg.Properties.EnableSoftDelete = boolPtr(false)
	_, err = m.CreateOrUpdateVault(ctx, cfg)
	require.NoError(t, err)

	again, err := m.GetVault(ctx, "forced")
	require.NoError(t, err)
	require.NotNil(t, again.Properties.EnableSoftDelete)
	assert.True(t, *again.Properties.EnableSoftDelete, "soft-delete must never revert to false")
}

// TestCreateVaultMaterializesSiblingDefaults verifies the enabledForDeployment /
// enabledForDiskEncryption / enabledForTemplateDeployment flags default to false
// (present, not absent) when a create body omits them, matching real Azure's GET
// response and the enableRbacAuthorization default; otherwise an azurerm refresh
// drifts on the absent fields.
func TestCreateVaultMaterializesSiblingDefaults(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := driver.KVVaultConfig{
		Name:     "siblings",
		Location: "eastus",
		Scope:    scope.Scope{Subscription: "sub-1", ResourceGroup: "rg-1"},
		Properties: driver.KVVaultProperties{
			TenantID: "tenant-1",
			SKU:      driver.KVVaultSKU{Family: "A", Name: "standard"},
		},
	}

	created, err := m.CreateOrUpdateVault(ctx, cfg)
	require.NoError(t, err)

	got, err := m.GetVault(ctx, "siblings")
	require.NoError(t, err)

	for _, v := range []*driver.KVVaultInfo{created, got} {
		require.NotNil(t, v.Properties.EnabledForDeployment)
		assert.False(t, *v.Properties.EnabledForDeployment)
		require.NotNil(t, v.Properties.EnabledForDiskEncryption)
		assert.False(t, *v.Properties.EnabledForDiskEncryption)
		require.NotNil(t, v.Properties.EnabledForTemplateDeployment)
		assert.False(t, *v.Properties.EnabledForTemplateDeployment)
	}
}

// TestCreateVaultKeepsExplicitSiblingTrue ensures an explicit true on any of the
// sibling flags is preserved rather than defaulted to false.
func TestCreateVaultKeepsExplicitSiblingTrue(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := driver.KVVaultConfig{
		Name:     "sibling-true",
		Location: "eastus",
		Scope:    scope.Scope{Subscription: "sub-1", ResourceGroup: "rg-1"},
		Properties: driver.KVVaultProperties{
			TenantID:                 "tenant-1",
			SKU:                      driver.KVVaultSKU{Family: "A", Name: "standard"},
			EnabledForDeployment:     boolPtr(true),
			EnabledForDiskEncryption: boolPtr(true),
		},
	}

	_, err := m.CreateOrUpdateVault(ctx, cfg)
	require.NoError(t, err)

	got, err := m.GetVault(ctx, "sibling-true")
	require.NoError(t, err)
	require.NotNil(t, got.Properties.EnabledForDeployment)
	assert.True(t, *got.Properties.EnabledForDeployment)
	require.NotNil(t, got.Properties.EnabledForDiskEncryption)
	assert.True(t, *got.Properties.EnabledForDiskEncryption)
	// The unset third flag still defaults to false.
	require.NotNil(t, got.Properties.EnabledForTemplateDeployment)
	assert.False(t, *got.Properties.EnabledForTemplateDeployment)
}

func TestGetVaultReturnsCopy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateVault(ctx, sampleVaultConfig("v1", "sub-1", "rg-1"))
	require.NoError(t, err)

	got, err := m.GetVault(ctx, "v1")
	require.NoError(t, err)

	// Mutating the returned copy must not alter the stored record.
	got.Tags["env"] = "mutated"
	got.Properties.AccessPolicies[0].Permissions.Keys[0] = "purge"

	again, err := m.GetVault(ctx, "v1")
	require.NoError(t, err)
	assert.Equal(t, "test", again.Tags["env"])
	assert.Equal(t, "get", again.Properties.AccessPolicies[0].Permissions.Keys[0])
}

func TestCreateOrUpdateVaultReplaces(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateVault(ctx, sampleVaultConfig("v1", "sub-1", "rg-1"))
	require.NoError(t, err)

	cfg := sampleVaultConfig("v1", "sub-1", "rg-1")
	cfg.Properties.SKU.Name = "standard"
	_, err = m.CreateOrUpdateVault(ctx, cfg)
	require.NoError(t, err)

	got, err := m.GetVault(ctx, "v1")
	require.NoError(t, err)
	assert.Equal(t, "standard", got.Properties.SKU.Name)

	all, err := m.ListVaults(ctx, scope.Scope{})
	require.NoError(t, err)
	assert.Len(t, all, 1, "replace must not create a second vault")
}

func TestListVaultsByScope(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateVault(ctx, sampleVaultConfig("v1", "sub-1", "rg-1"))
	require.NoError(t, err)
	_, err = m.CreateOrUpdateVault(ctx, sampleVaultConfig("v2", "sub-1", "rg-2"))
	require.NoError(t, err)

	byRG, err := m.ListVaults(ctx, scope.Scope{Subscription: "sub-1", ResourceGroup: "rg-1"})
	require.NoError(t, err)
	require.Len(t, byRG, 1)
	assert.Equal(t, "v1", byRG[0].Name)

	bySub, err := m.ListVaults(ctx, scope.Scope{Subscription: "sub-1"})
	require.NoError(t, err)
	assert.Len(t, bySub, 2)
}

// TestListVaultsSortedByName pins the deterministic list order real ARM
// (Vaults.ListByResourceGroup / ListBySubscription and the azurerm_key_vaults
// data source) exposes: vaults come back sorted by name regardless of creation
// order, so pagers and Terraform refreshes never churn on randomized map order.
func TestListVaultsSortedByName(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Insert in reverse-alphabetical order.
	for _, name := range []string{"vault-c", "vault-a", "vault-b"} {
		_, err := m.CreateOrUpdateVault(ctx, sampleVaultConfig(name, "sub-1", "rg-1"))
		require.NoError(t, err)
	}

	got, err := m.ListVaults(ctx, scope.Scope{Subscription: "sub-1", ResourceGroup: "rg-1"})
	require.NoError(t, err)

	names := make([]string, len(got))
	for i := range got {
		names[i] = got[i].Name
	}

	assert.Equal(t, []string{"vault-a", "vault-b", "vault-c"}, names)
}

func TestDeleteVault(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateVault(ctx, sampleVaultConfig("v1", "sub-1", "rg-1"))
	require.NoError(t, err)

	require.NoError(t, m.DeleteVault(ctx, "v1"))

	_, err = m.GetVault(ctx, "v1")
	assert.True(t, errors.IsNotFound(err))

	assert.True(t, errors.IsNotFound(m.DeleteVault(ctx, "v1")))
}

func TestCreateVaultRequiresName(t *testing.T) {
	m := newTestMock()
	_, err := m.CreateOrUpdateVault(context.Background(), driver.KVVaultConfig{})
	assert.True(t, errors.IsInvalidArgument(err))
}
