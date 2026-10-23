package cloudwatch

import (
	"context"
	"strings"
	"testing"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

func TestValidatePeriod(t *testing.T) {
	m := newTestMock()

	for _, p := range []int{1, 5, 10, 20, 30, 60, 120, 300, 3600, 86400, 2147483640} {
		if err := m.ValidatePeriod("Period", p); err != nil {
			t.Errorf("period %d: unexpected error %v", p, err)
		}
	}

	for _, p := range []int{0, -1, -60, 2, 15, 45, 61, 90, 2147483700, 540431955284459520} {
		err := m.ValidatePeriod("MetricDataQueries.member.1.MetricStat.Period", p)
		if !cerrors.IsInvalidArgument(err) {
			t.Fatalf("period %d: want InvalidArgument, got %v", p, err)
		}

		if !strings.Contains(cerrors.Message(err), "parameter MetricDataQueries.member.1.MetricStat.Period is not a valid period") {
			t.Errorf("period %d: message should name the parameter: %s", p, cerrors.Message(err))
		}
	}
}

// TestGetMetricDataRejectsBadPeriod covers a Period that used to overflow the
// bucket step to zero (divide by zero) or to a negative value.
func TestGetMetricDataRejectsBadPeriod(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	now := m.opts.Clock.Now()

	requireNoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "App", MetricName: "Latency", Value: 5, Timestamp: now.Add(-time.Minute)},
	}))

	in := driver.GetMetricInput{
		Namespace: "App", MetricName: "Latency", StartTime: now.Add(-time.Hour), EndTime: now, Stat: "Sum",
	}

	for _, p := range []int{-60, 61, 540431955284459520, 9223372036854775800} {
		in.Period = p

		if _, err := m.GetMetricData(ctx, in); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("period %d: want InvalidArgument, got %v", p, err)
		}
	}

	in.Period = 0

	res, err := m.GetMetricData(ctx, in)
	requireNoError(t, err)

	if len(res.Values) != 1 || res.Values[0] != 5 {
		t.Fatalf("zero period should default to a minute: %+v", res)
	}
}

// TestBuildMetricResultLongPeriod checks that a period longer than a
// Duration can hold still buckets every point into one period.
func TestBuildMetricResultLongPeriod(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	data := []driver.MetricDatum{
		{Value: 1, Timestamp: start.Add(time.Hour)},
		{Value: 2, Timestamp: start.Add(48 * time.Hour)},
	}

	res := buildMetricResult(data, start, 540431955284459520, "Sum")
	if len(res.Values) != 1 || res.Values[0] != 3 || !res.Timestamps[0].Equal(start) {
		t.Fatalf("want one bucket of 3 at start, got %+v", res)
	}

	if res := buildMetricResult(data, start, 0, "Sum"); len(res.Values) != 0 {
		t.Fatalf("a zero step should return no points, got %+v", res)
	}
}
