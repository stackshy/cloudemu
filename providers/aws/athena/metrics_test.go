package athena

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestQueryMetricsPublishedPerWorkGroup pins that a completed query publishes
// the AWS/Athena metrics on {QueryState, QueryType, WorkGroup} (Milliseconds
// times, Bytes for DML ProcessedBytes), and that a workgroup with
// PublishCloudWatchMetricsEnabled=false publishes nothing.
func TestQueryMetricsPublishedPerWorkGroup(t *testing.T) {
	clk := config.NewFakeClock(time.Unix(1_700_000_000, 0))
	opts := config.NewOptions(config.WithClock(clk))
	m := New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()
	out := &driver.ResultConfiguration{OutputLocation: "s3://out/"}

	requireNoError(t, m.CreateWorkGroup(ctx, driver.WorkGroup{
		Name:          "quiet",
		Configuration: driver.WorkGroupConfiguration{PublishCloudWatchMetricsEnabled: ptr(false)},
	}), "CreateWorkGroup")

	for _, in := range []driver.StartQueryExecutionInput{
		{QueryString: "CREATE DATABASE analytics", ResultConfiguration: out},
		{QueryString: "SELECT 1", ResultConfiguration: out},
		{QueryString: "SELECT 2", ResultConfiguration: out, WorkGroup: "quiet"},
	} {
		_, err := m.StartQueryExecution(ctx, in)
		requireNoError(t, err, "StartQueryExecution")
	}

	get := func(name, queryType, wg string) *mondriver.MetricDataResult {
		t.Helper()

		res, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace: "AWS/Athena", MetricName: name,
			Dimensions: map[string]string{"QueryState": "SUCCEEDED", "QueryType": queryType, "WorkGroup": wg},
			StartTime:  clk.Now().Add(-time.Minute), EndTime: clk.Now().Add(time.Minute),
			Period: 60, Stat: "SampleCount",
		})
		requireNoError(t, err, "GetMetricData "+name)

		return res
	}

	for _, c := range []struct{ name, queryType, unit string }{
		{"TotalExecutionTime", "DDL", "Milliseconds"},
		{"EngineExecutionTime", "DDL", "Milliseconds"},
		{"TotalExecutionTime", "DML", "Milliseconds"},
		{"QueryQueueTime", "DML", "Milliseconds"},
		{"QueryPlanningTime", "DML", "Milliseconds"},
		{"ServicePreProcessingTime", "DML", "Milliseconds"},
		{"ServiceProcessingTime", "DML", "Milliseconds"},
		{"ProcessedBytes", "DML", "Bytes"},
	} {
		res := get(c.name, c.queryType, "primary")
		if len(res.Values) != 1 || res.Values[0] != 1 || res.Unit != c.unit {
			t.Errorf("%s{%s} = %v %q, want one sample in %q", c.name, c.queryType, res.Values, res.Unit, c.unit)
		}
	}

	if res := get("ProcessedBytes", "DDL", "primary"); len(res.Values) != 0 {
		t.Errorf("ProcessedBytes must not be reported for DDL, got %v", res.Values)
	}

	if res := get("TotalExecutionTime", "DML", "quiet"); len(res.Values) != 0 {
		t.Errorf("workgroup with publishing disabled must emit nothing, got %v", res.Values)
	}
}
