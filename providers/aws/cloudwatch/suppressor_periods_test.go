package cloudwatch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// A transition during an active ExtensionPeriod stays held until the period
// ends, with or without a WaitPeriod. Before, it fired at once.
func TestExtensionPeriodHoldsLaterTransition(t *testing.T) {
	for _, wait := range []int{0, 60} {
		t.Run(fmt.Sprintf("wait %d", wait), func(t *testing.T) {
			ctx := context.Background()
			m, fc, pub := newClockMock()
			suppressed(t, m, wait, 10)

			requireNoError(t, m.SetAlarmState(ctx, "sup", stateAlarm, "maintenance"))
			requireNoError(t, m.SetAlarmState(ctx, "sup", stateOK, "done"))
			assertEqual(t, "ExtensionPeriod", composite(t, m, "svc").ActionsSuppressedBy)

			requireNoError(t, m.SetAlarmState(ctx, "a", stateAlarm, "breach"))

			c := composite(t, m, "svc")
			assertEqual(t, stateAlarm, c.State)
			assertEqual(t, "ExtensionPeriod", c.ActionsSuppressedBy)
			assertEqual(t, 0, pub.count(compositeTopic))

			fc.Advance(9 * time.Second)
			assertEqual(t, 0, pub.count(compositeTopic))

			fc.Advance(time.Second)
			assertEqual(t, "", composite(t, m, "svc").ActionsSuppressedBy)
			assertEqual(t, 1, pub.count(compositeTopic))
		})
	}
}

// The WaitPeriod applies only to a move into ALARM. The first evaluation to
// OK and a return to OK fire their OK actions at once. Before, both waited.
func TestWaitPeriodOnlyForAlarm(t *testing.T) {
	ctx := context.Background()
	m, _, pub := newClockMock()
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("a")))
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("sup")))
	requireNoError(t, m.PutCompositeAlarm(ctx, driver.CompositeAlarmConfig{
		Name: "svc", AlarmRule: "ALARM(a)", AlarmActions: []string{compositeTopic}, OKActions: []string{okTopic},
		ActionsSuppressor: "sup", ActionsSuppressorWaitPeriod: intp(120), ActionsSuppressorExtensionPeriod: intp(60),
	}))

	c := composite(t, m, "svc")
	assertEqual(t, stateOK, c.State)
	assertEqual(t, "", c.ActionsSuppressedBy)
	assertEqual(t, 1, pub.count(okTopic))

	requireNoError(t, m.SetAlarmState(ctx, "a", stateAlarm, "breach"))
	assertEqual(t, "WaitPeriod", composite(t, m, "svc").ActionsSuppressedBy)

	requireNoError(t, m.SetAlarmState(ctx, "a", stateOK, "calm"))

	c = composite(t, m, "svc")
	assertEqual(t, stateOK, c.State)
	assertEqual(t, "", c.ActionsSuppressedBy)
	assertEqual(t, 2, pub.count(okTopic))
	assertEqual(t, 0, pub.count(compositeTopic))
}
