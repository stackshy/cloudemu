package cloudwatch

import (
	"context"
	"testing"
	"time"
)

// configOf returns an event's configuration block, or previousConfiguration.
func configOf(t *testing.T, ev capturedEvent, key string) map[string]any {
	t.Helper()

	cfg, ok := ev.detail[key].(map[string]any)
	if !ok {
		t.Fatalf("event has no %s: %v", key, ev.detail)
	}

	return cfg
}

// requireConfigEvent checks the envelope and the documented configuration
// block of a metric alarm.
func requireConfigEvent(t *testing.T, ev capturedEvent, name, operation, state string) map[string]any {
	t.Helper()

	assertEqual(t, "aws.cloudwatch", ev.source)
	assertEqual(t, "CloudWatch Alarm Configuration Change", ev.detailType)
	assertEqual(t, 1, len(ev.resources))
	assertEqual(t, "arn:aws:cloudwatch:us-east-1:123456789012:alarm:"+name, ev.resources[0])
	assertEqual(t, name, ev.detail["alarmName"])
	assertEqual(t, operation, ev.detail["operation"])

	st, _ := ev.detail["state"].(map[string]any)
	assertEqual(t, state, st["value"])

	if _, err := time.Parse(reasonDataTimeFormat, st["timestamp"].(string)); err != nil {
		t.Fatalf("state.timestamp: %v", err)
	}

	cfg := configOf(t, ev, "configuration")
	assertEqual(t, name, cfg["alarmName"])
	assertEqual(t, "GreaterThanThreshold", cfg["comparisonOperator"])
	assertEqual(t, float64(1), cfg["evaluationPeriods"])
	assertEqual(t, true, cfg["actionsEnabled"])

	for _, k := range []string{"okActions", "alarmActions", "insufficientDataActions", "metrics"} {
		if _, ok := cfg[k].([]any); !ok {
			t.Fatalf("configuration.%s = %v, want a list", k, cfg[k])
		}
	}

	if _, err := time.Parse(reasonDataTimeFormat, cfg["timestamp"].(string)); err != nil {
		t.Fatalf("configuration.timestamp: %v", err)
	}

	return cfg
}

// PutMetricAlarm on a new alarm publishes operation create, before the first
// state change.
func TestConfigChangeEventOnCreate(t *testing.T) {
	m, bus, _ := newEventMock()

	cfg := lazyAlarm("cfg", 300, "ignore")
	cfg.AlarmDescription = "d1"
	requireNoError(t, m.CreateAlarm(context.Background(), cfg))

	events := bus.ofType(eventAlarmConfigChange)
	assertEqual(t, 1, len(events))

	body := requireConfigEvent(t, events[0], "cfg", "create", stateInsufficientData)
	assertEqual(t, float64(5), body["threshold"])
	assertEqual(t, "ignore", body["treatMissingData"])
	assertEqual(t, "d1", body["description"])
	assertEqual(t, alarmTopic, body["alarmActions"].([]any)[0])

	if _, ok := events[0].detail["previousConfiguration"]; ok {
		t.Fatal("a create has no previousConfiguration")
	}
}

// PutMetricAlarm on an existing alarm publishes operation update with the
// replaced configuration, and the current state.
func TestConfigChangeEventOnUpdate(t *testing.T) {
	m, bus, fc := newEventMock()
	ctx := context.Background()

	requireNoError(t, m.CreateAlarm(ctx, lazyAlarm("cfg", 300, "")))
	requireNoError(t, m.SetAlarmState(ctx, "cfg", stateAlarm, "x"))

	fc.Advance(time.Minute)

	next := lazyAlarm("cfg", 300, "")
	next.Threshold = 80
	requireNoError(t, m.CreateAlarm(ctx, next))

	events := bus.ofType(eventAlarmConfigChange)
	assertEqual(t, 2, len(events))

	body := requireConfigEvent(t, events[1], "cfg", "update", stateAlarm)
	prev := configOf(t, events[1], "previousConfiguration")

	assertEqual(t, float64(80), body["threshold"])
	assertEqual(t, float64(5), prev["threshold"])

	if body["timestamp"] == prev["timestamp"] {
		t.Fatalf("configuration timestamps should differ: %v", body["timestamp"])
	}

	// A new configuration gets a new query id, as in the AWS example.
	cur, _ := body["metrics"].([]any)[0].(map[string]any)
	old, _ := prev["metrics"].([]any)[0].(map[string]any)

	if cur["id"] == old["id"] {
		t.Fatal("update kept the old metric query id")
	}
}

// DeleteAlarms publishes operation delete with the final configuration.
func TestConfigChangeEventOnDelete(t *testing.T) {
	m, bus, _ := newEventMock()
	ctx := context.Background()

	requireNoError(t, m.CreateAlarm(ctx, lazyAlarm("cfg", 300, "")))
	requireNoError(t, m.DeleteAlarm(ctx, "cfg"))

	events := bus.ofType(eventAlarmConfigChange)
	assertEqual(t, 2, len(events))
	requireConfigEvent(t, events[1], "cfg", "delete", stateInsufficientData)

	// Deleting a missing alarm publishes nothing.
	assertError(t, m.DeleteAlarm(ctx, "cfg"), true)
	assertEqual(t, 2, len(bus.ofType(eventAlarmConfigChange)))
}

// An alarm with no TreatMissingData reports the AWS default in its event.
func TestConfigChangeEventDefaultsTreatMissingData(t *testing.T) {
	m, bus, _ := newEventMock()

	cfg := lazyAlarm("dflt", 300, "")
	requireNoError(t, m.CreateAlarm(context.Background(), cfg))

	events := bus.ofType(eventAlarmConfigChange)
	assertEqual(t, 1, len(events))

	body := requireConfigEvent(t, events[0], "dflt", "create", stateInsufficientData)
	assertEqual(t, "missing", body["treatMissingData"])
}
