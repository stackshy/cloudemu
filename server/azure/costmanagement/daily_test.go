package costmanagement_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/costmanagement/armcostmanagement"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestQueryUsage_DailyBucketsPerDay proves a Daily MonthToDate query returns one
// row per day in the month-to-date period (not a single monthly row stamped
// today), each carrying a pro-rated per-day cost, and that the per-day costs sum
// to approximately the monthly figure the same estate reports — the AWS Cost
// Explorer convention.
func TestQueryUsage_DailyBucketsPerDay(t *testing.T) {
	client := newCostClient(t)

	now := time.Now().UTC()
	wantDays := now.Day() // MonthToDate: first of month .. today, inclusive.

	daily, err := client.Usage(context.Background(), subscriptionScope, armcostmanagement.QueryDefinition{
		Type:      to.Ptr(armcostmanagement.ExportTypeActualCost),
		Timeframe: to.Ptr(armcostmanagement.TimeframeTypeMonthToDate),
		Dataset: &armcostmanagement.QueryDataset{
			Granularity: to.Ptr(armcostmanagement.GranularityTypeDaily),
			Aggregation: sumAggregation(),
		},
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, daily.Properties)

	cols := daily.Properties.Columns
	rows := daily.Properties.Rows

	costIdx := columnIndex(cols, "Cost")
	dateIdx := columnIndex(cols, "UsageDate")
	require.GreaterOrEqual(t, costIdx, 0, "missing Cost column")
	require.GreaterOrEqual(t, dateIdx, 0, "Daily query must add a UsageDate column")

	require.Len(t, rows, wantDays, "ungrouped Daily query returns one row per day in the period")

	// Each row is a positive per-day cost stamped with a distinct yyyymmdd date
	// inside the current month; together the days cover the contiguous 1..today
	// range.
	seenDays := map[int]bool{}

	var dailySum float64

	for _, row := range rows {
		c, ok := row[costIdx].(float64)
		require.True(t, ok, "cost is %T, want number", row[costIdx])
		assert.Positive(t, c, "each day carries a positive pro-rated cost")

		dailySum += c

		ymd, ok := row[dateIdx].(float64)
		require.True(t, ok, "UsageDate is %T, want number", row[dateIdx])

		assert.Equal(t, now.Year(), int(ymd)/10000)
		assert.Equal(t, int(now.Month()), (int(ymd)/100)%100)
		seenDays[int(ymd)%100] = true
	}

	for d := 1; d <= wantDays; d++ {
		assert.True(t, seenDays[d], "missing a row for day %d of the period", d)
	}

	// The per-day rows sum to ~= the monthly figure (24h/730h per day summed over
	// the days of the period, matching AWS Cost Explorer's convention).
	monthly := aggregateMonthly(t, client)
	perDay := monthly * 24.0 / 730.0
	assert.InDelta(t, perDay*float64(wantDays), dailySum, 1e-6,
		"daily rows must sum to the pro-rated monthly figure")
}

// aggregateMonthly returns the full monthly total the seeded estate reports
// through an ungrouped, non-granular query (a single aggregate row over the
// period).
func aggregateMonthly(t *testing.T, client *armcostmanagement.QueryClient) float64 {
	t.Helper()

	resp, err := client.Usage(context.Background(), subscriptionScope, armcostmanagement.QueryDefinition{
		Type:      to.Ptr(armcostmanagement.ExportTypeActualCost),
		Timeframe: to.Ptr(armcostmanagement.TimeframeTypeMonthToDate),
		Dataset:   &armcostmanagement.QueryDataset{Aggregation: sumAggregation()},
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, resp.Properties)
	require.Len(t, resp.Properties.Rows, 1, "non-granular query stays a single aggregate row")

	costIdx := columnIndex(resp.Properties.Columns, "Cost")
	total, ok := resp.Properties.Rows[0][costIdx].(float64)
	require.True(t, ok)

	return total
}

// costQueryResponse is the subset of the Cost Management query response the raw
// Monthly test reads. The typed armcostmanagement SDK's query GranularityType
// only models "Daily", so the Monthly path is exercised with a raw JSON POST.
type costQueryResponse struct {
	Properties struct {
		Columns []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"columns"`
		Rows [][]any `json:"rows"`
	} `json:"properties"`
}

// TestQueryUsage_MonthlyStaysSingleRow proves the Monthly granularity path is
// unchanged: one aggregate row for the period, carrying a BillingMonth date
// column (not one row per day). Sent as raw JSON because the query SDK cannot
// express a Monthly granularity.
func TestQueryUsage_MonthlyStaysSingleRow(t *testing.T) {
	url := newCostServerURL(t)

	body := `{"type":"ActualCost","timeframe":"MonthToDate",` +
		`"dataset":{"granularity":"Monthly","aggregation":{"totalCost":{"name":"Cost","function":"Sum"}}}}`

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		url+"/"+subscriptionScope+"/providers/Microsoft.CostManagement/query", bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out costQueryResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))

	require.Len(t, out.Properties.Rows, 1, "Monthly granularity stays one row per period")

	var hasBillingMonth bool
	for _, c := range out.Properties.Columns {
		if c.Name == "BillingMonth" {
			hasBillingMonth = true
		}
	}

	assert.True(t, hasBillingMonth, "Monthly query carries a BillingMonth column, got %+v", out.Properties.Columns)
}

// newCostServerURL seeds the same estate as newCostClient and returns the raw
// base URL of an in-process cloudemu server over it, for tests that POST a query
// body the typed SDK cannot express.
func newCostServerURL(t *testing.T) string {
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
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts.URL
}
