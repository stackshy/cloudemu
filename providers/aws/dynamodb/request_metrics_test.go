package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestRequestMetricsMatchRealDynamoDB pins the per-request metrics to the real
// AWS/DynamoDB taxonomy: SuccessfulRequestLatency (Milliseconds) and
// ReturnedItemCount (Count) on {TableName, Operation}, and no fabricated
// SuccessfulRequestCount metric.
func TestRequestMetricsMatchRealDynamoDB(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc))
	m := New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()

	createTestTable(m, "tbl")
	requireNoError(t, m.PutItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "a"}))
	requireNoError(t, m.PutItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "b"}))

	_, err := m.GetItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "a"})
	requireNoError(t, err)

	_, err = m.Query(ctx, driver.QueryInput{
		Table: "tbl", KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "u1"},
	})
	requireNoError(t, err)

	_, err = m.Scan(ctx, driver.ScanInput{Table: "tbl"})
	requireNoError(t, err)

	get := func(name, stat string, dims map[string]string) *mondriver.MetricDataResult {
		t.Helper()

		res, gerr := cw.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace: "AWS/DynamoDB", MetricName: name, Dimensions: dims,
			StartTime: fc.Now().Add(-time.Minute), EndTime: fc.Now().Add(time.Minute),
			Period: 60, Stat: stat,
		})
		requireNoError(t, gerr)

		return res
	}

	names, err := cw.ListMetrics(ctx, "AWS/DynamoDB")
	requireNoError(t, err)

	for _, n := range names {
		if n == "SuccessfulRequestCount" {
			t.Fatal("SuccessfulRequestCount is not a real DynamoDB metric and must not be published")
		}
	}

	latency := map[string]float64{"PutItem": 2, "GetItem": 1, "Query": 1, "Scan": 1}
	for op, count := range latency {
		res := get("SuccessfulRequestLatency", "SampleCount", map[string]string{"TableName": "tbl", "Operation": op})
		if len(res.Values) != 1 || res.Values[0] != count {
			t.Errorf("SuccessfulRequestLatency{%s} SampleCount = %v, want %v", op, res.Values, count)
		}

		assertEqual(t, "Milliseconds", res.Unit)
	}

	for _, op := range []string{"Query", "Scan"} {
		res := get("ReturnedItemCount", "Sum", map[string]string{"TableName": "tbl", "Operation": op})
		if len(res.Values) != 1 || res.Values[0] != 2 {
			t.Errorf("ReturnedItemCount{%s} = %v, want [2]", op, res.Values)
		}

		assertEqual(t, "Count", res.Unit)
	}

	// GetItem is not a ReturnedItemCount operation in real DynamoDB.
	res := get("ReturnedItemCount", "Sum", map[string]string{"TableName": "tbl", "Operation": "GetItem"})
	assertEqual(t, 0, len(res.Values))
}
