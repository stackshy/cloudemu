// Real-SDK test for the generic Microsoft.Resources listing (az resource list).
// The live armresources Client drives both the subscription-wide and the
// per-resource-group listing against the discovery-backed handler.

package resourcegraph_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

func TestSDKGenericResourcesList(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	_, err := cloudP.Databricks.CreateWorkspace(ctx, dbxdriver.WorkspaceConfig{
		Name: "ws-e2e", ResourceGroup: "rg-e2e", Location: "eastus", SKUName: "premium",
		ManagedResourceGroupID: "/subscriptions/123456789012/resourceGroups/managed-rg",
		Tags:                   map[string]string{"env": "prod"},
	})
	require.NoError(t, err)

	srv := azureserver.New(azureserver.Drivers{
		Databricks:        cloudP.Databricks,
		ResourceDiscovery: cloudP.ResourceDiscovery,
		SubscriptionID:    "123456789012",
	})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newGenericResourcesClient(t, ts)

	t.Run("list by resource group", func(t *testing.T) {
		names := collect(ctx, t, client.NewListByResourceGroupPager("rg-e2e", nil),
			func(p armresources.ClientListByResourceGroupResponse) []*armresources.GenericResourceExpanded {
				return p.Value
			})
		assert.Contains(t, names, "ws-e2e")
	})

	t.Run("subscription-wide list", func(t *testing.T) {
		names := collect(ctx, t, client.NewListPager(nil),
			func(p armresources.ClientListResponse) []*armresources.GenericResourceExpanded {
				return p.Value
			})
		assert.Contains(t, names, "ws-e2e")
	})

	t.Run("empty group returns nothing", func(t *testing.T) {
		names := collect(ctx, t, client.NewListByResourceGroupPager("no-such-rg", nil),
			func(p armresources.ClientListByResourceGroupResponse) []*armresources.GenericResourceExpanded {
				return p.Value
			})
		assert.Empty(t, names)
	})
}

// collect drains a pager, extracting resource names via value, so it works for
// both the subscription-wide and per-group list pagers (which return distinct
// response types).
func collect[T any](ctx context.Context, t *testing.T, pager *runtime.Pager[T],
	value func(T) []*armresources.GenericResourceExpanded,
) []string {
	t.Helper()

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		require.NoError(t, err)

		for _, r := range value(page) {
			if r.Name != nil {
				names = append(names, *r.Name)
			}
		}
	}

	return names
}

func newGenericResourcesClient(t *testing.T, ts *httptest.Server) *armresources.Client {
	t.Helper()

	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		}},
		Transport: ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}

	c, err := armresources.NewClient("123456789012", fakeCred{}, opts)
	require.NoError(t, err)

	return c
}
