package cloudwatch

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const mathNS = "M/App"

func boolRef(b bool) *bool { return &b }

// rateAlarm watches err/req*100 > 20 over one 60s period.
func rateAlarm(name string) driver.AlarmConfig {
	return driver.AlarmConfig{
		Name: name, ComparisonOperator: "GreaterThanThreshold", Threshold: 20, EvaluationPeriods: 1,
		Metrics: []driver.MetricDataQuery{
			{ID: "err", ReturnData: boolRef(false), MetricStat: &driver.MetricStat{Namespace: mathNS, MetricName: "Errors", Period: 60, Stat: "Sum"}},
			{ID: "req", ReturnData: boolRef(false), MetricStat: &driver.MetricStat{Namespace: mathNS, MetricName: "Requests", Period: 60, Stat: "Sum"}},
			{ID: "rate", Expression: "err/req*100", Label: "ErrorRate", ReturnData: boolRef(true)},
		},
	}
}

func putMath(t *testing.T, m *Mock, fc *config.FakeClock, name string, v float64) {
	t.Helper()

	requireNoError(t, m.PutMetricData(context.Background(), []driver.MetricDatum{
		{Namespace: mathNS, MetricName: name, Value: v, Timestamp: fc.Now()},
	}))
}

// A math alarm evaluates its expression: 30 errors over 100 requests is a 30%
// rate, above the 20 threshold. Before CW-12a it stayed INSUFFICIENT_DATA.
func TestMathAlarmEvaluatesExpression(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	putMath(t, m, fc, "Errors", 30)
	putMath(t, m, fc, "Requests", 100)
	fc.Advance(time.Second)

	requireNoError(t, m.CreateAlarm(ctx, rateAlarm("rate")))
	assertEqual(t, stateAlarm, stateOf(t, m, "rate"))

	alarms, err := m.DescribeAlarms(ctx, []string{"rate"})
	requireNoError(t, err)
	assertEqual(t, 3, len(alarms[0].Metrics))
	assertEqual(t, "err/req*100", alarms[0].Metrics[2].Expression)
	assertEqual(t, 0, alarms[0].Period)
}

// New data on an input metric re-evaluates the math alarm right away.
func TestMathAlarmPutMetricDataReevaluates(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	putMath(t, m, fc, "Requests", 100)
	putMath(t, m, fc, "Errors", 5)
	fc.Advance(time.Second)

	requireNoError(t, m.CreateAlarm(ctx, rateAlarm("rate")))
	assertEqual(t, stateOK, stateOf(t, m, "rate"))

	// Still inside the evaluation interval, so only the PutMetricData trigger
	// can move the alarm.
	putMath(t, m, fc, "Errors", 40)
	fc.Advance(time.Second)

	m.alarmMu.Lock()
	a, _ := m.alarms.Get("rate")
	state := a.State
	m.alarmMu.Unlock()

	assertEqual(t, stateAlarm, state)
}

// An expression outside the supported syntax evaluates to no data.
func TestMathAlarmUnsupportedExpression(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	putMath(t, m, fc, "Errors", 30)

	cfg := rateAlarm("fill")
	cfg.Metrics[2].Expression = "FILL(err, 0)"

	requireNoError(t, m.CreateAlarm(ctx, cfg))
	assertEqual(t, stateInsufficientData, stateOf(t, m, "fill"))
}

// The state change event lists the math alarm's queries.
func TestMathAlarmEventMetrics(t *testing.T) {
	m, bus, fc := newEventMock()
	ctx := context.Background()

	putMath(t, m, fc, "Errors", 30)
	putMath(t, m, fc, "Requests", 100)
	fc.Advance(time.Second)

	requireNoError(t, m.CreateAlarm(ctx, rateAlarm("rate")))

	events := bus.ofType(eventAlarmStateChange)
	if len(events) != 1 {
		t.Fatalf("state events = %d, want 1", len(events))
	}

	cfg, _ := events[0].detail["configuration"].(map[string]any)
	metrics, _ := cfg["metrics"].([]any)
	assertEqual(t, 3, len(metrics))

	rate, _ := metrics[2].(map[string]any)
	assertEqual(t, "err/req*100", rate["expression"])
	assertEqual(t, true, rate["returnData"])

	if _, ok := rate["metricStat"]; ok {
		t.Fatalf("expression entry has a metricStat: %v", rate)
	}

	errQ, _ := metrics[0].(map[string]any)
	assertEqual(t, false, errQ["returnData"])
}

// The stored query list is a copy, so a caller changing its config later
// does not change the alarm.
func TestMathAlarmStoresCopy(t *testing.T) {
	m, _, _ := newClockMock()
	ctx := context.Background()

	cfg := rateAlarm("rate")
	requireNoError(t, m.CreateAlarm(ctx, cfg))

	cfg.Metrics[2].Expression = "changed"
	*cfg.Metrics[0].ReturnData = true

	alarms, err := m.DescribeAlarms(ctx, []string{"rate"})
	requireNoError(t, err)
	assertEqual(t, "err/req*100", alarms[0].Metrics[2].Expression)
	assertEqual(t, false, *alarms[0].Metrics[0].ReturnData)
}

// messagePublisher keeps every SNS message body.
type messagePublisher struct {
	mu       sync.Mutex
	messages []string
}

func (p *messagePublisher) PublishExternal(_ context.Context, _, message string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.messages = append(p.messages, message)

	return nil
}

// The SNS notification of a math alarm lists its Metrics in the Trigger,
// with no single MetricName. Before, the Trigger had an empty MetricName and
// no Metrics.
func TestMathAlarmNotificationTrigger(t *testing.T) {
	m, fc, _ := newClockMock()
	pub := &messagePublisher{}
	m.SetSNSPublisher(pub)

	ctx := context.Background()

	requireNoError(t, m.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace: mathNS, MetricName: "Errors", Value: 30, Timestamp: fc.Now(), Dimensions: map[string]string{"Service": "api"},
	}}))
	putMath(t, m, fc, "Requests", 100)
	fc.Advance(time.Second)

	cfg := rateAlarm("rate")
	cfg.AlarmActions = []string{alarmTopic}
	cfg.Metrics[0].MetricStat.Dimensions = map[string]string{"Service": "api"}
	requireNoError(t, m.CreateAlarm(ctx, cfg))

	if len(pub.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(pub.messages))
	}

	var body struct {
		Trigger struct {
			MetricName       *string `json:"MetricName"`
			Period           int     `json:"Period"`
			TreatMissingData string  `json:"TreatMissingData"`
			Metrics          []struct {
				ID         string `json:"Id"`
				Expression string `json:"Expression"`
				Label      string `json:"Label"`
				ReturnData bool   `json:"ReturnData"`
				MetricStat *struct {
					Metric struct {
						Dimensions []map[string]string `json:"Dimensions"`
						MetricName string              `json:"MetricName"`
						Namespace  string              `json:"Namespace"`
					} `json:"Metric"`
					Period int    `json:"Period"`
					Stat   string `json:"Stat"`
				} `json:"MetricStat"`
			} `json:"Metrics"`
		} `json:"Trigger"`
	}

	requireNoError(t, json.Unmarshal([]byte(pub.messages[0]), &body))

	tr := body.Trigger
	if tr.MetricName != nil {
		t.Fatalf("math Trigger has MetricName %q", *tr.MetricName)
	}

	assertEqual(t, 60, tr.Period)
	assertEqual(t, "- TreatMissingData:"+strings.Repeat(" ", 20)+"missing", tr.TreatMissingData)
	assertEqual(t, 3, len(tr.Metrics))
	assertEqual(t, "err/req*100", tr.Metrics[2].Expression)
	assertEqual(t, "ErrorRate", tr.Metrics[2].Label)
	assertEqual(t, true, tr.Metrics[2].ReturnData)

	errQ := tr.Metrics[0]
	if errQ.MetricStat == nil {
		t.Fatalf("err entry has no MetricStat")
	}

	assertEqual(t, false, errQ.ReturnData)
	assertEqual(t, "Errors", errQ.MetricStat.Metric.MetricName)
	assertEqual(t, "Sum", errQ.MetricStat.Stat)
	assertEqual(t, "api", errQ.MetricStat.Metric.Dimensions[0]["value"])
	assertEqual(t, "Service", errQ.MetricStat.Metric.Dimensions[0]["name"])
}

// The single-metric Trigger matches the aws-lambda-go sample payload
// cloudwatch-alarm-sns-payload-single-metric.json.
func TestNotificationTriggerSingleMetric(t *testing.T) {
	m, fc, _ := newClockMock()
	pub := &messagePublisher{}
	m.SetSNSPublisher(pub)

	ctx := context.Background()
	dims := map[string]string{"InstanceId": "TestInstance"}

	requireNoError(t, m.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace: "AWS/EC2", MetricName: "NetworkOut", Value: 1234, Unit: "Bytes", Timestamp: fc.Now(), Dimensions: dims,
	}}))
	fc.Advance(time.Second)

	requireNoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{
		Name: "net", Namespace: "AWS/EC2", MetricName: "NetworkOut", Dimensions: dims, Unit: "Bytes",
		Stat: "Average", Period: 60, EvaluationPeriods: 1, Threshold: 0, ComparisonOperator: "GreaterThanThreshold",
		AlarmActions: []string{alarmTopic},
	}))

	if len(pub.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(pub.messages))
	}

	var body struct {
		Trigger map[string]any `json:"Trigger"`
	}

	requireNoError(t, json.Unmarshal([]byte(pub.messages[0]), &body))

	want := map[string]any{
		"MetricName":                       "NetworkOut",
		"Namespace":                        "AWS/EC2",
		"StatisticType":                    "Statistic",
		"Statistic":                        "AVERAGE",
		"Unit":                             "Bytes",
		"Dimensions":                       []any{map[string]any{"value": "TestInstance", "name": "InstanceId"}},
		"Period":                           float64(60),
		"EvaluationPeriods":                float64(1),
		"ComparisonOperator":               "GreaterThanThreshold",
		"Threshold":                        float64(0),
		"TreatMissingData":                 "- TreatMissingData:" + strings.Repeat(" ", 20) + "missing",
		"EvaluateLowSampleCountPercentile": "",
	}

	if !reflect.DeepEqual(body.Trigger, want) {
		t.Fatalf("Trigger =\n%v\nwant\n%v", body.Trigger, want)
	}
}

// A snapshot keeps the Metrics list and ThresholdMetricID.
func TestMathAlarmSnapshot(t *testing.T) {
	ctx := context.Background()
	src, _, _ := newClockMock()

	cfg := rateAlarm("rate")
	cfg.ThresholdMetricID = "err"
	requireNoError(t, src.CreateAlarm(ctx, cfg))

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst, _, _ := newClockMock()
	requireNoError(t, dst.Restore(ctx, data))

	alarms, err := dst.DescribeAlarms(ctx, []string{"rate"})
	requireNoError(t, err)
	assertEqual(t, 3, len(alarms[0].Metrics))
	assertEqual(t, "err", alarms[0].ThresholdMetricID)
	assertEqual(t, "Requests", alarms[0].Metrics[1].MetricStat.MetricName)
}

// Math alarms survive concurrent writes and reads. Run with -race.
func TestMathAlarmConcurrency(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	var wg sync.WaitGroup

	for i := range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			name := "rate"
			if i%2 == 0 {
				name = "rate2"
			}

			for range 20 {
				_ = m.PutMetricData(ctx, []driver.MetricDatum{{Namespace: mathNS, MetricName: "Errors", Value: 1, Timestamp: fc.Now()}})
				_ = m.CreateAlarm(ctx, rateAlarm(name))
				_, _ = m.DescribeAlarms(ctx, nil)
				_ = m.DeleteAlarm(ctx, name)
			}
		}()
	}

	wg.Wait()
}
