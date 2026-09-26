package aws_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	sdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

const (
	cwAlarmPattern = `{"source":["aws.cloudwatch"],"detail-type":["CloudWatch Alarm State Change"]}`
	cwNamespace    = "Events/App"
	cwMetric       = "Errors"
)

// cwAlarm is a Sum > 5 alarm over one 60 second period.
func cwAlarm(name string) mondriver.AlarmConfig {
	return mondriver.AlarmConfig{
		Name: name, Namespace: cwNamespace, MetricName: cwMetric,
		ComparisonOperator: "GreaterThanThreshold", Threshold: 5,
		Period: 60, EvaluationPeriods: 1, Stat: "Sum",
	}
}

func cwPut(ctx context.Context, p *aws.Provider, fc *config.FakeClock, v float64) error {
	return p.CloudWatch.PutMetricData(ctx, []mondriver.MetricDatum{
		{Namespace: cwNamespace, MetricName: cwMetric, Value: v, Timestamp: fc.Now()},
	})
}

// TestCloudWatchAlarmStateChangeEvents pins that alarm transitions reach a
// default-bus rule with the real envelope, from SetAlarmState, from new data
// and from a lazy read.
func TestCloudWatchAlarmStateChangeEvents(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	p := aws.New(config.WithClock(fc))
	ctx := context.Background()
	drain := captureEvents(t, p, cwAlarmPattern)

	if err := p.CloudWatch.CreateAlarm(ctx, cwAlarm("errors")); err != nil {
		t.Fatalf("CreateAlarm: %v", err)
	}

	if err := p.CloudWatch.SetAlarmState(ctx, "errors", "ALARM", "e2e"); err != nil {
		t.Fatalf("SetAlarmState: %v", err)
	}

	if err := cwPut(ctx, p, fc, 1); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	fc.Advance(2 * time.Minute)

	if _, err := p.CloudWatch.DescribeAlarms(ctx, nil); err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}

	events := drain()

	want := [][2]string{{"INSUFFICIENT_DATA", "ALARM"}, {"ALARM", "OK"}, {"OK", "INSUFFICIENT_DATA"}}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d", len(events), len(want))
	}

	for i := range events {
		requireEnvelope(t, &events[i], "aws.cloudwatch", "CloudWatch Alarm State Change")

		if len(events[i].Resources) != 1 || events[i].Resources[0] != "arn:aws:cloudwatch:us-east-1:123456789012:alarm:errors" {
			t.Fatalf("resources = %v", events[i].Resources)
		}

		d := detailOf(t, &events[i])
		prev, _ := d["previousState"].(map[string]any)
		cur, _ := d["state"].(map[string]any)

		if prev["value"] != want[i][0] || cur["value"] != want[i][1] || d["alarmName"] != "errors" {
			t.Fatalf("event %d detail = %v, want %s -> %s", i, d, want[i][0], want[i][1])
		}
	}

	if cur, _ := detailOf(t, &events[0])["state"].(map[string]any); cur["reason"] != "e2e" {
		t.Fatalf("SetAlarmState reason = %v, want e2e", cur["reason"])
	}
}

// reentrantAlarmSetup wires a Lambda target on the alarm rule that runs act
// with the handler's ctx on every alarm event.
func reentrantAlarmSetup(t *testing.T, act func(ctx context.Context) error) (*aws.Provider, *config.FakeClock, *atomic.Int32) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	p := aws.New(config.WithClock(fc))
	ctx := context.Background()

	fn, err := p.Lambda.CreateFunction(ctx, sdriver.FunctionConfig{Name: "on-alarm"})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	calls := &atomic.Int32{}

	p.Lambda.RegisterHandler("on-alarm", func(hctx context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)

		return nil, act(hctx)
	})

	if _, err := p.EventBridge.PutRule(ctx, &ebdriver.RuleConfig{Name: "alarm", EventPattern: cwAlarmPattern}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := p.EventBridge.PutTargets(ctx, "", "alarm", []ebdriver.Target{{ID: "fn", ARN: fn.ARN}}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if err := p.CloudWatch.CreateAlarm(ctx, cwAlarm("loop")); err != nil {
		t.Fatalf("CreateAlarm: %v", err)
	}

	return p, fc, calls
}

// TestCloudWatchAlarmEventTargetSetsAlarmState pins that the event is sent
// with no CloudWatch lock held. The target flips the same alarm on every
// event, so a publish under alarmMu deadlocks and a lost hop depth recurses
// forever. The hop cap ends the chain.
func TestCloudWatchAlarmEventTargetSetsAlarmState(t *testing.T) {
	var p *aws.Provider

	flip := atomic.Bool{}

	p, _, calls := reentrantAlarmSetup(t, func(ctx context.Context) error {
		state := "OK"
		if flip.Load() {
			state = "ALARM"
		}

		flip.Store(!flip.Load())

		return p.CloudWatch.SetAlarmState(ctx, "loop", state, "handler")
	})

	runWithTimeout(t, "SetAlarmState", func() error {
		return p.CloudWatch.SetAlarmState(context.Background(), "loop", "ALARM", "test")
	})

	if calls.Load() == 0 {
		t.Fatal("the alarm event never reached the handler")
	}

	h, err := p.CloudWatch.GetAlarmHistory(context.Background(), "loop", 0)
	if err != nil {
		t.Fatalf("GetAlarmHistory: %v", err)
	}

	if len(h) < 2 {
		t.Fatalf("history has %d entries, want the handler's transition too", len(h))
	}
}

// TestCloudWatchAlarmEventTargetPutsMetricData pins the same for a target
// that writes data to the metric the alarm watches.
func TestCloudWatchAlarmEventTargetPutsMetricData(t *testing.T) {
	var (
		p  *aws.Provider
		fc *config.FakeClock
	)

	armed := atomic.Bool{}

	p, fc, calls := reentrantAlarmSetup(t, func(ctx context.Context) error {
		if !armed.CompareAndSwap(true, false) {
			return nil
		}

		return cwPut(ctx, p, fc, 1)
	})

	armed.Store(true)

	runWithTimeout(t, "PutMetricData", func() error { return cwPut(context.Background(), p, fc, 10) })

	if calls.Load() == 0 {
		t.Fatal("the alarm event never reached the handler")
	}

	alarms, err := p.CloudWatch.DescribeAlarms(context.Background(), []string{"loop"})
	if err != nil || len(alarms) != 1 {
		t.Fatalf("DescribeAlarms: %v", err)
	}

	// 10 + 1 in the same period is still above the threshold.
	if alarms[0].State != "ALARM" {
		t.Fatalf("state = %s, want ALARM", alarms[0].State)
	}
}
