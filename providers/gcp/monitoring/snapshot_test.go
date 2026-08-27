package monitoring

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRestoreRoundTrip proves the Cloud Monitoring mock serializes its
// entire state — metric buffer and alarms — and restores it into a fresh mock
// identity-preservingly.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src, _ := newTestMock()

	require.NoError(t, src.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace: "compute.googleapis.com", MetricName: "instance/cpu/utilization",
		Value: 0.42, Unit: "None", Dimensions: map[string]string{"instance_id": "inst-1"},
	}}))

	require.NoError(t, src.CreateAlarm(ctx, driver.AlarmConfig{
		Name: "high-cpu", Namespace: "compute.googleapis.com", MetricName: "instance/cpu/utilization",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 0.8, Period: 60,
		EvaluationPeriods: 1, Stat: "Average",
	}))

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("snapshot not stable across restore:\n first=%s\nsecond=%s", data, data2)
	}

	alarms, err := dst.DescribeAlarms(ctx, []string{"high-cpu"})
	require.NoError(t, err)
	require.Len(t, alarms, 1)
	assert.Equal(t, "high-cpu", alarms[0].Name)
}

// TestSnapshotEmpty confirms a fresh mock snapshots and restores without error.
func TestSnapshotEmpty(t *testing.T) {
	ctx := context.Background()

	src, _ := newTestMock()

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, false)
	require.NoError(t, err)

	assert.True(t, bytes.Equal(data, data2))
}
