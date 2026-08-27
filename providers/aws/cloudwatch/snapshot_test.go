package cloudwatch

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestSnapshotRestoreRoundTrip proves the CloudWatch mock serializes its entire
// state — metric buffer, alarms, and dashboards — and restores it into a fresh
// mock identity-preservingly.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()

	requireNoError(t, src.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace: "AWS/EC2", MetricName: "CPUUtilization", Value: 42, Unit: "Percent",
		Dimensions: map[string]string{"InstanceId": "i-123"},
	}}))

	requireNoError(t, src.CreateAlarm(ctx, driver.AlarmConfig{
		Name: "high-cpu", Namespace: "AWS/EC2", MetricName: "CPUUtilization",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 80, Period: 60,
		EvaluationPeriods: 1, Stat: "Average",
	}))

	requireNoError(t, src.PutDashboard(ctx, "ops", `{"widgets":[]}`))

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	requireNoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("snapshot not stable across restore:\n first=%s\nsecond=%s", data, data2)
	}

	alarms, err := dst.DescribeAlarms(ctx, []string{"high-cpu"})
	requireNoError(t, err)
	if len(alarms) != 1 || alarms[0].Name != "high-cpu" {
		t.Fatalf("restored alarms = %v, want name high-cpu", alarms)
	}

	dash, err := dst.GetDashboard(ctx, "ops")
	requireNoError(t, err)
	if dash == nil {
		t.Fatalf("restored dashboard ops = nil")
	}
}

// TestSnapshotEmpty confirms a fresh mock snapshots and restores without error.
func TestSnapshotEmpty(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, false)
	requireNoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("empty snapshot not stable: %s vs %s", data, data2)
	}
}
