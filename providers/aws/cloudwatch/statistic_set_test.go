package cloudwatch

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestGetMetricDataStatisticSet verifies that a datum published as a
// pre-aggregated StatisticValues set (SampleCount/Sum/Min/Max) is folded into
// the series so every statistic reflects the supplied aggregate.
func TestGetMetricDataStatisticSet(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	requireNoError(t, m.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace:  "NS",
		MetricName: "Batch",
		Timestamp:  base,
		StatisticValues: &driver.StatisticSet{
			SampleCount: 4,
			Sum:         100,
			Minimum:     10,
			Maximum:     40,
		},
	}}))

	get := func(stat string) float64 {
		res, err := m.GetMetricData(ctx, driver.GetMetricInput{
			Namespace: "NS", MetricName: "Batch",
			StartTime: base, EndTime: base.Add(time.Minute), Period: 60, Stat: stat,
		})
		requireNoError(t, err)
		assertEqual(t, 1, len(res.Values))

		return res.Values[0]
	}

	assertEqual(t, 100.0, get("Sum"))
	assertEqual(t, 25.0, get("Average"))
	assertEqual(t, 10.0, get("Minimum"))
	assertEqual(t, 40.0, get("Maximum"))
	assertEqual(t, 4.0, get("SampleCount"))
}

// TestGetMetricDataValuesCounts verifies that paired Values/Counts arrays weight
// each observation by its count when the series is aggregated.
func TestGetMetricDataValuesCounts(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	requireNoError(t, m.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace:  "NS",
		MetricName: "Weighted",
		Timestamp:  base,
		Values:     []float64{1, 2, 3},
		Counts:     []float64{4, 1, 1},
	}}))

	get := func(stat string) float64 {
		res, err := m.GetMetricData(ctx, driver.GetMetricInput{
			Namespace: "NS", MetricName: "Weighted",
			StartTime: base, EndTime: base.Add(time.Minute), Period: 60, Stat: stat,
		})
		requireNoError(t, err)
		assertEqual(t, 1, len(res.Values))

		return res.Values[0]
	}

	assertEqual(t, 9.0, get("Sum"))         // 1*4 + 2 + 3
	assertEqual(t, 6.0, get("SampleCount")) // 4 + 1 + 1
	assertEqual(t, 1.5, get("Average"))     // 9 / 6
	assertEqual(t, 1.0, get("Minimum"))
	assertEqual(t, 3.0, get("Maximum"))
}
