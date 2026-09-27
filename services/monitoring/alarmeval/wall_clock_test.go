package alarmeval_test

import (
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wallParams(period, evalPeriods int, loc *time.Location) alarmeval.Params {
	return alarmeval.Params{
		Period: period, EvaluationPeriods: evalPeriods, Stat: "Sum",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 1,
		ExtendedRange: true, WallClock: true, Location: loc,
	}
}

func point(ts time.Time, v float64) []driver.MetricDatum {
	return []driver.MetricDatum{{Value: v, Timestamp: ts}}
}

// At 12:07 a 5-minute wall clock alarm sees only [12:00, 12:05).
func TestWallClockEvaluatesCompletedPeriod(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 7, 0, 0, time.UTC)
	p := wallParams(300, 1, nil)

	assert.Equal(t, time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC), p.WindowStart(now))

	in := point(time.Date(2025, 1, 1, 12, 4, 59, 0, time.UTC), 5)
	assert.Equal(t, alarmeval.StateAlarm, alarmeval.EvaluateWindow(in, &p, now).State)
	assert.Equal(t, time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC), alarmeval.EvaluatedStart(in, &p, now))

	before := point(time.Date(2025, 1, 1, 11, 59, 59, 0, time.UTC), 5)
	assert.Equal(t, alarmeval.StateInsufficientData, alarmeval.EvaluateWindow(before, &p, now).State)
}

// A point in the current period is seen only once that period ends.
func TestWallClockIgnoresCurrentPeriod(t *testing.T) {
	p := wallParams(300, 1, nil)
	late := point(time.Date(2025, 1, 1, 12, 6, 0, 0, time.UTC), 5)

	at0807 := time.Date(2025, 1, 1, 12, 7, 0, 0, time.UTC)
	assert.Equal(t, alarmeval.StateInsufficientData, alarmeval.EvaluateWindow(late, &p, at0807).State)

	at0809 := time.Date(2025, 1, 1, 12, 9, 59, 0, time.UTC)
	assert.Equal(t, alarmeval.StateInsufficientData, alarmeval.EvaluateWindow(late, &p, at0809).State)

	at0810 := time.Date(2025, 1, 1, 12, 10, 0, 0, time.UTC)
	assert.Equal(t, alarmeval.StateAlarm, alarmeval.EvaluateWindow(late, &p, at0810).State)
}

// A wall clock window looks back exactly EvaluationPeriods, so an older
// point past that is not used, unlike the sliding evaluation range.
func TestWallClockHasNoExtendedRange(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 7, 0, 0, time.UTC)
	old := point(time.Date(2025, 1, 1, 11, 52, 0, 0, time.UTC), 5)

	wall := wallParams(300, 2, nil)
	assert.Equal(t, time.Date(2025, 1, 1, 11, 55, 0, 0, time.UTC), wall.WindowStart(now))
	assert.Equal(t, alarmeval.StateInsufficientData, alarmeval.EvaluateWindow(old, &wall, now).State)

	sliding := wall
	sliding.WallClock = false
	assert.Equal(t, now.Add(-4*5*time.Minute), sliding.WindowStart(now), "sliding keeps N+2")
}

// A +05:30 zone moves the daily boundary to 18:30 UTC.
func TestWallClockTimezoneDaily(t *testing.T) {
	loc, err := alarmeval.ParseTimezone("+05:30")
	require.NoError(t, err)

	p := wallParams(86400, 1, loc)
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC) // 05:30 on Jan 2 local

	assert.Equal(t, time.Date(2024, 12, 31, 18, 30, 0, 0, time.UTC), p.WindowStart(now).UTC())

	lastLocalDay := point(time.Date(2025, 1, 1, 18, 29, 0, 0, time.UTC), 5)
	assert.Equal(t, alarmeval.StateAlarm, alarmeval.EvaluateWindow(lastLocalDay, &p, now).State)

	today := point(time.Date(2025, 1, 1, 18, 31, 0, 0, time.UTC), 5)
	assert.Equal(t, alarmeval.StateInsufficientData, alarmeval.EvaluateWindow(today, &p, now).State)
}

// Weekly windows start on Monday at 00:00.
func TestWallClockWeekly(t *testing.T) {
	p := wallParams(604800, 1, nil)
	now := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC) // Wednesday

	assert.Equal(t, time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC), p.WindowStart(now))
}

// Hourly windows in an IANA zone follow local hours across a DST change.
func TestWallClockHourlyAcrossDST(t *testing.T) {
	loc, err := alarmeval.ParseTimezone("America/New_York")
	require.NoError(t, err)

	p := wallParams(3600, 1, loc)
	// 2025-03-09 03:30 EDT, one hour after the 02:00 spring-forward gap.
	now := time.Date(2025, 3, 9, 3, 30, 0, 0, loc)

	assert.Equal(t, time.Date(2025, 3, 9, 1, 0, 0, 0, loc), p.WindowStart(now))
}

func TestParseTimezone(t *testing.T) {
	ok := map[string]int{
		"":           0,
		"UTC":        0,
		"Z":          0,
		"+05:30":     5*3600 + 30*60,
		"UTC+05:30":  5*3600 + 30*60,
		"GMT-08:00":  -8 * 3600,
		"+01:05":     3600 + 5*60,
		"Asia/Tokyo": 9 * 3600,
	}

	ref := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	for in, want := range ok {
		loc, err := alarmeval.ParseTimezone(in)
		require.NoError(t, err, in)

		_, off := ref.In(loc).Zone()
		assert.Equal(t, want, off, in)
	}

	for _, in := range []string{"+01:03", "Nowhere/Nope", "Local", "+25:00", "UTC+5"} {
		_, err := alarmeval.ParseTimezone(in)
		assert.Error(t, err, in)
	}
}

// WallClockPeriod lists the periods a wall clock window allows.
func TestWallClockPeriod(t *testing.T) {
	for _, p := range []int{60, 300, 3600, 86400, 604800} {
		assert.True(t, alarmeval.WallClockPeriod(p), p)
	}

	for _, p := range []int{10, 30, 120, 600, 7200} {
		assert.False(t, alarmeval.WallClockPeriod(p), p)
	}
}
