package cloudwatch

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
)

// twoWeeksOfMinutes is a full training window at one point a minute.
const twoWeeksOfMinutes = 20160

// scaleLimit is how long one call on two weeks of data may take.
func scaleLimit() time.Duration {
	if raceEnabled {
		return 10 * time.Second
	}

	return time.Second
}

// newScaleMock stores two weeks of one-minute data and a band alarm on it.
// The clock is one minute past the last point.
func newScaleMock(tb testing.TB) (*Mock, *config.FakeClock) {
	tb.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc)))

	data := make([]driver.MetricDatum, twoWeeksOfMinutes)
	for i := range data {
		data[i] = driver.MetricDatum{
			Namespace: bandNS, MetricName: bandMetric, Value: float64(10 + i%3),
			Timestamp: fc.Now().Add(time.Duration(i) * time.Minute),
		}
	}

	if err := m.PutMetricData(context.Background(), data); err != nil {
		tb.Fatal(err)
	}

	fc.Advance(twoWeeksOfMinutes * time.Minute)

	if err := m.CreateAlarm(context.Background(), bandAlarm("band", "GreaterThanUpperThreshold")); err != nil {
		tb.Fatal(err)
	}

	return m, fc
}

// One PutMetricData on a metric with two weeks of history re-evaluates the
// band alarm quickly, and so does a two-week band over every point.
func TestBandScale(t *testing.T) {
	m, fc := newScaleMock(t)

	start := time.Now()
	putBand(t, m, fc.Now(), 100)

	if d := time.Since(start); d > scaleLimit() {
		t.Fatalf("PutMetricData took %v", d)
	}

	assertEqual(t, stateAlarm, stateOf(t, m, "band"))

	a, _ := m.alarms.Get("band")
	from := fc.Now().Add(-metricmath.TrainingWindow)
	ev := metricmath.New(a.Metrics, m.rangeFetcher(from, fc.Now().Add(time.Second)))

	start = time.Now()

	band, _, err := ev.BandAt("ad1", 60)
	requireNoError(t, err)

	if d := time.Since(start); d > scaleLimit() {
		t.Fatalf("BandAt took %v", d)
	}

	if len(band.Timestamps) < twoWeeksOfMinutes-metricmath.MinTrainingPoints {
		t.Fatalf("band has %d points", len(band.Timestamps))
	}
}

func BenchmarkPutMetricDataBandAlarm(b *testing.B) {
	m, fc := newScaleMock(b)
	ctx := context.Background()

	b.ResetTimer()

	for range b.N {
		err := m.PutMetricData(ctx, []driver.MetricDatum{{Namespace: bandNS, MetricName: bandMetric, Value: 11, Timestamp: fc.Now()}})
		if err != nil {
			b.Fatal(err)
		}
	}
}
