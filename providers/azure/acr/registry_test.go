package acr

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateOrUpdateRegistry(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	reg, err := m.CreateOrUpdateRegistry(ctx, "rg-1", "MyReg", driver.AzureRegistryConfig{
		Location:         "eastus",
		SKUName:          "Premium",
		AdminUserEnabled: true,
		IdentityType:     "SystemAssigned",
	})
	require.NoError(t, err)

	assert.Equal(t, "myreg.azurecr.io", reg.LoginServer)
	assert.Equal(t, "Premium", reg.SKUTier)
	assert.True(t, reg.AdminUserEnabled)
	assert.NotEmpty(t, reg.PrincipalID)
	assert.NotEmpty(t, reg.TenantID)

	// Idempotent update preserves creation date.
	updated, err := m.CreateOrUpdateRegistry(ctx, "rg-1", "MyReg", driver.AzureRegistryConfig{Location: "westus"})
	require.NoError(t, err)
	assert.Equal(t, reg.CreationDate, updated.CreationDate)
	assert.Equal(t, "Standard", updated.SKUName) // default when SKU omitted
}

func TestListRegistryCredentialsRequiresAdmin(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateRegistry(ctx, "rg-1", "noadmin", driver.AzureRegistryConfig{})
	require.NoError(t, err)

	// Admin user disabled: listing credentials is a failed precondition.
	_, err = m.ListRegistryCredentials(ctx, "rg-1", "noadmin")
	require.Error(t, err)

	// Unknown registry is a not-found.
	_, err = m.ListRegistryCredentials(ctx, "rg-1", "ghost")
	require.Error(t, err)
}

func TestRegenerateRegistryCredential(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateRegistry(ctx, "rg-1", "reg", driver.AzureRegistryConfig{AdminUserEnabled: true})
	require.NoError(t, err)

	before, err := m.ListRegistryCredentials(ctx, "rg-1", "reg")
	require.NoError(t, err)

	after, err := m.RegenerateRegistryCredential(ctx, "rg-1", "reg", "password")
	require.NoError(t, err)

	assert.NotEqual(t, before.Password, after.Password)
	assert.Equal(t, before.Password2, after.Password2)

	_, err = m.RegenerateRegistryCredential(ctx, "rg-1", "reg", "bogus")
	require.Error(t, err)
}

func TestDeleteTagKeepsManifest(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")
	detail := pushTestImage(t, m, "app", "v1")

	require.NoError(t, m.TagImage(ctx, "app", detail.Digest, "v2"))
	require.NoError(t, m.DeleteTag(ctx, "app", "v1"))

	// The manifest survives, reachable by its remaining tag and by digest.
	img, err := m.GetImage(ctx, "app", "v2")
	require.NoError(t, err)
	assert.Equal(t, detail.Digest, img.Digest)

	_, err = m.GetImage(ctx, "app", "v1")
	require.Error(t, err)

	// Deleting an unknown tag is a not-found.
	require.Error(t, m.DeleteTag(ctx, "app", "ghost"))
}
