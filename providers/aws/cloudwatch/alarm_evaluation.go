package cloudwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// dynamoDBNamespace alarms ignore missing data unless told otherwise. See
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/alarms-and-missing-data.html.
const dynamoDBNamespace = "AWS/DynamoDB"

// alarmNotice is one state change waiting to be published: its EventBridge
// event and the message for its SNS topics. It is built under alarmMu and
// published after the lock is released, so a subscriber or rule target that
// calls back into this mock cannot deadlock.
type alarmNotice struct {
	event   *alarmStateEvent
	topics  []string
	message string
}

// Tick evaluates every alarm that is due at now and reports whether any
// alarm changed state. It has the Tickable signature of the shared scheduler
// seam. Reads already evaluate lazily, so nothing has to call it.
func (m *Mock) Tick(now time.Time) bool {
	return m.evaluateDue(context.Background(), now)
}

// evaluateDue evaluates each alarm whose evaluation interval has passed since
// it was last evaluated. It reports whether any alarm changed state.
func (m *Mock) evaluateDue(ctx context.Context, now time.Time) bool {
	var (
		notices []*alarmNotice
		changed bool
	)

	m.alarmMu.Lock()

	for _, a := range m.alarms.All() {
		if !alarmeval.Due(a.LastEvaluatedAt, now, alarmeval.EvaluationInterval(a.Period, a.EvaluationPeriods)) {
			continue
		}

		old := a.State
		notices = append(notices, m.evaluateLocked(a, now))
		changed = changed || a.State != old
	}

	m.alarmMu.Unlock()

	m.publish(ctx, notices...)

	return changed
}

// evaluateMetricAlarms evaluates, right away, every alarm on one of the given
// metrics. PutMetricData calls it so new data shows up without waiting for
// the next interval.
func (m *Mock) evaluateMetricAlarms(ctx context.Context, keys map[metricKey]bool) {
	now := m.opts.Clock.Now()

	var notices []*alarmNotice

	m.alarmMu.Lock()

	for _, a := range m.alarms.All() {
		if keys[metricKey{Namespace: a.Namespace, MetricName: a.MetricName}] {
			notices = append(notices, m.evaluateLocked(a, now))
		}
	}

	m.alarmMu.Unlock()

	m.publish(ctx, notices...)
}

// alarmParams projects an alarm's thresholds onto the shared evaluator's Params.
func alarmParams(alarm *alarmData) alarmeval.Params {
	return alarmeval.Params{
		Period:                 alarm.Period,
		EvaluationPeriods:      alarm.EvaluationPeriods,
		DatapointsToAlarm:      alarm.DatapointsToAlarm,
		Stat:                   alarm.Stat,
		ComparisonOperator:     alarm.ComparisonOperator,
		Threshold:              alarm.Threshold,
		TreatMissingData:       alarm.TreatMissingData,
		IgnoreMissingByDefault: alarm.Namespace == dynamoDBNamespace,
	}
}

// evaluateLocked evaluates one alarm at now and applies the result. The
// caller holds alarmMu. It returns the notice to publish, or nil.
func (m *Mock) evaluateLocked(alarm *alarmData, now time.Time) *alarmNotice {
	params := alarmParams(alarm)
	at := params.EvaluationTime(now)

	// Data stored under another unit is not seen, so an alarm with the wrong
	// unit stays in INSUFFICIENT_DATA like on AWS.
	filtered := m.collectFilteredDatums(alarm.Namespace, alarm.MetricName, alarm.Dimensions, alarm.Unit, params.WindowStart(at), at)
	out := alarmeval.EvaluateWindow(filtered, &params, at)

	alarm.LastEvaluatedAt = now

	if out.Retain {
		return nil
	}

	return m.transitionLocked(alarm, out.State, out.Reason, evaluationReasonData(filtered, &params, at), now)
}

// evaluationReasonData is the stateReasonData of a metric-driven transition.
// It is a small subset of what CloudWatch sends.
func evaluationReasonData(datums []driver.MetricDatum, p *alarmeval.Params, now time.Time) string {
	data := struct {
		Version          string    `json:"version"`
		QueryDate        string    `json:"queryDate"`
		StartDate        string    `json:"startDate"`
		Statistic        string    `json:"statistic"`
		Period           int       `json:"period"`
		RecentDatapoints []float64 `json:"recentDatapoints"`
		Threshold        float64   `json:"threshold"`
	}{
		Version:          "1.0",
		QueryDate:        now.UTC().Format(reasonDataTimeFormat),
		StartDate:        p.WindowStart(now).UTC().Format(reasonDataTimeFormat),
		Statistic:        p.Stat,
		Period:           p.Period,
		RecentDatapoints: alarmeval.RecentDatapoints(datums, p, now),
		Threshold:        p.Threshold,
	}

	b, err := json.Marshal(data)
	if err != nil {
		return ""
	}

	return string(b)
}

// transitionLocked moves an alarm to newState. Nothing happens when the state
// is unchanged: the reason, the timestamps and the history all describe the
// last transition, and actions and events fire only on a change. The caller
// holds alarmMu. It returns the notice to publish, or nil.
func (m *Mock) transitionLocked(alarm *alarmData, newState, reason, reasonData string, now time.Time) *alarmNotice {
	oldState := alarm.State
	if oldState == newState {
		return nil
	}

	m.appendHistory(alarm, newState, reason, reasonData, now)

	prev := eventState(alarm.State, alarm.StateReason, alarm.StateReasonData, alarm.StateUpdatedTimestamp)

	alarm.State = newState
	alarm.StateReason = reason
	alarm.StateReasonData = reasonData
	alarm.StateUpdatedTimestamp = now
	alarm.StateTransitionedTimestamp = now

	// The event is sent even when actions are disabled.
	notice := &alarmNotice{event: stateEventLocked(alarm, prev)}
	notice.topics, notice.message = m.actionTopics(alarm, oldState, newState, now)

	return notice
}

// appendHistory records one alarm state transition in the history log.
// It runs before the alarm is updated, so alarm still holds the old state.
func (m *Mock) appendHistory(alarm *alarmData, newState, reason, reasonData string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.history = append(m.history, driver.AlarmHistoryEntry{
		AlarmName:          alarm.Name,
		Timestamp:          now,
		OldState:           alarm.State,
		NewState:           newState,
		HistoryItemType:    historyStateUpdate,
		Reason:             fmt.Sprintf("Transition from %s to %s: %s", alarm.State, newState, reason),
		OldStateReasonData: alarm.StateReasonData,
		NewStateReasonData: reasonData,
	})
}

// actionTopics returns the SNS topics and message for a state change. There
// are no topics when no publisher is wired, actions are disabled, or the new
// state has no SNS actions. Other action ARNs (Auto Scaling, EC2) are stored
// but not fired.
func (m *Mock) actionTopics(a *alarmData, oldState, newState string, now time.Time) (topics []string, message string) {
	if m.sns == nil || !a.ActionsEnabled {
		return nil, ""
	}

	var actions []string

	switch newState {
	case stateAlarm:
		actions = a.AlarmActions
	case stateOK:
		actions = a.OKActions
	case stateInsufficientData:
		actions = a.InsufficientDataActions
	}

	for _, arn := range actions {
		if strings.HasPrefix(arn, snsTopicARNPrefix) {
			topics = append(topics, arn)
		}
	}

	if len(topics) == 0 {
		return nil, ""
	}

	return topics, m.alarmNotification(a, oldState, newState, now)
}

// publish sends each notice's event to EventBridge and its message to its SNS
// topics. Callers must not hold alarmMu or mu, because a subscriber or rule
// target may call straight back into this mock.
func (m *Mock) publish(ctx context.Context, notices ...*alarmNotice) {
	for _, n := range notices {
		if n == nil {
			continue
		}

		if n.event != nil {
			m.emitStateEvent(ctx, n.event)
		}

		for _, arn := range n.topics {
			_ = m.sns.PublishExternal(context.Background(), arn, n.message)
		}
	}
}

// alarmNotification renders the JSON body CloudWatch publishes to an SNS topic
// on a state change. It mirrors the real notification's key fields so a
// subscriber (e.g. an SQS queue) receives a recognizable alarm payload.
func (m *Mock) alarmNotification(a *alarmData, oldState, newState string, now time.Time) string {
	payload := map[string]any{
		"AlarmName":        a.Name,
		"AlarmDescription": a.AlarmDescription,
		"AWSAccountId":     m.opts.AccountID,
		"Region":           m.opts.Region,
		"NewStateValue":    newState,
		"NewStateReason":   a.StateReason,
		"OldStateValue":    oldState,
		"StateChangeTime":  now.UTC().Format(time.RFC3339),
		"Trigger": map[string]any{
			"MetricName":         a.MetricName,
			"Namespace":          a.Namespace,
			"Statistic":          a.Stat,
			"ComparisonOperator": a.ComparisonOperator,
			"Threshold":          a.Threshold,
			"Period":             a.Period,
			"EvaluationPeriods":  a.EvaluationPeriods,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ""
	}

	return string(body)
}

func (m *Mock) collectFilteredDatums(
	namespace, metricName string, dims map[string]string, unit string, windowStart, now time.Time,
) []driver.MetricDatum {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := metricKey{Namespace: namespace, MetricName: metricName}
	dataPoints := m.metrics[key]

	var filtered []driver.MetricDatum

	for i := range dataPoints {
		d := &dataPoints[i]
		if d.Timestamp.Before(windowStart) || d.Timestamp.After(now) {
			continue
		}

		if !alarmeval.MatchDimensions(d.Dimensions, dims) || !alarmeval.MatchUnit(d.Unit, unit) {
			continue
		}

		filtered = append(filtered, *d)
	}

	return filtered
}
