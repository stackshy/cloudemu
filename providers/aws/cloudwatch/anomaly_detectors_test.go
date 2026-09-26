package cloudwatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	bandNS     = "A/App"
	bandMetric = "Lat"
)

// bandAlarm compares A/App Lat (Average, 60s) with ANOMALY_DETECTION_BAND(m1, 2).
func bandAlarm(name, op string) driver.AlarmConfig {
	return driver.AlarmConfig{
		Name: name, ComparisonOperator: op, EvaluationPeriods: 1, ThresholdMetricID: "ad1",
		Metrics: []driver.MetricDataQuery{
			{ID: "m1", ReturnData: boolRef(true), MetricStat: &driver.MetricStat{Namespace: bandNS, MetricName: bandMetric, Period: 60, Stat: "Average"}},
			{ID: "ad1", Expression: "ANOMALY_DETECTION_BAND(m1, 2)", ReturnData: boolRef(true)},
		},
	}
}

func putBand(t *testing.T, m *Mock, at time.Time, v float64) {
	t.Helper()

	requireNoError(t, m.PutMetricData(context.Background(), []driver.MetricDatum{
		{Namespace: bandNS, MetricName: bandMetric, Value: v, Timestamp: at},
	}))
}

// putSeries puts one value a minute, starting now, and leaves the clock one
// minute after the last point.
func putSeries(t *testing.T, m *Mock, fc *config.FakeClock, values ...float64) {
	t.Helper()

	for _, v := range values {
		putBand(t, m, fc.Now(), v)
		fc.Advance(time.Minute)
	}
}

func flat(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}

	return out
}

// A steady series stays inside its band. A spike above mean+2sd, or a dip
// below mean-2sd, breaches it. Before CW-7a a band alarm stayed
// INSUFFICIENT_DATA.
func TestBandAlarmDirections(t *testing.T) {
	tests := []struct {
		name  string
		op    string
		value float64
		want  string
	}{
		{"steady outside", "LessThanLowerOrGreaterThanUpperThreshold", 10, stateOK},
		{"spike outside", "LessThanLowerOrGreaterThanUpperThreshold", 100, stateAlarm},
		{"dip outside", "LessThanLowerOrGreaterThanUpperThreshold", 0, stateAlarm},
		{"spike above", "GreaterThanUpperThreshold", 100, stateAlarm},
		{"dip above", "GreaterThanUpperThreshold", 0, stateOK},
		{"dip below", "LessThanLowerThreshold", 0, stateAlarm},
		{"spike below", "LessThanLowerThreshold", 100, stateOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, fc, _ := newClockMock()
			ctx := context.Background()

			putSeries(t, m, fc, 9, 11, 9, 11, 9, 11, 9, 11, 9, 11)
			requireNoError(t, m.CreateAlarm(ctx, bandAlarm("band", tc.op)))

			putBand(t, m, fc.Now(), tc.value)
			assertEqual(t, tc.want, stateOf(t, m, "band"))
		})
	}
}

// With fewer than three earlier points the band has no value, so the alarm
// has no usable data.
func TestBandAlarmNeedsTraining(t *testing.T) {
	m, fc, _ := newClockMock()

	putSeries(t, m, fc, 10, 10)
	requireNoError(t, m.CreateAlarm(context.Background(), bandAlarm("band", "GreaterThanUpperThreshold")))
	putBand(t, m, fc.Now(), 100)

	assertEqual(t, stateInsufficientData, stateOf(t, m, "band"))
}

// The stateReasonData of an anomaly alarm reports the band edges and no
// threshold. The SNS trigger names the band instead of a threshold.
func TestBandAlarmReasonData(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	putSeries(t, m, fc, flat(5, 10)...)
	requireNoError(t, m.CreateAlarm(ctx, bandAlarm("band", "GreaterThanUpperThreshold")))
	putBand(t, m, fc.Now(), 100)

	alarms, err := m.DescribeAlarms(ctx, []string{"band"})
	requireNoError(t, err)

	var data map[string]any
	requireNoError(t, json.Unmarshal([]byte(alarms[0].StateReasonData), &data))

	if _, ok := data["threshold"]; ok {
		t.Fatalf("reason data has a threshold: %s", alarms[0].StateReasonData)
	}

	upper, _ := data["recentUpperThresholds"].([]any)
	lower, _ := data["recentLowerThresholds"].([]any)
	if len(upper) != 1 || len(lower) != 1 || upper[0].(float64) < 10 || upper[0].(float64) > 10.001 {
		t.Fatalf("reason data = %s", alarms[0].StateReasonData)
	}

	a, _ := m.alarms.Get("band")
	trigger := notificationTrigger(a)

	if _, ok := trigger["Threshold"]; ok || trigger["ThresholdMetricId"] != "ad1" {
		t.Fatalf("trigger = %v", trigger)
	}
}

// Evaluating an anomaly alarm creates its missing detector, on the
// PutMetricData path and on the Tick path, without deadlocking. A deleted
// detector comes back on the next evaluation, as the AWS docs describe.
func TestBandAlarmCreatesDetector(t *testing.T) {
	for _, path := range []string{"put", "tick"} {
		t.Run(path, func(t *testing.T) {
			m, fc, _ := newClockMock()
			ctx := context.Background()

			withTimeout(t, func() {
				requireNoError(t, m.CreateAlarm(ctx, bandAlarm("band", "GreaterThanUpperThreshold")))
			})
			assertEqual(t, 1, len(describeDetectors(t, m)))

			requireNoError(t, m.DeleteAnomalyDetector(ctx, driver.AnomalyDetector{Namespace: bandNS, MetricName: bandMetric, Stat: "Average"}))
			assertEqual(t, 0, len(describeDetectors(t, m)))

			withTimeout(t, func() {
				if path == "put" {
					putBand(t, m, fc.Now(), 1)
					return
				}

				fc.Advance(time.Minute)
				m.Tick(fc.Now())
			})

			got := describeDetectors(t, m)
			if len(got) != 1 || got[0].Namespace != bandNS || got[0].MetricName != bandMetric || got[0].Stat != "Average" {
				t.Fatalf("detectors = %+v", got)
			}
		})
	}
}

// A metric-math band trains on its expression and creates a METRIC_MATH
// detector over the list without the band entry.
func TestMathBandAlarm(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	alarm := driver.AlarmConfig{
		Name: "rate-band", ComparisonOperator: "GreaterThanUpperThreshold", EvaluationPeriods: 1, ThresholdMetricID: "ad1",
		Metrics: []driver.MetricDataQuery{
			{ID: "m1", ReturnData: boolRef(false), MetricStat: &driver.MetricStat{Namespace: bandNS, MetricName: bandMetric, Period: 60, Stat: "Sum"}},
			{ID: "e1", Expression: "m1*2", ReturnData: boolRef(true)},
			{ID: "ad1", Expression: "ANOMALY_DETECTION_BAND(e1, 2)", ReturnData: boolRef(true)},
		},
	}

	putSeries(t, m, fc, flat(5, 10)...)
	requireNoError(t, m.CreateAlarm(ctx, alarm))
	putBand(t, m, fc.Now(), 50)

	assertEqual(t, stateAlarm, stateOf(t, m, "rate-band"))

	got := describeDetectors(t, m)
	if len(got) != 1 || len(got[0].Metrics) != 2 || got[0].Metrics[1].ID != "e1" || !*got[0].Metrics[1].ReturnData || *got[0].Metrics[0].ReturnData {
		t.Fatalf("detectors = %+v", got)
	}
}

// ExcludedTimeRanges leave noisy history out of training, which narrows the
// band enough for a small step to breach it.
func TestBandExcludedTimeRanges(t *testing.T) {
	for _, exclude := range []bool{false, true} {
		m, fc, _ := newClockMock()
		ctx := context.Background()

		noisyStart := fc.Now()
		putSeries(t, m, fc, 0, 20, 0, 20, 0, 20, 0, 20)
		noisyEnd := fc.Now().Add(-time.Minute) // the last noisy point
		putSeries(t, m, fc, 10, 10, 10)

		if exclude {
			requireNoError(t, m.PutAnomalyDetector(ctx, driver.AnomalyDetector{
				Namespace: bandNS, MetricName: bandMetric, Stat: "Average",
				ExcludedTimeRanges: []driver.TimeRange{{StartTime: noisyStart, EndTime: noisyEnd}},
			}))
		}

		requireNoError(t, m.CreateAlarm(ctx, bandAlarm("band", "GreaterThanUpperThreshold")))
		putBand(t, m, fc.Now(), 11)

		want := stateOK
		if exclude {
			want = stateAlarm
		}

		assertEqual(t, want, stateOf(t, m, "band"))
	}
}

// A detector reports PENDING_TRAINING while it settles and until it has three
// periods of data, then TRAINED. Once its data ages out of the two-week
// window it reports TRAINED_INSUFFICIENT_DATA.
func TestAnomalyDetectorState(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc), config.WithAsyncSettle()))
	ctx := context.Background()

	requireNoError(t, m.PutAnomalyDetector(ctx, driver.AnomalyDetector{Namespace: bandNS, MetricName: bandMetric, Stat: "Average"}))
	putSeries(t, m, fc, 10, 10, 10)
	assertEqual(t, detectorTrained, describeDetectors(t, m)[0].StateValue)

	m2 := New(config.NewOptions(config.WithClock(fc), config.WithAsyncSettle()))
	requireNoError(t, m2.PutAnomalyDetector(ctx, driver.AnomalyDetector{Namespace: bandNS, MetricName: bandMetric, Stat: "Average"}))
	assertEqual(t, detectorPendingTraining, describeDetectors(t, m2)[0].StateValue)

	fc.Advance(anomalyTrainingSettle)
	assertEqual(t, detectorPendingTraining, describeDetectors(t, m2)[0].StateValue)

	putSeries(t, m2, fc, 10, 10, 10)
	assertEqual(t, detectorTrained, describeDetectors(t, m2)[0].StateValue)

	fc.Advance(15 * 24 * time.Hour)
	assertEqual(t, detectorTrainedInsufficient, describeDetectors(t, m2)[0].StateValue)
}

// Put replaces the configuration of the detector on the same metric, and
// Delete of a missing detector is NotFound.
func TestAnomalyDetectorPutReplaceDelete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	d := driver.AnomalyDetector{Namespace: bandNS, MetricName: bandMetric, Stat: "Average", Dimensions: map[string]string{"Host": "a"}}
	d.ExcludedTimeRanges = []driver.TimeRange{{StartTime: time.Unix(0, 0), EndTime: time.Unix(60, 0)}}
	requireNoError(t, m.PutAnomalyDetector(ctx, d))

	d.ExcludedTimeRanges = nil
	d.MetricTimezone = "Europe/Berlin"
	requireNoError(t, m.PutAnomalyDetector(ctx, d))

	got := describeDetectors(t, m)
	if len(got) != 1 || len(got[0].ExcludedTimeRanges) != 0 || got[0].MetricTimezone != "Europe/Berlin" {
		t.Fatalf("detectors = %+v", got)
	}

	requireNoError(t, m.DeleteAnomalyDetector(ctx, d))
	assertError(t, m.DeleteAnomalyDetector(ctx, d), true)
	assertError(t, m.PutAnomalyDetector(ctx, driver.AnomalyDetector{Namespace: bandNS}), true)
}

// Detectors survive a snapshot and restore.
func TestAnomalyDetectorSnapshot(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	requireNoError(t, src.PutAnomalyDetector(ctx, driver.AnomalyDetector{
		Namespace: bandNS, MetricName: bandMetric, Stat: "p90", MetricTimezone: "UTC", PeriodicSpikes: boolRef(true),
		ExcludedTimeRanges: []driver.TimeRange{{StartTime: time.Unix(0, 0).UTC(), EndTime: time.Unix(60, 0).UTC()}},
	}))

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	got := describeDetectors(t, dst)
	if len(got) != 1 || got[0].Stat != "p90" || !*got[0].PeriodicSpikes || len(got[0].ExcludedTimeRanges) != 1 {
		t.Fatalf("restored detectors = %+v", got)
	}

	if !strings.Contains(string(data), "anomalyDetectors") {
		t.Fatalf("snapshot has no detectors: %s", data)
	}
}

func describeDetectors(t *testing.T, m *Mock) []driver.AnomalyDetector {
	t.Helper()

	out, err := m.DescribeAnomalyDetectors(context.Background())
	requireNoError(t, err)

	return out
}

// withTimeout fails the test when fn does not return quickly, which is how
// a lock taken twice shows up.
func withTimeout(t *testing.T, fn func()) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)
		fn()
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out: possible deadlock")
	}
}

// Changing an existing alarm into a band alarm creates its detector at once,
// the same as creating a new band alarm does.
func TestAnomalyDetectorCreatedWhenAlarmBecomesBand(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc), config.WithAsyncSettle()))
	ctx := context.Background()

	requireNoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{
		Name: "lat", Namespace: bandNS, MetricName: bandMetric, Stat: "Average",
		Period: 60, EvaluationPeriods: 1, Threshold: 100, ComparisonOperator: "GreaterThanThreshold",
	}))
	assertEqual(t, 0, len(describeDetectors(t, m)))

	requireNoError(t, m.CreateAlarm(ctx, bandAlarm("lat", "GreaterThanUpperThreshold")))
	assertEqual(t, 1, len(describeDetectors(t, m)))
}
