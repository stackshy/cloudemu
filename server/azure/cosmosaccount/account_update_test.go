// Real-user e2e regression test: PATCH .../databaseAccounts/{name}
// (DatabaseAccountsClient.BeginUpdate) was not routed at all — the handler's
// ServeHTTP only handled PUT/GET/DELETE, so any real armcosmos client calling
// Update (the path Terraform's azurerm_cosmosdb_account takes to change tags
// or consistency without a full re-create) got a 405. These tests drive the
// real armcosmos BeginUpdate LRO end-to-end and pin down the fix: PATCH is a
// non-destructive partial update — only submitted fields change, tags are a
// full replace (not a merge) when present, and immutable fields (kind) are
// left untouched.
package cosmosaccount_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSDKDatabaseAccountUpdateTags drives BeginUpdate to change tags only,
// asserting the new tags fully replace the old set (not merge) and that
// unrelated fields (kind, location) survive untouched.
func TestSDKDatabaseAccountUpdateTags(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "cosmos-upd", armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armcosmos.DatabaseAccountKindGlobalDocumentDB),
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			DatabaseAccountOfferType: to.Ptr("Standard"),
			Locations: []*armcosmos.Location{
				{LocationName: to.Ptr("eastus"), FailoverPriority: to.Ptr[int32](0)},
			},
		},
		Tags: map[string]*string{"env": to.Ptr("staging"), "owner": to.Ptr("team-a")},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	updPoller, err := client.BeginUpdate(ctx, "rg-1", "cosmos-upd", armcosmos.DatabaseAccountUpdateParameters{
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	require.NoError(t, err)

	updated, err := updPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	require.NotNil(t, updated.Tags["env"])
	assert.Equal(t, "prod", *updated.Tags["env"])
	// PATCH tags is a full replace, not a merge: "owner" must be gone.
	assert.NotContains(t, updated.Tags, "owner")

	require.NotNil(t, updated.Kind)
	assert.Equal(t, armcosmos.DatabaseAccountKindGlobalDocumentDB, *updated.Kind)
	require.NotNil(t, updated.Location)
	assert.Equal(t, "eastus", *updated.Location)

	// ... and the change survives an independent GET.
	got, err := client.Get(ctx, "rg-1", "cosmos-upd", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Tags["env"])
	assert.Equal(t, "prod", *got.Tags["env"])
	assert.NotContains(t, got.Tags, "owner")
}

// TestSDKDatabaseAccountUpdateConsistencyPolicy drives BeginUpdate to change
// only the consistency policy, asserting it round-trips and a field omitted
// from the PATCH body (tags) is preserved rather than reset to empty.
func TestSDKDatabaseAccountUpdateConsistencyPolicy(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "cosmos-upd-cp", armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr("westus2"),
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			DatabaseAccountOfferType: to.Ptr("Standard"),
			Locations: []*armcosmos.Location{
				{LocationName: to.Ptr("westus2"), FailoverPriority: to.Ptr[int32](0)},
			},
		},
		Tags: map[string]*string{"keep": to.Ptr("me")},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	updPoller, err := client.BeginUpdate(ctx, "rg-1", "cosmos-upd-cp", armcosmos.DatabaseAccountUpdateParameters{
		Properties: &armcosmos.DatabaseAccountUpdateProperties{
			ConsistencyPolicy: &armcosmos.ConsistencyPolicy{
				DefaultConsistencyLevel: to.Ptr(armcosmos.DefaultConsistencyLevelStrong),
			},
		},
	}, nil)
	require.NoError(t, err)

	updated, err := updPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	require.NotNil(t, updated.Properties)
	require.NotNil(t, updated.Properties.ConsistencyPolicy)
	require.NotNil(t, updated.Properties.ConsistencyPolicy.DefaultConsistencyLevel)
	assert.Equal(t, armcosmos.DefaultConsistencyLevelStrong, *updated.Properties.ConsistencyPolicy.DefaultConsistencyLevel)

	// Tags were not part of this PATCH, so they must survive unchanged.
	require.NotNil(t, updated.Tags["keep"])
	assert.Equal(t, "me", *updated.Tags["keep"])
}

// TestSDKDatabaseAccountUpdateMissing asserts PATCHing a nonexistent account
// 404s rather than silently creating one (PATCH is update-only, unlike PUT's
// create-or-update).
func TestSDKDatabaseAccountUpdateMissing(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)

	// The emulator answers the initial PATCH synchronously (200/404, no async
	// 202), so BeginUpdate's own initial request — not a later poll — is where
	// the 404 surfaces.
	_, err := client.BeginUpdate(ctx, "rg-1", "does-not-exist", armcosmos.DatabaseAccountUpdateParameters{
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	require.Error(t, err)
}
