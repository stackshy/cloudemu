package cloudwatch

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

// Tick reports a change only when an alarm moves state.
func TestTickReportsTransitions(t *testing.T) {
	m, fc, _ := newClockMock()
	putValue(t, m, fc, lazyNS, 10)
	requireNoError(t, m.CreateAlarm(context.Background(), lazyAlarm("tick", 60, "")))

	assertEqual(t, false, m.Tick(fc.Now()))

	fc.Advance(2 * time.Minute)
	assertEqual(t, true, m.Tick(fc.Now()))
	assertEqual(t, false, m.Tick(fc.Now()))
}

// StateTransitionedTimestamp moves only when the state changes. With no
// EvaluationState, StateUpdatedTimestamp moves with it. See API_MetricAlarm.
func TestAlarmTimestamps(t *testing.T) {
	ctx := context.Background()
	m, fc, _ := newClockMock()
	created := fc.Now()
	putValue(t, m, fc, lazyNS, 10)
	requireNoError(t, m.CreateAlarm(ctx, lazyAlarm("ts", 300, "")))

	fc.Advance(time.Minute)
	putValue(t, m, fc, lazyNS, 10) // re-evaluates, still ALARM

	a, err := m.DescribeAlarms(ctx, []string{"ts"})
	requireNoError(t, err)
	assertEqual(t, created, a[0].StateTransitionedTimestamp)
	assertEqual(t, created, a[0].StateUpdatedTimestamp)

	fc.Advance(6 * time.Minute)

	a, err = m.DescribeAlarms(ctx, []string{"ts"})
	requireNoError(t, err)
	assertEqual(t, stateInsufficientData, a[0].State)
	assertEqual(t, fc.Now(), a[0].StateTransitionedTimestamp)
	assertEqual(t, fc.Now(), a[0].StateUpdatedTimestamp)
}

// A multi-day alarm runs hourly and only sees data up to the top of the hour.
func TestMultiDayAlarmTopOfHour(t *testing.T) {
	m, fc, _ := newClockMock()
	cfg := lazyAlarm("daily", 3600, "")
	cfg.EvaluationPeriods = 25
	cfg.DatapointsToAlarm = 1

	fc.Advance(30 * time.Minute)
	putValue(t, m, fc, lazyNS, 10)
	requireNoError(t, m.CreateAlarm(context.Background(), cfg))
	assertEqual(t, stateInsufficientData, stateOf(t, m, "daily"))

	fc.Advance(59 * time.Minute)
	assertEqual(t, stateInsufficientData, stateOf(t, m, "daily"))

	fc.Advance(time.Minute)
	assertEqual(t, stateAlarm, stateOf(t, m, "daily"))
}

// LastEvaluatedAt survives a snapshot, so a forced state still holds after a
// restore.
func TestLastEvaluatedAtPersists(t *testing.T) {
	ctx := context.Background()
	m, fc, _ := newClockMock()
	requireNoError(t, m.CreateAlarm(ctx, lazyAlarm("snap", 60, "")))
	requireNoError(t, m.SetAlarmState(ctx, "snap", stateAlarm, "forced"))

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := New(config.NewOptions(config.WithClock(fc)))
	requireNoError(t, restored.Restore(ctx, data))

	assertEqual(t, stateAlarm, stateOf(t, restored, "snap"))

	fc.Advance(time.Minute)
	assertEqual(t, stateInsufficientData, stateOf(t, restored, "snap"))
}
