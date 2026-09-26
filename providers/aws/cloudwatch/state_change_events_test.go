package cloudwatch

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// capturedEvent is one event handed to the fake bus.
type capturedEvent struct {
	source, detailType string
	detail             map[string]any
	resources          []string
}

// capturingBus records every published event with its detail as JSON.
type capturingBus struct {
	mu     sync.Mutex
	events []capturedEvent
}

func (b *capturingBus) PublishServiceEvent(_ context.Context, source, detailType string, detail any, resources []string) {
	raw, err := json.Marshal(detail)
	if err != nil {
		panic(err)
	}

	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		panic(err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.events = append(b.events, capturedEvent{source: source, detailType: detailType, detail: d, resources: resources})
}

// all returns the state change events.
func (b *capturingBus) all() []capturedEvent {
	return b.ofType(eventAlarmStateChange)
}

func (b *capturingBus) ofType(detailType string) []capturedEvent {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out []capturedEvent

	for _, ev := range b.events {
		if ev.detailType == detailType {
			out = append(out, ev)
		}
	}

	return out
}

func newEventMock() (*Mock, *capturingBus, *config.FakeClock) {
	m, fc, _ := newClockMock()
	bus := &capturingBus{}
	m.SetEventPublisher(bus)

	return m, bus, fc
}

// requireStateEvent checks the documented envelope and detail shape.
func requireStateEvent(t *testing.T, ev capturedEvent, name, prev, cur string) {
	t.Helper()

	assertEqual(t, "aws.cloudwatch", ev.source)
	assertEqual(t, "CloudWatch Alarm State Change", ev.detailType)
	assertEqual(t, 1, len(ev.resources))
	assertEqual(t, "arn:aws:cloudwatch:us-east-1:123456789012:alarm:"+name, ev.resources[0])
	assertEqual(t, name, ev.detail["alarmName"])

	state, _ := ev.detail["state"].(map[string]any)
	previous, _ := ev.detail["previousState"].(map[string]any)

	assertEqual(t, cur, state["value"])
	assertEqual(t, prev, previous["value"])

	for _, s := range []map[string]any{state, previous} {
		ts, _ := s["timestamp"].(string)
		if _, err := time.Parse(reasonDataTimeFormat, ts); err != nil {
			t.Fatalf("timestamp %q is not in the CloudWatch format: %v", ts, err)
		}

		if s["reason"] == "" || s["reason"] == nil {
			t.Fatalf("state has no reason: %v", s)
		}
	}
}

// metricOf returns configuration.metrics[0] of an event.
func metricOf(t *testing.T, ev capturedEvent) map[string]any {
	t.Helper()

	cfg, _ := ev.detail["configuration"].(map[string]any)
	metrics, _ := cfg["metrics"].([]any)

	if len(metrics) != 1 {
		t.Fatalf("configuration.metrics = %v, want one query", cfg["metrics"])
	}

	q, _ := metrics[0].(map[string]any)

	return q
}

// A metric-driven transition publishes one event with the full documented
// detail, including configuration.metrics.
func TestStateChangeEventOnPutMetricData(t *testing.T) {
	m, bus, fc := newEventMock()
	ctx := context.Background()

	cfg := lazyAlarm("cpu", 60, "")
	cfg.AlarmDescription = "too many errors"
	cfg.Dimensions = map[string]string{"InstanceId": "i-1"}
	requireNoError(t, m.CreateAlarm(ctx, cfg))
	assertEqual(t, 0, len(bus.all()))

	requireNoError(t, m.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace: lazyNS, MetricName: lazyMetric, Value: 10, Dimensions: map[string]string{"InstanceId": "i-1"},
		Timestamp: m.opts.Clock.Now(),
	}}))

	events := bus.all()
	assertEqual(t, 1, len(events))
	requireStateEvent(t, events[0], "cpu", stateInsufficientData, stateAlarm)

	state, _ := events[0].detail["state"].(map[string]any)
	previous, _ := events[0].detail["previousState"].(map[string]any)

	if rd, _ := state["reasonData"].(string); !json.Valid([]byte(rd)) || rd == "" {
		t.Fatalf("state.reasonData = %q, want the evaluation JSON", rd)
	}

	assertEqual(t, initialStateReason, previous["reason"])

	if _, ok := previous["reasonData"]; ok {
		t.Fatalf("previousState.reasonData should be absent for a new alarm: %v", previous)
	}

	cfgOut, _ := events[0].detail["configuration"].(map[string]any)
	assertEqual(t, "too many errors", cfgOut["description"])

	q := metricOf(t, events[0])
	assertEqual(t, true, q["returnData"])

	if id, _ := q["id"].(string); id == "" {
		t.Fatal("metric query has no id")
	}

	stat, _ := q["metricStat"].(map[string]any)
	metric, _ := stat["metric"].(map[string]any)
	dims, _ := metric["dimensions"].(map[string]any)

	assertEqual(t, float64(60), stat["period"])
	assertEqual(t, "Sum", stat["stat"])
	assertEqual(t, lazyNS, metric["namespace"])
	assertEqual(t, lazyMetric, metric["name"])
	assertEqual(t, "i-1", dims["InstanceId"])

	// Same state again: no new event.
	putValue(t, m, fc, lazyNS, 20)
	assertEqual(t, 1, len(bus.all()))
}

// SetAlarmState publishes an event, and a call that keeps the state does not.
func TestStateChangeEventOnSetAlarmState(t *testing.T) {
	m, bus, _ := newEventMock()
	ctx := context.Background()

	requireNoError(t, m.CreateAlarm(ctx, lazyAlarm("manual", 300, "")))
	requireNoError(t, m.SetAlarmStateWithData(ctx, "manual", stateAlarm, "testing", `{"k":"v"}`))

	events := bus.all()
	assertEqual(t, 1, len(events))
	requireStateEvent(t, events[0], "manual", stateInsufficientData, stateAlarm)

	state, _ := events[0].detail["state"].(map[string]any)
	assertEqual(t, "testing", state["reason"])
	assertEqual(t, `{"k":"v"}`, state["reasonData"])

	requireNoError(t, m.SetAlarmState(ctx, "manual", stateAlarm, "again"))
	assertEqual(t, 1, len(bus.all()))
}

// A transition found by a lazy read (no new data) publishes an event.
func TestStateChangeEventOnLazyRead(t *testing.T) {
	m, bus, fc := newEventMock()
	ctx := context.Background()

	requireNoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: lazyNS, MetricName: lazyMetric, Value: 10, Timestamp: m.opts.Clock.Now()},
	}))
	requireNoError(t, m.CreateAlarm(ctx, lazyAlarm("stale", 60, "")))
	assertEqual(t, 1, len(bus.all()))

	fc.Advance(2 * time.Minute)

	if _, err := m.DescribeAlarms(ctx, []string{"stale"}); err != nil {
		t.Fatal(err)
	}

	events := bus.all()
	assertEqual(t, 2, len(events))
	requireStateEvent(t, events[1], "stale", stateAlarm, stateInsufficientData)

	// The previous state is the ALARM transition with its own timestamp.
	previous, _ := events[1].detail["previousState"].(map[string]any)
	first, _ := events[0].detail["state"].(map[string]any)
	assertEqual(t, first["timestamp"], previous["timestamp"])
	assertEqual(t, first["reason"], previous["reason"])
}

// The event is sent even with actions disabled. SNS actions are not.
func TestStateChangeEventIgnoresActionsEnabled(t *testing.T) {
	m, fc, pub := newClockMock()
	bus := &capturingBus{}
	m.SetEventPublisher(bus)

	off := false
	cfg := lazyAlarm("quiet", 60, "")
	cfg.ActionsEnabled = &off
	requireNoError(t, m.CreateAlarm(context.Background(), cfg))
	putValue(t, m, fc, lazyNS, 10)

	assertEqual(t, 1, len(bus.all()))
	assertEqual(t, 0, pub.count(alarmTopic))
}

// ExtendedStatistic is reported as the query's stat.
func TestStateChangeEventExtendedStatistic(t *testing.T) {
	m, bus, _ := newEventMock()
	ctx := context.Background()

	cfg := lazyAlarm("p99", 60, "")
	cfg.Stat = ""
	cfg.ExtendedStatistic = "p99"
	requireNoError(t, m.CreateAlarm(ctx, cfg))
	requireNoError(t, m.SetAlarmState(ctx, "p99", stateOK, "x"))

	stat, _ := metricOf(t, bus.all()[0])["metricStat"].(map[string]any)
	assertEqual(t, "p99", stat["stat"])
}

// The metric query id is stable across events of one configuration.
func TestStateChangeEventQueryIDStable(t *testing.T) {
	m, bus, _ := newEventMock()
	ctx := context.Background()

	requireNoError(t, m.CreateAlarm(ctx, lazyAlarm("ids", 300, "")))
	requireNoError(t, m.SetAlarmState(ctx, "ids", stateAlarm, "a"))
	requireNoError(t, m.SetAlarmState(ctx, "ids", stateOK, "b"))

	events := bus.all()
	assertEqual(t, 2, len(events))
	assertEqual(t, metricOf(t, events[0])["id"], metricOf(t, events[1])["id"])
}

// An unwired mock transitions without publishing anything.
func TestStateChangeEventUnwired(t *testing.T) {
	m, _, _ := newClockMock()
	requireNoError(t, m.CreateAlarm(context.Background(), lazyAlarm("solo", 300, "")))
	requireNoError(t, m.SetAlarmState(context.Background(), "solo", stateAlarm, "x"))
}
