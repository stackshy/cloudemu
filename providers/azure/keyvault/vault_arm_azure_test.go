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
