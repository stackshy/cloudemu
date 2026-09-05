package storageaccount_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSDKStorageAccountIdentitySystemAssigned proves a system-assigned managed
// identity survives create -> get with a synthesized principalId/tenantId. The
// identity block is a top-level ARM sibling of properties, which the
// echo-properties overlay (properties-only) does not preserve — so before it
// was modeled here a Terraform user with identity { type = "SystemAssigned" }
// saw the block vanish on every refresh (perpetual drift).
func TestSDKStorageAccountIdentitySystemAssigned(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctident", armstorage.AccountCreateParameters{
		Location: to.Ptr("westus2"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
		Identity: &armstorage.Identity{Type: to.Ptr(armstorage.IdentityTypeSystemAssigned)},
	}, nil)
	require.NoError(t, err)
	created, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, created.Identity)
	require.Equal(t, armstorage.IdentityTypeSystemAssigned, *created.Identity.Type)
	require.NotEmpty(t, *created.Identity.PrincipalID)
	require.NotEmpty(t, *created.Identity.TenantID)

	got, err := client.GetProperties(ctx, "rg-1", "acctident", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Identity)
	assert.Equal(t, armstorage.IdentityTypeSystemAssigned, *got.Identity.Type)
	assert.Equal(t, *created.Identity.PrincipalID, *got.Identity.PrincipalID, "principalId is stable across reads")
	assert.Equal(t, *created.Identity.TenantID, *got.Identity.TenantID)
}

// TestSDKStorageAccountIdentityUserAssigned proves a user-assigned identity
// round-trips: the attached identity resource ID is echoed back with a
// synthesized principal/client pair, as real Azure reports it.
func TestSDKStorageAccountIdentityUserAssigned(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)
	uaID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/" +
		"Microsoft.ManagedIdentity/userAssignedIdentities/uai1"

	poller, err := client.BeginCreate(ctx, "rg-1", "acctua", armstorage.AccountCreateParameters{
		Location: to.Ptr("westus2"),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
		Identity: &armstorage.Identity{
			Type:                   to.Ptr(armstorage.IdentityTypeUserAssigned),
			UserAssignedIdentities: map[string]*armstorage.UserAssignedIdentity{uaID: {}},
		},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := client.GetProperties(ctx, "rg-1", "acctua", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Identity)
	assert.Equal(t, armstorage.IdentityTypeUserAssigned, *got.Identity.Type)
	require.Contains(t, got.Identity.UserAssignedIdentities, uaID)
	entry := got.Identity.UserAssignedIdentities[uaID]
	require.NotNil(t, entry)
	require.NotNil(t, entry.PrincipalID)
	require.NotNil(t, entry.ClientID)
	assert.NotEmpty(t, *entry.PrincipalID)
	assert.NotEmpty(t, *entry.ClientID)
}

// TestSDKStorageAccountIdentityPatch proves a PATCH can add an identity, that a
// tags-only PATCH preserves it (partial-update semantics), and that a PATCH to
// type None clears the block.
func TestSDKStorageAccountIdentityPatch(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctpi", armstorage.AccountCreateParameters{
		Location: to.Ptr("westus2"),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	_, err = client.Update(ctx, "rg-1", "acctpi", armstorage.AccountUpdateParameters{
		Identity: &armstorage.Identity{Type: to.Ptr(armstorage.IdentityTypeSystemAssigned)},
	}, nil)
	require.NoError(t, err)
	added, err := client.GetProperties(ctx, "rg-1", "acctpi", nil)
	require.NoError(t, err)
	require.NotNil(t, added.Identity)
	assert.Equal(t, armstorage.IdentityTypeSystemAssigned, *added.Identity.Type)
	principal := *added.Identity.PrincipalID

	_, err = client.Update(ctx, "rg-1", "acctpi", armstorage.AccountUpdateParameters{
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	require.NoError(t, err)
	preserved, err := client.GetProperties(ctx, "rg-1", "acctpi", nil)
	require.NoError(t, err)
	require.NotNil(t, preserved.Identity, "tags-only PATCH must not drop identity")
	assert.Equal(t, principal, *preserved.Identity.PrincipalID, "principalId stays stable across PATCH")

	_, err = client.Update(ctx, "rg-1", "acctpi", armstorage.AccountUpdateParameters{
		Identity: &armstorage.Identity{Type: to.Ptr(armstorage.IdentityTypeNone)},
	}, nil)
	require.NoError(t, err)
	cleared, err := client.GetProperties(ctx, "rg-1", "acctpi", nil)
	require.NoError(t, err)
	assert.Nil(t, cleared.Identity, "identity type None clears the block")
}

// TestSDKStorageAccountNoIdentityByDefault proves an account created without an
// identity block reports none (real Azure omits identity entirely rather than
// returning type None).
func TestSDKStorageAccountNoIdentityByDefault(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctnoid", armstorage.AccountCreateParameters{
		Location: to.Ptr("westus2"),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := client.GetProperties(ctx, "rg-1", "acctnoid", nil)
	require.NoError(t, err)
	assert.Nil(t, got.Identity)
}
