package monitor

import (
	"fmt"
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
		if alarmReads(a, keys) {
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

// alarmReads reports whether an alert reads one of the given metrics.
func alarmReads(a *alarmData, keys map[metricKey]bool) bool {
	if keys[metricKey{Namespace: a.Namespace, MetricName: a.MetricName}] {
		return true
	}

	for i := range a.Criteria {
		if keys[metricKey{Namespace: a.Criteria[i].Namespace, MetricName: a.Criteria[i].MetricName}] {
			return true
		}
	}

	return false
}

// evaluateLocked evaluates one alert rule at now and applies the result. The
// caller holds alarmMu.
func (m *Mock) evaluateLocked(alarm *alarmData, now time.Time) *alertFire {
	if len(alarm.Criteria) > 0 {
		return m.evaluateCriteriaLocked(alarm, now)
	}

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

// evaluateCriteriaLocked evaluates each criterion of a multi-criteria alert.
// The alert is in ALARM when every criterion is, OK when any criterion is OK,
// and INSUFFICIENT_DATA otherwise. A criterion whose missing data is ignored
// keeps its last state. The caller holds alarmMu.
func (m *Mock) evaluateCriteriaLocked(alarm *alarmData, now time.Time) *alertFire {
	breached, ok := 0, 0

	for i := range alarm.Criteria {
		c := &alarm.Criteria[i]

		params := alarmParams(alarm)
		params.Stat = c.Stat
		params.ComparisonOperator = c.ComparisonOperator
		params.Threshold = c.Threshold
		at := params.EvaluationTime(now)

		filtered := m.collectFilteredDatums(c.Namespace, c.MetricName, c.Dimensions, alarm.Unit, params.WindowStart(at), at)
		if out := alarmeval.EvaluateWindow(filtered, &params, at); !out.Retain {
			c.State = out.State
		}

		switch c.State {
		case alarmeval.StateAlarm:
			breached++
		case alarmeval.StateOK:
			ok++
		}
	}

	alarm.LastEvaluatedAt = now

	switch {
	case breached == len(alarm.Criteria):
		return m.transitionLocked(alarm, alarmeval.StateAlarm, fmt.Sprintf("All %d criteria are met", breached), now)
	case ok > 0:
		return m.transitionLocked(alarm, alarmeval.StateOK, fmt.Sprintf("%d of %d criteria are not met", ok, len(alarm.Criteria)), now)
	default:
		return m.transitionLocked(alarm, alarmeval.StateInsufficientData, "Insufficient data for the alert criteria", now)
	}
}
