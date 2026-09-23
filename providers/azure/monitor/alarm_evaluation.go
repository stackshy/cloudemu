package monitor

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
)

// alertFire is one transition into ALARM waiting for its action groups. It
// holds a copy of the rule so delivery can run after alarmMu is released.
type alertFire struct {
	alarm    alarmData
	newState string
	now      time.Time
}

// Tick evaluates every alert rule that is due at now and reports whether any
// rule changed state. It has the Tickable signature of the shared scheduler
// seam. Reads already evaluate lazily, so nothing has to call it.
func (m *Mock) Tick(now time.Time) bool {
	return m.evaluateDue(now)
}

// evaluateDue evaluates each alert rule whose evaluation interval has passed
// since it was last evaluated. It reports whether any rule changed state.
func (m *Mock) evaluateDue(now time.Time) bool {
	var (
		fires   []*alertFire
		changed bool
	)

	m.alarmMu.Lock()

	for _, a := range m.alarms.All() {
		if !alarmeval.Due(a.LastEvaluatedAt, now, alarmeval.EvaluationInterval(a.Period, a.EvaluationPeriods)) {
			continue
		}

		old := a.State
		fires = append(fires, m.evaluateLocked(a, now))
		changed = changed || a.State != old
	}

	m.alarmMu.Unlock()

	m.deliver(fires)

	return changed
}

// evaluateMetricAlarms evaluates, right away, every alert rule on one of the
// given metrics.
func (m *Mock) evaluateMetricAlarms(keys map[metricKey]bool) {
	now := m.opts.Clock.Now()

	var fires []*alertFire

	m.alarmMu.Lock()

	for _, a := range m.alarms.All() {
		if keys[metricKey{Namespace: a.Namespace, MetricName: a.MetricName}] {
			fires = append(fires, m.evaluateLocked(a, now))
		}
	}

	m.alarmMu.Unlock()

	m.deliver(fires)
}

// alarmParams projects an alert rule's thresholds onto the shared evaluator's Params.
func alarmParams(alarm *alarmData) alarmeval.Params {
	return alarmeval.Params{
		Period:             alarm.Period,
		EvaluationPeriods:  alarm.EvaluationPeriods,
		DatapointsToAlarm:  alarm.DatapointsToAlarm,
		Stat:               alarm.Stat,
		ComparisonOperator: alarm.ComparisonOperator,
		Threshold:          alarm.Threshold,
		TreatMissingData:   alarm.TreatMissingData,
	}
}

// evaluateLocked evaluates one alert rule at now and applies the result. The
// caller holds alarmMu.
func (m *Mock) evaluateLocked(alarm *alarmData, now time.Time) *alertFire {
	params := alarmParams(alarm)
	at := params.EvaluationTime(now)

	filtered := m.collectFilteredDatums(alarm.Namespace, alarm.MetricName, alarm.Dimensions, alarm.Unit, params.WindowStart(at), at)
	out := alarmeval.EvaluateWindow(filtered, &params, at)

	alarm.LastEvaluatedAt = now

	if out.Retain {
		return nil
	}

	return m.transitionLocked(alarm, out.State, out.Reason, now)
}

// transitionLocked moves an alert rule to newState and records a history
// entry. Nothing happens when the state is unchanged. A move into ALARM
// returns the action groups to fire. The caller holds alarmMu.
func (m *Mock) transitionLocked(alarm *alarmData, newState, reason string, now time.Time) *alertFire {
	oldState := alarm.State
	if oldState == newState {
		return nil
	}

	m.appendHistory(alarm.Name, oldState, newState, reason, now)

	alarm.State = newState
	alarm.StateReason = reason
	alarm.StateUpdatedTimestamp = now
	alarm.StateTransitionedTimestamp = now

	if newState != alarmeval.StateAlarm {
		return nil
	}

	return &alertFire{alarm: *alarm, newState: newState, now: now}
}

// deliver fires the action groups of each pending transition. Callers must
// not hold alarmMu or mu, because a receiver may call back into this mock.
func (m *Mock) deliver(fires []*alertFire) {
	for _, f := range fires {
		if f != nil {
			m.fireActionGroups(&f.alarm, f.newState, f.now)
		}
	}
}
