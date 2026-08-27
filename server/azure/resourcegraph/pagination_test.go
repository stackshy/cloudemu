// Real-SDK pagination round-trip: verifies totalRecords reflects the full
// result set and that $skipToken pages through the remainder (finding 9).

package resourcegraph_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func TestSDKResourceGraph_Pagination(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	for _, name := range []string{"b1", "b2", "b3", "b4", "b5"} {
		require.NoError(t, cloudP.BlobStorage.CreateBucket(ctx, name))
	}

	srv := azureserver.New(azureserver.Drivers{
		BlobStorage:       cloudP.BlobStorage,
		ResourceDiscovery: cloudP.ResourceDiscovery,
		SubscriptionID:    "123456789012",
	})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newResourceGraphClient(t, ts)

	first, err := client.Resources(ctx, armresourcegraph.QueryRequest{
		Query:   to.Ptr("Resources | where type == 'microsoft.storage/storageaccounts'"),
		Options: &armresourcegraph.QueryRequestOptions{Top: to.Ptr(int32(2))},
	}, nil)
	require.NoError(t, err)

	page1 := first.Data.([]any)
	assert.Len(t, page1, 2, "page holds $top rows")

	require.NotNil(t, first.TotalRecords)
	assert.Equal(t, int64(5), *first.TotalRecords, "totalRecords is the full set, not the page")

	require.NotNil(t, first.SkipToken, "a truncated result must return a $skipToken")

	// Page through the remainder with the returned skip token.
	seen := len(page1)
	token := first.SkipToken

	for token != nil {
		next, nerr := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources | where type == 'microsoft.storage/storageaccounts'"),
			Options: &armresourcegraph.QueryRequestOptions{
				Top:       to.Ptr(int32(2)),
				SkipToken: token,
			},
		}, nil)
		require.NoError(t, nerr)

		seen += len(next.Data.([]any))
		token = next.SkipToken
	}

	assert.Equal(t, 5, seen, "skipToken paging reaches every row exactly once")
}
