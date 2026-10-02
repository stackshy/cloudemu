package monitoring

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
)

// incidentFire is one policy transition waiting for channel delivery. It holds
// a copy of the policy so delivery can run after alarmMu is released.
type incidentFire struct {
	alarm    alarmData
	newState string
	now      time.Time
}

// Tick evaluates every alert policy that is due at now and reports whether
// any policy changed state. It has the Tickable signature of the shared
// scheduler seam. Reads already evaluate lazily, so nothing has to call it.
func (m *Mock) Tick(now time.Time) bool {
	return m.evaluateDue(now)
}

// evaluateDue evaluates each alert policy whose evaluation interval has
// passed since it was last evaluated. It reports whether any policy changed.
func (m *Mock) evaluateDue(now time.Time) bool {
	var (
		fires   []*incidentFire
		changed bool
	)

	m.alarmMu.Lock()

	for _, a := range m.alarms.All() {
		if alarmeval.Due(a.LastEvaluatedAt, now, alarmeval.EvaluationInterval(a.Period, a.EvaluationPeriods)) {
			old := a.State
			fires = append(fires, m.evaluateLocked(a, now))
			changed = changed || a.State != old
		}
	}

	m.alarmMu.Unlock()

	m.deliver(fires)

	return changed
}

// evaluateMetricAlarms evaluates, right away, every alert policy on one of
// the given metrics.
func (m *Mock) evaluateMetricAlarms(keys map[metricKey]bool) {
	now := m.opts.Clock.Now()

	var fires []*incidentFire

	m.alarmMu.Lock()

	for _, a := range m.alarms.All() {
		if keys[metricKey{Namespace: a.Namespace, MetricName: a.MetricName}] {
			fires = append(fires, m.evaluateLocked(a, now))
		}
	}

	m.alarmMu.Unlock()

	m.deliver(fires)
}

// evaluateLocked evaluates one alert policy at now and applies the result.
// The caller holds alarmMu.
func (m *Mock) evaluateLocked(alarm *alarmData, now time.Time) *incidentFire {
	params := alarmeval.Params{
		Period:             alarm.Period,
		EvaluationPeriods:  alarm.EvaluationPeriods,
		DatapointsToAlarm:  alarm.DatapointsToAlarm,
		Stat:               alarm.Stat,
		ComparisonOperator: alarm.ComparisonOperator,
		Threshold:          alarm.Threshold,
		TreatMissingData:   alarm.TreatMissingData,
	}
	at := params.EvaluationTime(now)

	filtered := m.collectFilteredDatums(alarm.Namespace, alarm.MetricName, alarm.Dimensions, alarm.Unit, params.WindowStart(at), at)
	out := alarmeval.EvaluateWindow(filtered, &params, at)

	alarm.LastEvaluatedAt = now

	if out.Retain {
		return nil
	}

	return m.transitionLocked(alarm, out.State, out.Reason, now)
}

// transitionLocked moves an alert policy to newState and records a history
// entry. Nothing happens when the state is unchanged. A change returns the
// delivery to run. The caller holds alarmMu.
func (m *Mock) transitionLocked(alarm *alarmData, newState, reason string, now time.Time) *incidentFire {
	oldState := alarm.State
	if oldState == newState {
		return nil
	}

	m.appendHistory(alarm.Name, oldState, newState, reason, now)

	alarm.State = newState
	alarm.StateReason = reason
	alarm.StateUpdatedTimestamp = now
	alarm.StateTransitionedTimestamp = now

	return &incidentFire{alarm: *alarm, newState: newState, now: now}
}

// deliver sends each pending incident to its channels. Incidents open on ALARM
// and close on OK. Callers must not hold alarmMu or mu, because a channel
// may call back into this mock.
func (m *Mock) deliver(fires []*incidentFire) {
	for _, f := range fires {
		if f != nil {
			m.fireNotificationChannels(&f.alarm, f.newState, f.now)
		}
	}
}
