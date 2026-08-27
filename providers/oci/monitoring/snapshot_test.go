package monitoring

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestSnapshotRestoreRoundTrip seeds metric series, a fired alarm (with state
// history) and a notification channel, snapshots, restores into a fresh mock and
// asserts each store's state comes back — including the alarm's history and the
// series' samples, both promoted from unexported fields.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	src, now := newMock(t)
	ctx := t.Context()

	alarm, err := src.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "high-cpu", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	// A breaching sample fires the alarm, writing a state-change history entry.
	post(t, src, compartmentA, "CpuUtilization", now.Add(-10*time.Second), 95)

	ch, err := src.CreateNotificationChannel(ctx, driver.NotificationChannelConfig{
		Name: "ops", Type: "EMAIL", Endpoint: "ops@example.com",
	})
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst, _ := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	// The alarm is back under its OCID, still FIRING, with its history intact.
	fired, err := dst.GetOCIAlarm(ctx, alarm.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFiring, fired.Status)
	assert.False(t, fired.TimeTriggered.IsZero())

	history, err := dst.OCIAlarmHistory(ctx, alarm.ID, 0)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, StatusOK, history[0].OldState)
	assert.Equal(t, StatusFiring, history[0].NewState)

	// The metric series survived with its sample: a summarize query aggregates it.
	metrics, err := dst.SummarizeOCIMetrics(ctx, compartmentA, OCIMetricQuery{
		Namespace: namespace,
		Query:     "CpuUtilization[1m].mean()",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.NotEmpty(t, metrics[0].Values)
	assert.InDelta(t, 95.0, metrics[0].Values[0], 0.001)

	// The notification channel is back under its OCID.
	gotCh, err := dst.GetNotificationChannel(ctx, ch.ID)
	require.NoError(t, err)
	assert.Equal(t, "ops", gotCh.Name)
}

// TestSnapshotRestoreEmptyNilSafe confirms an empty mock round-trips cleanly.
func TestSnapshotRestoreEmptyNilSafe(t *testing.T) {
	src, _ := newMock(t)
	ctx := t.Context()

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst, _ := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	_, err = dst.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "fresh", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)
}
