package cloudwatch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	compositeNS    = "Comp/App"
	compositeTopic = "arn:aws:sns:us-east-1:123456789012:composite"
)

// childAlarm is a Maximum > 5 alarm on compositeNS/name. Missing data is
// ignored so a forced or breached state holds while the clock moves.
func childAlarm(name string) driver.AlarmConfig {
	return driver.AlarmConfig{
		Name: name, Namespace: compositeNS, MetricName: name, ComparisonOperator: "GreaterThanThreshold",
		Threshold: 5, Period: 60, EvaluationPeriods: 1, Stat: "Maximum", TreatMissingData: "ignore",
	}
}

func breach(t *testing.T, m *Mock, fc *config.FakeClock, metric string) {
	t.Helper()

	requireNoError(t, m.PutMetricData(context.Background(), []driver.MetricDatum{
		{Namespace: compositeNS, MetricName: metric, Value: 9, Timestamp: fc.Now()},
	}))
}

func composite(t *testing.T, m *Mock, name string) driver.CompositeAlarmInfo {
	t.Helper()

	out, err := m.DescribeCompositeAlarms(context.Background(), []string{name})
	requireNoError(t, err)

	if len(out) != 1 {
		t.Fatalf("want composite %q, got %d", name, len(out))
	}

	return out[0]
}

func putComposite(t *testing.T, m *Mock, name, rule string) {
	t.Helper()

	requireNoError(t, m.PutCompositeAlarm(context.Background(), driver.CompositeAlarmConfig{
		Name: name, AlarmRule: rule, AlarmActions: []string{compositeTopic}, OKActions: []string{okTopic},
	}))
}

func intp(v int) *int { return &v }

// Before, the composite stayed INSUFFICIENT_DATA forever and fired nothing.
func TestCompositeFollowsChildren(t *testing.T) {
	ctx := context.Background()
	m, fc, pub := newClockMock()
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("a")))
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("b")))

	putComposite(t, m, "both", "ALARM(a) AND ALARM(b)")
	assertEqual(t, stateOK, composite(t, m, "both").State)

	breach(t, m, fc, "a")
	assertEqual(t, stateAlarm, stateOf(t, m, "a"))
	assertEqual(t, stateOK, composite(t, m, "both").State)
	assertEqual(t, 0, pub.count(compositeTopic))

	breach(t, m, fc, "b")

	c := composite(t, m, "both")
	assertEqual(t, stateAlarm, c.State)
	assertEqual(t, 1, pub.count(compositeTopic))

	if !strings.HasPrefix(c.StateReason, "arn:aws:cloudwatch:us-east-1:123456789012:alarm:b transitioned to ALARM at ") {
		t.Errorf("reason = %q", c.StateReason)
	}

	if !strings.Contains(c.StateReasonData, `"triggeringAlarms":[{"arn":"arn:aws:cloudwatch:us-east-1:123456789012:alarm:b"`) {
		t.Errorf("reasonData = %q", c.StateReasonData)
	}

	h := historyOf(t, m, "both")
	assertEqual(t, 2, len(h))
	assertEqual(t, stateAlarm, h[0].NewState)
}

func TestCompositeOfComposites(t *testing.T) {
	ctx := context.Background()
	m, fc, _ := newClockMock()
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("a")))

	putComposite(t, m, "inner", "ALARM(a)")
	putComposite(t, m, "outer", `ALARM("arn:aws:cloudwatch:us-east-1:123456789012:alarm:inner") AND TRUE`)
	assertEqual(t, stateOK, composite(t, m, "outer").State)

	breach(t, m, fc, "a")
	assertEqual(t, stateAlarm, composite(t, m, "inner").State)
	assertEqual(t, stateAlarm, composite(t, m, "outer").State)
}

// AWS stops evaluating composites on a cycle and refuses to delete them.
func TestCompositeCycle(t *testing.T) {
	ctx := context.Background()
	m, _, _ := newClockMock()

	putComposite(t, m, "c1", "TRUE")
	putComposite(t, m, "c2", "ALARM(c1)")
	assertEqual(t, stateAlarm, composite(t, m, "c2").State)

	done := make(chan struct{})

	go func() {
		defer close(done)

		putComposite(t, m, "c1", "NOT ALARM(c2)")
		requireNoError(t, m.SetAlarmState(ctx, "c1", stateOK, "forced"))
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cycle evaluation did not finish")
	}

	// c1 is on a cycle: its state is kept, so c2 keeps its state too.
	assertEqual(t, stateOK, composite(t, m, "c1").State)
	assertEqual(t, stateAlarm, composite(t, m, "c2").State)

	for _, name := range []string{"c1", "c2"} {
		err := m.DeleteAlarms(ctx, []string{name})
		if !errors.IsFailedPrecondition(err) {
			t.Fatalf("delete %s: want FailedPrecondition, got %v", name, err)
		}
	}
}

func TestPutCompositeValidation(t *testing.T) {
	ctx := context.Background()
	m, _, _ := newClockMock()
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("a")))

	cases := []struct {
		name string
		cfg  driver.CompositeAlarmConfig
		code errors.Code
		msg  string
	}{
		{"missing child", driver.CompositeAlarmConfig{Name: "x", AlarmRule: "ALARM(a) OR ALARM(gone)"},
			errors.FailedPrecondition, "Could not save the composite alarm as alarms [gone] in the alarm rule do not exist"},
		{"leading space", driver.CompositeAlarmConfig{Name: "x", AlarmRule: " ALARM(a)"},
			errors.InvalidArgument, "AlarmRule must not contain leading or trailing whitespace or be null"},
		{"bad syntax", driver.CompositeAlarmConfig{Name: "x", AlarmRule: "ALARM(a) AND"}, errors.InvalidArgument, "invalid AlarmRule: unexpected end"},
		{"suppressor without periods", driver.CompositeAlarmConfig{Name: "x", AlarmRule: "ALARM(a)", ActionsSuppressor: "a"},
			errors.InvalidArgument, "are required when ActionsSuppressor is set"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := m.PutCompositeAlarm(ctx, tc.cfg)
			if errors.GetCode(err) != tc.code || !strings.Contains(errors.Message(err), tc.msg) {
				t.Fatalf("got %v, want %v containing %q", err, tc.code, tc.msg)
			}

			out, _ := m.DescribeCompositeAlarms(ctx, []string{"x"})
			assertEqual(t, 0, len(out))
		})
	}
}

// suppressed builds a composite on "a" whose actions are held by "sup".
func suppressed(t *testing.T, m *Mock, wait, extension int) {
	t.Helper()

	ctx := context.Background()
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("a")))
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("sup")))
	requireNoError(t, m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{
		Name: "svc", AlarmRule: "ALARM(a)", AlarmActions: []string{compositeTopic},
		ActionsSuppressor: "sup", ActionsSuppressorWaitPeriod: intp(wait), ActionsSuppressorExtensionPeriod: intp(extension),
	}))
}

// The composite enters ALARM with the suppressor OK: its actions wait, then
// fire when the WaitPeriod ends.
func TestSuppressorWaitPeriodExpires(t *testing.T) {
	ctx := context.Background()
	m, fc, pub := newClockMock()
	suppressed(t, m, 120, 180)

	requireNoError(t, m.SetAlarmState(ctx, "a", stateAlarm, "breach"))

	c := composite(t, m, "svc")
	assertEqual(t, stateAlarm, c.State)
	assertEqual(t, "WaitPeriod", c.ActionsSuppressedBy)
	assertEqual(t, "Actions suppressed by WaitPeriod", c.ActionsSuppressedReason)
	assertEqual(t, 0, pub.count(compositeTopic))

	fc.Advance(119 * time.Second)
	assertEqual(t, 0, pub.count(compositeTopic))
	assertEqual(t, "WaitPeriod", composite(t, m, "svc").ActionsSuppressedBy)

	fc.Advance(time.Second)
	assertEqual(t, "", composite(t, m, "svc").ActionsSuppressedBy)
	assertEqual(t, 1, pub.count(compositeTopic))

	// Nothing fires twice.
	fc.Advance(time.Hour)
	_ = composite(t, m, "svc")
	assertEqual(t, 1, pub.count(compositeTopic))
}

// The suppressor enters ALARM during the WaitPeriod: the actions stay held
// past the end of the wait, then fire when the ExtensionPeriod after the
// suppressor leaves ALARM ends.
func TestSuppressorAlarmThenExtension(t *testing.T) {
	ctx := context.Background()
	m, fc, pub := newClockMock()
	suppressed(t, m, 60, 180)

	requireNoError(t, m.SetAlarmState(ctx, "a", stateAlarm, "breach"))
	requireNoError(t, m.SetAlarmState(ctx, "sup", stateAlarm, "maintenance"))
	assertEqual(t, "Alarm", composite(t, m, "svc").ActionsSuppressedBy)

	fc.Advance(10 * time.Minute)
	assertEqual(t, "Alarm", composite(t, m, "svc").ActionsSuppressedBy)
	assertEqual(t, 0, pub.count(compositeTopic))

	requireNoError(t, m.SetAlarmState(ctx, "sup", stateOK, "done"))
	assertEqual(t, "ExtensionPeriod", composite(t, m, "svc").ActionsSuppressedBy)

	fc.Advance(179 * time.Second)
	assertEqual(t, 0, pub.count(compositeTopic))

	fc.Advance(time.Second)
	assertEqual(t, "", composite(t, m, "svc").ActionsSuppressedBy)
	assertEqual(t, 1, pub.count(compositeTopic))
}

// With the suppressor already in ALARM the composite's actions are held by
// Alarm from the start.
func TestSuppressorAlreadyInAlarm(t *testing.T) {
	ctx := context.Background()
	m, _, pub := newClockMock()
	suppressed(t, m, 60, 60)

	requireNoError(t, m.SetAlarmState(ctx, "sup", stateAlarm, "maintenance"))
	requireNoError(t, m.SetAlarmState(ctx, "a", stateAlarm, "breach"))

	c := composite(t, m, "svc")
	assertEqual(t, stateAlarm, c.State)
	assertEqual(t, "Alarm", c.ActionsSuppressedBy)
	assertEqual(t, 0, pub.count(compositeTopic))
}

// Before, DeleteAlarms deleted a referenced child and any number of composites.
func TestDeleteAlarmsGuards(t *testing.T) {
	ctx := context.Background()
	m, _, _ := newClockMock()
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("a")))
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("b")))
	putComposite(t, m, "p1", "ALARM(a)")
	putComposite(t, m, "p2", "ALARM(a) OR ALARM(b)")

	err := m.DeleteAlarms(ctx, []string{"a", "b"})
	assertEqual(t, "Cannot delete a,b as there are composite alarm(s) depending on them.", errors.Message(err))
	assertEqual(t, 2, len(mustAlarms(t, m)))

	err = m.DeleteAlarm(ctx, "b")
	assertEqual(t, "Cannot delete b as there are composite alarm(s) depending on it.", errors.Message(err))

	err = m.DeleteAlarms(ctx, []string{"p1", "p2"})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("two composites: want InvalidArgument, got %v", err)
	}

	assertEqual(t, 2, len(mustComposites(t, m)))

	// A composite and the child only it references go in one call.
	requireNoError(t, m.DeleteAlarms(ctx, []string{"b", "p2", "missing"}))
	assertEqual(t, 1, len(mustAlarms(t, m)))
	assertEqual(t, 1, len(mustComposites(t, m)))
}

func mustAlarms(t *testing.T, m *Mock) []driver.AlarmInfo {
	t.Helper()

	out, err := m.DescribeAlarms(context.Background(), nil)
	requireNoError(t, err)

	return out
}

func mustComposites(t *testing.T, m *Mock) []driver.CompositeAlarmInfo {
	t.Helper()

	out, err := m.DescribeCompositeAlarms(context.Background(), nil)
	requireNoError(t, err)

	return out
}

// Composites send state change and configuration change events (CW-X5,
// CW-X6). Before, they sent none.
func TestCompositeEvents(t *testing.T) {
	ctx := context.Background()
	m, bus, fc := newEventMock()
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("a")))

	requireNoError(t, m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{
		Name: "svc", AlarmRule: "ALARM(a)", AlarmDescription: "d",
		ActionsSuppressor: "a", ActionsSuppressorWaitPeriod: intp(120), ActionsSuppressorExtensionPeriod: intp(180),
	}))

	configs := bus.ofType(eventAlarmConfigChange)
	create := configs[len(configs)-1]
	assertEqual(t, "create", create.detail["operation"])
	assertEqual(t, stateInsufficientData, create.detail["state"].(map[string]any)["value"])
	cfg := create.detail["configuration"].(map[string]any)
	assertEqual(t, "ALARM(a)", cfg["alarmRule"])
	assertEqual(t, "a", cfg["actionsSuppressor"])
	assertEqual(t, float64(120), cfg["actionsSuppressorWaitPeriod"])
	assertEqual(t, float64(180), cfg["actionsSuppressorExtensionPeriod"])

	initial := bus.all()
	last := initial[len(initial)-1]
	requireStateEventShape(t, last, "svc", stateInsufficientData, stateOK)

	breach(t, m, fc, "a")

	states := bus.all()
	ev := states[len(states)-1]
	requireStateEventShape(t, ev, "svc", stateOK, stateAlarm)

	// The suppressor is in ALARM, so the actions are held by Alarm.
	assertEqual(t, "Alarm", ev.detail["state"].(map[string]any)["actionsSuppressedBy"])

	requireNoError(t, m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{Name: "svc", AlarmRule: "ALARM(a) OR FALSE"}))
	configs = bus.ofType(eventAlarmConfigChange)
	update := configs[len(configs)-1]
	assertEqual(t, "update", update.detail["operation"])
	assertEqual(t, "ALARM(a)", update.detail["previousConfiguration"].(map[string]any)["alarmRule"])

	requireNoError(t, m.DeleteAlarms(ctx, []string{"svc"}))
	configs = bus.ofType(eventAlarmConfigChange)
	assertEqual(t, "delete", configs[len(configs)-1].detail["operation"])
}

func requireStateEventShape(t *testing.T, ev capturedEvent, name, prev, cur string) {
	t.Helper()

	assertEqual(t, "aws.cloudwatch", ev.source)
	assertEqual(t, "arn:aws:cloudwatch:us-east-1:123456789012:alarm:"+name, ev.resources[0])
	assertEqual(t, name, ev.detail["alarmName"])
	assertEqual(t, prev, ev.detail["previousState"].(map[string]any)["value"])
	assertEqual(t, cur, ev.detail["state"].(map[string]any)["value"])
	assertEqual(t, "ALARM(a)", ev.detail["configuration"].(map[string]any)["alarmRule"])
}

// reentrantComposite calls back into the mock from an SNS publish and an
// EventBridge rule target.
type reentrantComposite struct {
	m     *Mock
	clock *config.FakeClock
	calls int
}

func (p *reentrantComposite) reenter(ctx context.Context) {
	p.calls++
	if p.calls > 10 {
		return
	}

	_, _ = p.m.DescribeAlarms(ctx, nil)
	_, _ = p.m.DescribeCompositeAlarms(ctx, nil)
	_ = p.m.PutMetricData(ctx, []driver.MetricDatum{{Namespace: compositeNS, MetricName: "a", Value: 1, Timestamp: p.clock.Now()}})
}

func (p *reentrantComposite) PublishExternal(ctx context.Context, _, _ string) error {
	p.reenter(ctx)

	return nil
}

func (p *reentrantComposite) PublishServiceEvent(ctx context.Context, _, _ string, _ any, _ []string) {
	p.reenter(ctx)
}

// A composite action that calls back into the mock must not deadlock.
func TestCompositeActionReentrancy(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc)))
	p := &reentrantComposite{m: m, clock: fc}
	m.SetSNSPublisher(p)
	m.SetEventPublisher(p)

	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = m.CreateAlarm(ctx, childAlarm("a"))
		_ = m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{
			Name: "svc", AlarmRule: "ALARM(a)", AlarmActions: []string{compositeTopic}, OKActions: []string{okTopic},
		})
		_ = m.PutMetricData(ctx, []driver.MetricDatum{{Namespace: compositeNS, MetricName: "a", Value: 9, Timestamp: fc.Now()}})
		_ = m.SetAlarmState(ctx, "svc", stateOK, "forced")
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: a composite action that re-enters the mock did not return")
	}

	if p.calls == 0 {
		t.Fatal("expected re-entrant calls")
	}
}

// SetAlarmState works on a composite. The same state still replaces the
// reason and moves StateUpdatedTimestamp (API_CompositeAlarm).
func TestSetAlarmStateComposite(t *testing.T) {
	ctx := context.Background()
	m, fc, pub := newClockMock()
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("a")))
	putComposite(t, m, "svc", "ALARM(a)")

	requireNoError(t, m.SetAlarmState(ctx, "svc", stateAlarm, "testing"))
	c := composite(t, m, "svc")
	assertEqual(t, stateAlarm, c.State)
	assertEqual(t, "testing", c.StateReason)
	assertEqual(t, 1, pub.count(compositeTopic))

	fc.Advance(time.Minute)
	requireNoError(t, m.SetAlarmState(ctx, "svc", stateAlarm, "again"))
	c = composite(t, m, "svc")
	assertEqual(t, "again", c.StateReason)
	assertEqual(t, fc.Now(), c.StateUpdatedTimestamp)
	assertEqual(t, 1, pub.count(compositeTopic))
}

// CW-X4: a same-state SetAlarmState on a metric alarm replaces the reason and
// keeps StateUpdatedTimestamp, which API_MetricAlarm ties to StateValue and
// EvaluationState only. This locks in behaviour that was already correct.
func TestSetAlarmStateSameStateMetricAlarm(t *testing.T) {
	ctx := context.Background()
	m, fc, _ := newClockMock()
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("a")))
	requireNoError(t, m.SetAlarmState(ctx, "a", stateAlarm, "first"))

	before := mustAlarms(t, m)[0].StateUpdatedTimestamp

	fc.Advance(time.Minute)
	requireNoError(t, m.SetAlarmState(ctx, "a", stateAlarm, "second"))

	a := mustAlarms(t, m)[0]
	assertEqual(t, "second", a.StateReason)
	assertEqual(t, before, a.StateUpdatedTimestamp)
}

func TestCompositeSnapshotKeepsSuppressor(t *testing.T) {
	ctx := context.Background()
	m, _, _ := newClockMock()
	suppressed(t, m, 30, 40)
	requireNoError(t, m.SetAlarmState(ctx, "a", stateAlarm, "breach"))

	raw, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored, _, _ := newClockMock()
	requireNoError(t, restored.Restore(ctx, raw))

	c := composite(t, restored, "svc")
	assertEqual(t, "sup", c.ActionsSuppressor)
	assertEqual(t, 30, c.ActionsSuppressorWaitPeriod)
	assertEqual(t, 40, c.ActionsSuppressorExtensionPeriod)
	assertEqual(t, "WaitPeriod", c.ActionsSuppressedBy)
}
