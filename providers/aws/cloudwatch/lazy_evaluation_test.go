package cloudwatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	lazyNS     = "Lazy/App"
	lazyMetric = "Errors"
	alarmTopic = "arn:aws:sns:us-east-1:123456789012:alarm"
	okTopic    = "arn:aws:sns:us-east-1:123456789012:ok"
	insuffTop  = "arn:aws:sns:us-east-1:123456789012:insufficient"
)

// recordingPublisher records every SNS publish by topic.
type recordingPublisher struct {
	mu     sync.Mutex
	topics []string
}

func (p *recordingPublisher) PublishExternal(_ context.Context, topicARN, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.topics = append(p.topics, topicARN)

	return nil
}

func (p *recordingPublisher) count(topic string) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := 0

	for _, t := range p.topics {
		if t == topic {
			n++
		}
	}

	return n
}

func newClockMock() (*Mock, *config.FakeClock, *recordingPublisher) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc)))
	pub := &recordingPublisher{}
	m.SetSNSPublisher(pub)

	return m, fc, pub
}

// lazyAlarm is a Sum > 5 alarm over one period on lazyNS/lazyMetric.
func lazyAlarm(name string, period int, treat string) driver.AlarmConfig {
	return driver.AlarmConfig{
		Name: name, Namespace: lazyNS, MetricName: lazyMetric,
		ComparisonOperator: "GreaterThanThreshold", Threshold: 5,
		Period: period, EvaluationPeriods: 1, Stat: "Sum", TreatMissingData: treat,
		AlarmActions: []string{alarmTopic}, OKActions: []string{okTopic}, InsufficientDataActions: []string{insuffTop},
	}
}

func putValue(t *testing.T, m *Mock, fc *config.FakeClock, ns string, v float64) {
	t.Helper()

	requireNoError(t, m.PutMetricData(context.Background(), []driver.MetricDatum{
		{Namespace: ns, MetricName: lazyMetric, Value: v, Timestamp: fc.Now()},
	}))
}

func stateOf(t *testing.T, m *Mock, name string) string {
	t.Helper()

	alarms, err := m.DescribeAlarms(context.Background(), []string{name})
	requireNoError(t, err)

	if len(alarms) != 1 {
		t.Fatalf("want 1 alarm %q, got %d", name, len(alarms))
	}

	return alarms[0].State
}

func historyOf(t *testing.T, m *Mock, name string) []driver.AlarmHistoryEntry {
	t.Helper()

	h, err := m.GetAlarmHistory(context.Background(), name, 0)
	requireNoError(t, err)

	return h
}

// An alarm created after its data exists is evaluated at once. PutMetricAlarm:
// "The alarm is then evaluated and its state is set appropriately."
func TestLazyEvalAlarmCreatedAfterData(t *testing.T) {
	m, fc, pub := newClockMock()
	putValue(t, m, fc, lazyNS, 10)

	requireNoError(t, m.CreateAlarm(context.Background(), lazyAlarm("late", 60, "")))

	assertEqual(t, stateAlarm, stateOf(t, m, "late"))
	assertEqual(t, 1, pub.count(alarmTopic))
}

// Once data stops, the next due read moves the alarm to INSUFFICIENT_DATA and
// fires InsufficientDataActions ("missing": all points missing).
func TestLazyEvalFallsBackToInsufficientData(t *testing.T) {
	m, fc, pub := newClockMock()
	putValue(t, m, fc, lazyNS, 10)
	requireNoError(t, m.CreateAlarm(context.Background(), lazyAlarm("stale", 60, "")))
	assertEqual(t, stateAlarm, stateOf(t, m, "stale"))

	// Past the evaluation range of N+2 periods, so every point is missing.
	fc.Advance(4 * time.Minute)

	assertEqual(t, stateInsufficientData, stateOf(t, m, "stale"))
	assertEqual(t, 1, pub.count(insuffTop))

	h := historyOf(t, m, "stale")
	assertEqual(t, 2, len(h))
	assertEqual(t, stateAlarm, h[0].OldState)
	assertEqual(t, stateInsufficientData, h[0].NewState)
}

// "ignore" keeps the current state when every point is missing.
func TestLazyEvalIgnoreRetainsState(t *testing.T) {
	m, fc, pub := newClockMock()
	putValue(t, m, fc, lazyNS, 10)
	requireNoError(t, m.CreateAlarm(context.Background(), lazyAlarm("keep", 60, "ignore")))

	// Past the evaluation range of N+2 periods, so every point is missing.
	fc.Advance(4 * time.Minute)

	assertEqual(t, stateAlarm, stateOf(t, m, "keep"))
	assertEqual(t, 0, pub.count(insuffTop))
	assertEqual(t, 1, len(historyOf(t, m, "keep")))
}

// "breaching" fills missing points as bad, so a new alarm with no data at all
// goes straight to ALARM.
func TestLazyEvalBreachingWithNoData(t *testing.T) {
	m, _, pub := newClockMock()

	requireNoError(t, m.CreateAlarm(context.Background(), lazyAlarm("bad", 60, "breaching")))

	assertEqual(t, stateAlarm, stateOf(t, m, "bad"))
	assertEqual(t, 1, pub.count(alarmTopic))
}

// "notBreaching" fills missing points as good, so a new alarm with no data
// goes to OK.
func TestLazyEvalNotBreachingWithNoData(t *testing.T) {
	m, _, _ := newClockMock()

	requireNoError(t, m.CreateAlarm(context.Background(), lazyAlarm("good", 60, "notBreaching")))

	assertEqual(t, stateOK, stateOf(t, m, "good"))
}

// SetAlarmState is temporary: the forced state holds for one evaluation
// interval and then goes back to the real state. SetAlarmState: "Metric alarms
// returns to their actual state quickly."
func TestLazyEvalSetAlarmStateReverts(t *testing.T) {
	ctx := context.Background()
	m, fc, pub := newClockMock()
	putValue(t, m, fc, lazyNS, 1)
	requireNoError(t, m.CreateAlarm(ctx, lazyAlarm("forced", 300, "")))
	assertEqual(t, stateOK, stateOf(t, m, "forced"))

	requireNoError(t, m.SetAlarmState(ctx, "forced", stateAlarm, "testing"))
	assertEqual(t, stateAlarm, stateOf(t, m, "forced"))
	assertEqual(t, 1, pub.count(alarmTopic))

	fc.Advance(30 * time.Second)
	assertEqual(t, stateAlarm, stateOf(t, m, "forced"))

	fc.Advance(30 * time.Second)
	assertEqual(t, stateOK, stateOf(t, m, "forced"))

	h := historyOf(t, m, "forced")
	// INSUFFICIENT_DATA->OK on create, then OK->ALARM forced, then ALARM->OK revert.
	assertEqual(t, 3, len(h))
	assertEqual(t, stateAlarm, h[0].OldState)
	assertEqual(t, stateOK, h[0].NewState)
	assertEqual(t, stateAlarm, h[1].NewState)
}

// AWS/DynamoDB alarms ignore missing data by default. An explicit policy
// overrides that. See alarms-and-missing-data.html: "You can override this if
// you choose a different option."
func TestLazyEvalDynamoDBDefaultIgnore(t *testing.T) {
	const ddb = "AWS/DynamoDB"

	ctx := context.Background()
	m, fc, _ := newClockMock()
	putValue(t, m, fc, ddb, 10)

	byDefault := lazyAlarm("ddb-default", 60, "")
	byDefault.Namespace = ddb
	explicit := lazyAlarm("ddb-missing", 60, "missing")
	explicit.Namespace = ddb

	requireNoError(t, m.CreateAlarm(ctx, byDefault))
	requireNoError(t, m.CreateAlarm(ctx, explicit))
	assertEqual(t, stateAlarm, stateOf(t, m, "ddb-default"))
	assertEqual(t, stateAlarm, stateOf(t, m, "ddb-missing"))

	// Past the evaluation range of N+2 periods, so every point is missing.
	fc.Advance(4 * time.Minute)

	assertEqual(t, stateAlarm, stateOf(t, m, "ddb-default"))
	assertEqual(t, stateInsufficientData, stateOf(t, m, "ddb-missing"))
}

// A 10-second alarm is evaluated every 10 seconds. Its data leaves the
// evaluation range after three periods, so a read 31 seconds later shows
// INSUFFICIENT_DATA.
func TestLazyEvalHighResolutionInterval(t *testing.T) {
	m, fc, _ := newClockMock()
	putValue(t, m, fc, lazyNS, 10)
	requireNoError(t, m.CreateAlarm(context.Background(), lazyAlarm("hires", 10, "")))
	assertEqual(t, stateAlarm, stateOf(t, m, "hires"))

	fc.Advance(21 * time.Second)
	assertEqual(t, stateAlarm, stateOf(t, m, "hires"))

	fc.Advance(10 * time.Second)
	assertEqual(t, stateInsufficientData, stateOf(t, m, "hires"))
}

// One breaching point followed by silence is the doc row "- - X - -" with
// Evaluation Periods and Datapoints to Alarm both 3. "alarms are designed to
// always go into ALARM state when the oldest available breaching datapoint
// during the Evaluation Periods number of data points is at least as old as
// the value of Datapoints to Alarm" (alarms-and-missing-data.html).
func TestLazyEvalPrematureAlarmAfterSilence(t *testing.T) {
	m, fc, pub := newClockMock()
	cfg := lazyAlarm("prem", 60, "")
	cfg.Stat, cfg.EvaluationPeriods, cfg.DatapointsToAlarm = "Maximum", 3, 3
	requireNoError(t, m.CreateAlarm(context.Background(), cfg))

	putValue(t, m, fc, lazyNS, 10)
	assertEqual(t, stateOK, stateOf(t, m, "prem"))

	fc.Advance(150 * time.Second)
	assertEqual(t, stateAlarm, stateOf(t, m, "prem"))
	assertEqual(t, 1, pub.count(alarmTopic))

	// Once the point leaves the evaluation range every point is missing.
	fc.Advance(3 * time.Minute)
	assertEqual(t, stateInsufficientData, stateOf(t, m, "prem"))
}

// A point older than Evaluation Periods but inside the evaluation range still
// counts: CloudWatch "evaluates the alarm state based on the most recent real
// data points that were successfully retrieved, including the necessary extra
// data points from farther back in the evaluation range."
func TestLazyEvalRangeReachesPastEvaluationPeriods(t *testing.T) {
	m, fc, _ := newClockMock()
	putValue(t, m, fc, lazyNS, 10)
	fc.Advance(90 * time.Second)

	requireNoError(t, m.CreateAlarm(context.Background(), lazyAlarm("reach", 60, "")))
	assertEqual(t, stateAlarm, stateOf(t, m, "reach"))
}
