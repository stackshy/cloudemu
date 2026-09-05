// Real-user e2e regression test: the account-level boolean/network toggles
// (enableAutomaticFailover, enableMultipleWriteLocations, publicNetworkAccess)
// and enableFreeTier that Terraform's azurerm_cosmosdb_account reads back on
// every refresh. They were not emitted by the handler and leaned on the generic
// property-echo overlay, which swallows an explicit zero value (false / "") —
// so an account created with multiple-write enabled and later PATCHed back to
// disabled kept reporting enabled, a perpetual Terraform drift. These tests
// drive the real armcosmos client and pin the fix: the toggles are authoritative
// handler output, always present with their real value, and round-trip through
// create, PATCH-to-false and GET.
package cosmosaccount_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSDKDatabaseAccountToggleDefaults asserts an account created without the
// toggles reports Azure's documented defaults (all false, publicNetworkAccess
// Enabled) on both the create response and an independent GET, rather than
// omitting them — Terraform reads each field and an absent value is drift-prone.
func TestSDKDatabaseAccountToggleDefaults(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)
	createAccount(t, client, "rg-1", "cosmos-def-tg", "eastus")

	got, err := client.Get(ctx, "rg-1", "cosmos-def-tg", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Properties)

	p := got.Properties
	require.NotNil(t, p.EnableAutomaticFailover)
	assert.False(t, *p.EnableAutomaticFailover)
	require.NotNil(t, p.EnableMultipleWriteLocations)
	assert.False(t, *p.EnableMultipleWriteLocations)
	require.NotNil(t, p.EnableFreeTier)
	assert.False(t, *p.EnableFreeTier)
	require.NotNil(t, p.PublicNetworkAccess)
	assert.Equal(t, armcosmos.PublicNetworkAccessEnabled, *p.PublicNetworkAccess)
}

// TestSDKDatabaseAccountToggleRoundTrip asserts every toggle set at create time
// survives create -> GET with its submitted value.
func TestSDKDatabaseAccountToggleRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "cosmos-tg", armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr("eastus"),
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			DatabaseAccountOfferType:     to.Ptr("Standard"),
			EnableAutomaticFailover:      to.Ptr(true),
			EnableMultipleWriteLocations: to.Ptr(true),
			PublicNetworkAccess:          to.Ptr(armcosmos.PublicNetworkAccessDisabled),
			Locations: []*armcosmos.Location{
				{LocationName: to.Ptr("eastus"), FailoverPriority: to.Ptr[int32](0)},
				{LocationName: to.Ptr("westus"), FailoverPriority: to.Ptr[int32](1)},
			},
		},
	}, nil)
	require.NoError(t, err)

	created, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)
	assertToggles(t, created.Properties, true, true, armcosmos.PublicNetworkAccessDisabled)
	// multi-write means every declared region accepts writes.
	require.Len(t, created.Properties.WriteLocations, 2)

	got, err := client.Get(ctx, "rg-1", "cosmos-tg", nil)
	require.NoError(t, err)
	assertToggles(t, got.Properties, true, true, armcosmos.PublicNetworkAccessDisabled)
	require.Len(t, got.Properties.WriteLocations, 2)
}

// TestSDKDatabaseAccountToggleDisableViaPatch is the core regression: an account
// created with multiple-write enabled and then PATCHed back to disabled must
// report disabled (writeLocations collapsing to the single priority-0 region),
// and a toggle omitted from the PATCH body must be preserved.
func TestSDKDatabaseAccountToggleDisableViaPatch(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "cosmos-tg-off", armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr("eastus"),
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			DatabaseAccountOfferType:     to.Ptr("Standard"),
			EnableAutomaticFailover:      to.Ptr(true),
			EnableMultipleWriteLocations: to.Ptr(true),
			Locations: []*armcosmos.Location{
				{LocationName: to.Ptr("eastus"), FailoverPriority: to.Ptr[int32](0)},
				{LocationName: to.Ptr("westus"), FailoverPriority: to.Ptr[int32](1)},
			},
		},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	updPoller, err := client.BeginUpdate(ctx, "rg-1", "cosmos-tg-off", armcosmos.DatabaseAccountUpdateParameters{
		Properties: &armcosmos.DatabaseAccountUpdateProperties{
			EnableMultipleWriteLocations: to.Ptr(false),
		},
	}, nil)
	require.NoError(t, err)

	updated, err := updPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, updated.Properties)
	require.NotNil(t, updated.Properties.EnableMultipleWriteLocations)
	assert.False(t, *updated.Properties.EnableMultipleWriteLocations,
		"PATCH multiWrite=false must report false, not the stale created-with true")
	// A single write region once multi-write is off.
	require.Len(t, updated.Properties.WriteLocations, 1)
	// A toggle omitted from the PATCH survives unchanged.
	require.NotNil(t, updated.Properties.EnableAutomaticFailover)
	assert.True(t, *updated.Properties.EnableAutomaticFailover)

	// ... and the disabled value survives an independent GET (not resurrected by
	// the overlay on a later read).
	got, err := client.Get(ctx, "rg-1", "cosmos-tg-off", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Properties.EnableMultipleWriteLocations)
	assert.False(t, *got.Properties.EnableMultipleWriteLocations)
	require.Len(t, got.Properties.WriteLocations, 1)
	require.NotNil(t, got.Properties.EnableAutomaticFailover)
	assert.True(t, *got.Properties.EnableAutomaticFailover)
}

func assertToggles(
	t *testing.T, props *armcosmos.DatabaseAccountGetProperties,
	autoFailover, multiWrite bool, pna armcosmos.PublicNetworkAccess,
) {
	t.Helper()

	require.NotNil(t, props)
	require.NotNil(t, props.EnableAutomaticFailover)
	assert.Equal(t, autoFailover, *props.EnableAutomaticFailover)
	require.NotNil(t, props.EnableMultipleWriteLocations)
	assert.Equal(t, multiWrite, *props.EnableMultipleWriteLocations)
	require.NotNil(t, props.PublicNetworkAccess)
	assert.Equal(t, pna, *props.PublicNetworkAccess)
}
