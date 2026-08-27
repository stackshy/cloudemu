package monitor

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRestoreRoundTrip proves the Azure Monitor mock serializes its
// entire state — metric buffer, alarms, and action groups — and restores it into
// a fresh mock identity-preservingly.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src, _ := newTestMock()

	require.NoError(t, src.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace: "Microsoft.Compute/virtualMachines", MetricName: "Percentage CPU",
		Value: 42, Unit: "Percent", Dimensions: map[string]string{"resourceId": "vm-1"},
	}}))

	require.NoError(t, src.CreateAlarm(ctx, driver.AlarmConfig{
		Name: "high-cpu", Namespace: "Microsoft.Compute/virtualMachines", MetricName: "Percentage CPU",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 80, Period: 60,
		EvaluationPeriods: 1, Stat: "Average",
	}))

	src.RegisterActionGroup("/ag/ops", map[string]any{"enabled": true})

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
