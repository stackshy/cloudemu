// Real-SDK round-trip test: the live azure-sdk-for-go armcostmanagement
// QueryClient drives the in-memory Cost Management handler end-to-end.
package costmanagement_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/costmanagement/armcostmanagement"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const subscriptionScope = "subscriptions/123456789012"

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// newCostClient seeds an Azure estate (two VMs + a public IP, all of which
// price on the always-on inventory) and returns a live armcostmanagement
// QueryClient pointed at an in-process cloudemu server over that estate.
func newCostClient(t *testing.T) *armcostmanagement.QueryClient {
	t.Helper()

	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	_, err := cloudP.VirtualMachines.RunInstances(ctx, computedriver.InstanceConfig{
		InstanceType: "Standard_D2s_v3", OSType: "Linux",
	}, 2)
	require.NoError(t, err)

	_, err = cloudP.VNet.AllocateAddress(ctx, netdriver.ElasticIPConfig{})
	require.NoError(t, err)

	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines:   cloudP.VirtualMachines,
		Network:           cloudP.VNet,
		ResourceDiscovery: cloudP.ResourceDiscovery,
		SubscriptionID:    "123456789012",
	})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := armcostmanagement.NewQueryClient(fakeCred{}, opts)
	require.NoError(t, err)

	return client
}

// sumAggregation is the standard total-cost aggregation every Cost Management
// query carries: SUM over the "Cost" column, aliased "totalCost".
func sumAggregation() map[string]*armcostmanagement.QueryAggregation {
	return map[string]*armcostmanagement.QueryAggregation{
		"totalCost": {Name: to.Ptr("Cost"), Function: to.Ptr(armcostmanagement.FunctionTypeSum)},
	}
}

// columnIndex returns the index of the column named name, or -1.
func columnIndex(cols []*armcostmanagement.QueryColumn, name string) int {
	for i, c := range cols {
		if c.Name != nil && *c.Name == name {
			return i
		}
	}

	return -1
}

// TestQueryUsage_SubscriptionScope proves an ungrouped subscription-scope query
// returns the Cost + Currency columns and a single row carrying a positive
// total cost for the seeded estate.
func TestQueryUsage_SubscriptionScope(t *testing.T) {
	client := newCostClient(t)

	resp, err := client.Usage(context.Background(), subscriptionScope, armcostmanagement.QueryDefinition{
		Type:      to.Ptr(armcostmanagement.ExportTypeActualCost),
		Timeframe: to.Ptr(armcostmanagement.TimeframeTypeMonthToDate),
		Dataset: &armcostmanagement.QueryDataset{
			Aggregation: sumAggregation(),
		},
	}, nil)
	require.NoError(t, err)

	require.NotNil(t, resp.Properties)
	cols := resp.Properties.Columns
	rows := resp.Properties.Rows

	require.GreaterOrEqual(t, columnIndex(cols, "Cost"), 0, "missing Cost column")
	currencyIdx := columnIndex(cols, "Currency")
	require.GreaterOrEqual(t, currencyIdx, 0, "missing Currency column")

	require.Len(t, rows, 1, "ungrouped query returns a single aggregate row")

	costIdx := columnIndex(cols, "Cost")
	total, ok := rows[0][costIdx].(float64)
	require.True(t, ok, "cost value is %T, want number", rows[0][costIdx])
	assert.Positive(t, total, "seeded estate must have a positive monthly cost")
	assert.Equal(t, "USD", rows[0][currencyIdx])

	require.NotNil(t, resp.Type)
	assert.Equal(t, "microsoft.costmanagement/Query", *resp.Type)
}

// TestQueryUsage_GroupByResourceType proves a query grouped by the ResourceType
// dimension yields one row per Azure resource type, each with its own positive
// cost, and that the VM type is present with its ARM type string.
func TestQueryUsage_GroupByResourceType(t *testing.T) {
	client := newCostClient(t)

	resp, err := client.Usage(context.Background(), subscriptionScope, armcostmanagement.QueryDefinition{
		Type:      to.Ptr(armcostmanagement.ExportTypeActualCost),
		Timeframe: to.Ptr(armcostmanagement.TimeframeTypeMonthToDate),
		Dataset: &armcostmanagement.QueryDataset{
			Aggregation: sumAggregation(),
			Grouping: []*armcostmanagement.QueryGrouping{
				{Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension), Name: to.Ptr("ResourceType")},
			},
		},
	}, nil)
	require.NoError(t, err)

	require.NotNil(t, resp.Properties)
	cols := resp.Properties.Columns
	rows := resp.Properties.Rows

	costIdx := columnIndex(cols, "Cost")
	typeIdx := columnIndex(cols, "ResourceType")
	currencyIdx := columnIndex(cols, "Currency")
	require.GreaterOrEqual(t, costIdx, 0)
	require.GreaterOrEqual(t, typeIdx, 0, "grouping must add a ResourceType column")
	require.GreaterOrEqual(t, currencyIdx, 0)

	// Two VMs + one public IP -> two distinct resource types.
	require.GreaterOrEqual(t, len(rows), 2, "grouping by ResourceType must yield grouped rows")

	seenVM := false
	for _, row := range rows {
		c, ok := row[costIdx].(float64)
		require.True(t, ok, "cost value is %T, want number", row[costIdx])
		assert.Positive(t, c)
		assert.Equal(t, "USD", row[currencyIdx])

		if row[typeIdx] == "microsoft.compute/virtualmachines" {
			seenVM = true
		}
	}

	assert.True(t, seenVM, "expected a microsoft.compute/virtualmachines group row")
}

// TestQueryUsage_GroupByServiceName proves grouping by the ServiceName
// dimension buckets resources under their Azure service meter category.
func TestQueryUsage_GroupByServiceName(t *testing.T) {
	client := newCostClient(t)

	resp, err := client.Usage(context.Background(), subscriptionScope, armcostmanagement.QueryDefinition{
		Type:      to.Ptr(armcostmanagement.ExportTypeActualCost),
		Timeframe: to.Ptr(armcostmanagement.TimeframeTypeMonthToDate),
		Dataset: &armcostmanagement.QueryDataset{
			Aggregation: sumAggregation(),
			Grouping: []*armcostmanagement.QueryGrouping{
				{Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension), Name: to.Ptr("ServiceName")},
			},
		},
	}, nil)
	require.NoError(t, err)

	require.NotNil(t, resp.Properties)
	cols := resp.Properties.Columns
	svcIdx := columnIndex(cols, "ServiceName")
	require.GreaterOrEqual(t, svcIdx, 0, "grouping must add a ServiceName column")

	labels := map[string]bool{}
	for _, row := range resp.Properties.Rows {
		if s, ok := row[svcIdx].(string); ok {
			labels[s] = true
		}
	}

	assert.True(t, labels["Virtual Machines"], "expected a Virtual Machines service group, got %v", labels)
	assert.GreaterOrEqual(t, len(labels), 2, "VM + public IP span two service groups")
}

// TestQueryUsage_GranularAndGrouped pins the exact column order for a query that
// sets BOTH a granularity and a grouping. Real Cost Management orders the
// columns cost → grouping dimension(s) → date → Currency, and every row matches
// that order. This is the combined case a grouped-or-granular-only test can't
// catch.
func TestQueryUsage_GranularAndGrouped(t *testing.T) {
	client := newCostClient(t)

	resp, err := client.Usage(context.Background(), subscriptionScope, armcostmanagement.QueryDefinition{
		Type:      to.Ptr(armcostmanagement.ExportTypeActualCost),
		Timeframe: to.Ptr(armcostmanagement.TimeframeTypeMonthToDate),
		Dataset: &armcostmanagement.QueryDataset{
			Granularity: to.Ptr(armcostmanagement.GranularityTypeDaily),
			Aggregation: sumAggregation(),
			Grouping: []*armcostmanagement.QueryGrouping{
				{Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension), Name: to.Ptr("ResourceType")},
			},
		},
	}, nil)
	require.NoError(t, err)

	require.NotNil(t, resp.Properties)

	names := make([]string, 0, len(resp.Properties.Columns))
	for _, c := range resp.Properties.Columns {
		require.NotNil(t, c.Name)
		names = append(names, *c.Name)
	}

	assert.Equal(t, []string{"Cost", "ResourceType", "UsageDate", "Currency"}, names,
		"grouping dimension must precede the granularity date column")

	require.NotEmpty(t, resp.Properties.Rows)

	// Every row is ordered to match the columns: number, string, number, "USD".
	for _, row := range resp.Properties.Rows {
		require.Len(t, row, 4)

		_, ok := row[0].(float64)
		assert.True(t, ok, "col 0 (Cost) is %T, want number", row[0])

		_, ok = row[1].(string)
		assert.True(t, ok, "col 1 (ResourceType) is %T, want string", row[1])

		_, ok = row[2].(float64)
		assert.True(t, ok, "col 2 (UsageDate) is %T, want number", row[2])

		assert.Equal(t, "USD", row[3], "col 3 must be the currency")
	}
}
