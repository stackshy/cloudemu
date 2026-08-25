// This file exercises two HIGH bugs in the databaseAccounts control plane:
//   - A multi-region create (Locations with more than one entry) used to
//     collapse to a single region, with readLocations/writeLocations/
//     failoverPolicies left unpopulated.
//   - POST .../failoverPriorityChange 404d instead of reordering the account's
//     regions.

package cosmosaccount_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSDKDatabaseAccountMultiRegionCreateGet asserts a two-region create
// carries both regions through to Get: properties.locations, readLocations,
// writeLocations (single-write, so only the priority-0 region) and
// failoverPolicies all reflect both regions, ordered by failover priority.
func TestSDKDatabaseAccountMultiRegionCreateGet(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "cosmos-multi", armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr("westus2"),
		Kind:     to.Ptr(armcosmos.DatabaseAccountKindGlobalDocumentDB),
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			DatabaseAccountOfferType: to.Ptr("Standard"),
			Locations: []*armcosmos.Location{
				{LocationName: to.Ptr("westus2"), FailoverPriority: to.Ptr[int32](0)},
				{LocationName: to.Ptr("eastus2"), FailoverPriority: to.Ptr[int32](1)},
			},
		},
	}, nil)
	require.NoError(t, err)

	created, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	assertTwoRegionAccount(t, created.Properties, "westus2", "eastus2")

	got, err := client.Get(ctx, "rg-1", "cosmos-multi", nil)
	require.NoError(t, err)

	assertTwoRegionAccount(t, got.Properties, "westus2", "eastus2")
}

// assertTwoRegionAccount checks properties.locations/readLocations carry both
// regions ordered by priority, writeLocations carries only the priority-0
// region (single-write default), and failoverPolicies mirrors locations.
func assertTwoRegionAccount(t *testing.T, props *armcosmos.DatabaseAccountGetProperties, first, second string) {
	t.Helper()
	require.NotNil(t, props)

	require.Len(t, props.Locations, 2)
	require.NotNil(t, props.Locations[0].LocationName)
	require.NotNil(t, props.Locations[1].LocationName)
	assert.Equal(t, first, *props.Locations[0].LocationName)
	assert.Equal(t, second, *props.Locations[1].LocationName)

	require.Len(t, props.ReadLocations, 2)

	require.Len(t, props.WriteLocations, 1)
	require.NotNil(t, props.WriteLocations[0].LocationName)
	assert.Equal(t, first, *props.WriteLocations[0].LocationName)

	require.Len(t, props.FailoverPolicies, 2)
	require.NotNil(t, props.FailoverPolicies[0].LocationName)
	require.NotNil(t, props.FailoverPolicies[1].LocationName)
	assert.Equal(t, first, *props.FailoverPolicies[0].LocationName)
	assert.Equal(t, second, *props.FailoverPolicies[1].LocationName)
	require.NotNil(t, props.FailoverPolicies[0].FailoverPriority)
	assert.Equal(t, int32(0), *props.FailoverPolicies[0].FailoverPriority)
}

// TestSDKDatabaseAccountMultiWriteLocations asserts an account created with
// EnableMultipleWriteLocations reports every region as a write location, not
// just the priority-0 one.
func TestSDKDatabaseAccountMultiWriteLocations(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "cosmos-multiwrite", armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr("westus2"),
		Kind:     to.Ptr(armcosmos.DatabaseAccountKindGlobalDocumentDB),
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			DatabaseAccountOfferType:     to.Ptr("Standard"),
			EnableMultipleWriteLocations: to.Ptr(true),
			Locations: []*armcosmos.Location{
				{LocationName: to.Ptr("westus2"), FailoverPriority: to.Ptr[int32](0)},
				{LocationName: to.Ptr("eastus2"), FailoverPriority: to.Ptr[int32](1)},
			},
		},
	}, nil)
	require.NoError(t, err)

	created, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	require.Len(t, created.Properties.WriteLocations, 2)
}

// TestSDKDatabaseAccountFailoverPriorityChange asserts the action reorders
// the account's failover priorities instead of 404ing, and the new ordering
// is visible on the next Get.
func TestSDKDatabaseAccountFailoverPriorityChange(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "cosmos-failover", armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armcosmos.DatabaseAccountKindGlobalDocumentDB),
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			DatabaseAccountOfferType: to.Ptr("Standard"),
			Locations: []*armcosmos.Location{
				{LocationName: to.Ptr("eastus"), FailoverPriority: to.Ptr[int32](0)},
				{LocationName: to.Ptr("westus"), FailoverPriority: to.Ptr[int32](1)},
			},
		},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	fpPoller, err := client.BeginFailoverPriorityChange(ctx, "rg-1", "cosmos-failover", armcosmos.FailoverPolicies{
		FailoverPolicies: []*armcosmos.FailoverPolicy{
			{FailoverPriority: to.Ptr[int32](0), LocationName: to.Ptr("westus")},
			{FailoverPriority: to.Ptr[int32](1), LocationName: to.Ptr("eastus")},
		},
	}, nil)
	require.NoError(t, err)

	_, err = fpPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := client.Get(ctx, "rg-1", "cosmos-failover", nil)
	require.NoError(t, err)

	require.Len(t, got.Properties.FailoverPolicies, 2)
	require.NotNil(t, got.Properties.FailoverPolicies[0].LocationName)
	assert.Equal(t, "westus", *got.Properties.FailoverPolicies[0].LocationName)
	require.NotNil(t, got.Properties.FailoverPolicies[1].LocationName)
	assert.Equal(t, "eastus", *got.Properties.FailoverPolicies[1].LocationName)

	// westus is now the write region.
	require.Len(t, got.Properties.WriteLocations, 1)
	require.NotNil(t, got.Properties.WriteLocations[0].LocationName)
	assert.Equal(t, "westus", *got.Properties.WriteLocations[0].LocationName)
}
