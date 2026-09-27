package cloudwatch

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

func TestPutCompositeAlarmRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := driver.CompositeAlarmConfig{
		Name:             "app-unhealthy",
		AlarmRule:        `ALARM("cpu-high") OR ALARM("mem-high")`,
		AlarmDescription: "app is unhealthy",
		AlarmActions:     []string{"arn:aws:sns:us-east-1:123456789012:ops"},
		OKActions:        []string{"arn:aws:sns:us-east-1:123456789012:ok"},
	}

	putChildren(t, m, "cpu-high", "mem-high")

	t.Run("create then describe returns what was put", func(t *testing.T) {
		requireNoError(t, m.PutCompositeAlarm(ctx, cfg))

		out, err := m.DescribeCompositeAlarms(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 1, len(out))

		a := out[0]
		assertEqual(t, cfg.Name, a.Name)
		assertEqual(t, cfg.AlarmRule, a.AlarmRule)
		assertEqual(t, cfg.AlarmDescription, a.AlarmDescription)
		assertEqual(t, true, a.ActionsEnabled)
		assertEqual(t, 1, len(a.AlarmActions))
		assertEqual(t, cfg.AlarmActions[0], a.AlarmActions[0])
		assertEqual(t, 1, len(a.OKActions))
		// Evaluated at creation: no child is in ALARM, so the rule is false.
		assertEqual(t, stateOK, a.State)
		if a.ARN == "" {
			t.Fatal("expected a non-empty composite alarm ARN")
		}
	})

	t.Run("describe by name", func(t *testing.T) {
		out, err := m.DescribeCompositeAlarms(ctx, []string{"app-unhealthy"})
		requireNoError(t, err)
		assertEqual(t, 1, len(out))
		assertEqual(t, "app-unhealthy", out[0].Name)
	})

	t.Run("describe unknown name returns none", func(t *testing.T) {
		out, err := m.DescribeCompositeAlarms(ctx, []string{"nope"})
		requireNoError(t, err)
		assertEqual(t, 0, len(out))
	})

	t.Run("empty name is rejected", func(t *testing.T) {
		assertError(t, m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{AlarmRule: "ALARM(x)"}), true)
	})

	t.Run("empty rule is rejected", func(t *testing.T) {
		assertError(t, m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{Name: "x"}), true)
	})
}

func TestCompositeAlarmActionsEnabledFalse(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	putChildren(t, m, "x")

	disabled := false
	requireNoError(t, m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{
		Name:           "quiet",
		AlarmRule:      "ALARM(x)",
		ActionsEnabled: &disabled,
	}))

	out, err := m.DescribeCompositeAlarms(ctx, []string{"quiet"})
	requireNoError(t, err)
	assertEqual(t, 1, len(out))
	assertEqual(t, false, out[0].ActionsEnabled)
}

func TestDeleteCompositeAlarms(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	putChildren(t, m, "x", "y")

	requireNoError(t, m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{Name: "c1", AlarmRule: "ALARM(x)"}))
	requireNoError(t, m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{Name: "c2", AlarmRule: "ALARM(y)"}))

	// Deleting a mix of present and absent names is not an error (DeleteAlarms
	// accepts both metric and composite alarm names in one call).
	requireNoError(t, m.DeleteCompositeAlarms(ctx, []string{"c1", "absent"}))

	out, err := m.DescribeCompositeAlarms(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 1, len(out))
	assertEqual(t, "c2", out[0].Name)
}

func TestPutCompositeAlarmUpdatePreservesState(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	putChildren(t, m, "x", "y")

	requireNoError(t, m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{Name: "u", AlarmRule: "ALARM(x)"}))

	// Update the rule; a subsequent describe should reflect the new rule while
	// the alarm remains a single entry.
	requireNoError(t, m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{Name: "u", AlarmRule: "ALARM(y)"}))

	out, err := m.DescribeCompositeAlarms(ctx, []string{"u"})
	requireNoError(t, err)
	assertEqual(t, 1, len(out))
	assertEqual(t, "ALARM(y)", out[0].AlarmRule)
}

// putChildren creates metric alarms for composite rules to reference.
func putChildren(t *testing.T, m *Mock, names ...string) {
	t.Helper()

	for _, name := range names {
		requireNoError(t, m.CreateAlarm(context.Background(), driver.AlarmConfig{
			Name: name, Namespace: "Child/App", MetricName: name, ComparisonOperator: "GreaterThanThreshold",
			Threshold: 5, Period: 60, EvaluationPeriods: 1, Stat: "Maximum",
		}))
	}
}
