package cloudwatch

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/awsevents"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// EventBridge identity of the alarm state change event. See "Alarm events and
// EventBridge" in the CloudWatch user guide.
const (
	eventSource            = "aws.cloudwatch"
	eventAlarmStateChange  = "CloudWatch Alarm State Change"
	eventAlarmConfigChange = "CloudWatch Alarm Configuration Change"
	operationCreate        = "create"
	operationUpdate        = "update"
	operationDelete        = "delete"
)

// alarmStateChangeDetail is the detail of a "CloudWatch Alarm State Change"
// event for a metric alarm.
type alarmStateChangeDetail struct {
	AlarmName     string             `json:"alarmName"`
	State         alarmEventState    `json:"state"`
	PreviousState alarmEventState    `json:"previousState"`
	Configuration alarmEventSettings `json:"configuration"`
}

// alarmEventState is one side of the transition. reasonData is left out when
// the state has none, as in the AWS examples.
type alarmEventState struct {
	Value      string `json:"value"`
	Reason     string `json:"reason"`
	ReasonData string `json:"reasonData,omitempty"`
	Timestamp  string `json:"timestamp"`
}

// alarmEventSettings is the configuration block of the event.
type alarmEventSettings struct {
	Description string             `json:"description,omitempty"`
	Metrics     []alarmEventMetric `json:"metrics"`
}

// alarmEventMetric is one metric query of the alarm.
type alarmEventMetric struct {
	ID         string              `json:"id"`
	MetricStat alarmEventMetricRef `json:"metricStat"`
	ReturnData bool                `json:"returnData"`
}

// alarmEventMetricRef is the metricStat of a metric query.
type alarmEventMetricRef struct {
	Metric alarmEventMetricID `json:"metric"`
	Period int                `json:"period"`
	Stat   string             `json:"stat"`
}

// alarmEventMetricID names the watched metric.
type alarmEventMetricID struct {
	Dimensions map[string]string `json:"dimensions"`
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
}

// alarmConfigChangeDetail is the detail of a "CloudWatch Alarm Configuration
// Change" event for a metric alarm. previousConfiguration is set on update only.
type alarmConfigChangeDetail struct {
	AlarmName             string           `json:"alarmName"`
	Operation             string           `json:"operation"`
	State                 alarmConfigState `json:"state"`
	Configuration         alarmConfigBody  `json:"configuration"`
	PreviousConfiguration *alarmConfigBody `json:"previousConfiguration,omitempty"`
}

// alarmConfigState is the alarm state carried by a configuration event.
type alarmConfigState struct {
	Value     string `json:"value"`
	Timestamp string `json:"timestamp"`
}

// alarmConfigBody is the configuration block of a configuration event.
type alarmConfigBody struct {
	EvaluationPeriods       int                `json:"evaluationPeriods"`
	Threshold               float64            `json:"threshold"`
	ComparisonOperator      string             `json:"comparisonOperator"`
	TreatMissingData        string             `json:"treatMissingData,omitempty"`
	Metrics                 []alarmEventMetric `json:"metrics"`
	AlarmName               string             `json:"alarmName"`
	Description             string             `json:"description,omitempty"`
	ActionsEnabled          bool               `json:"actionsEnabled"`
	Timestamp               string             `json:"timestamp"`
	OKActions               []string           `json:"okActions"`
	AlarmActions            []string           `json:"alarmActions"`
	InsufficientDataActions []string           `json:"insufficientDataActions"`
}

// alarmStateEvent is an event waiting to be published.
type alarmStateEvent struct {
	arn        string
	detailType string
	detail     any
}

// SetEventPublisher wires the EventBridge default bus that alarm state
// changes are published to. Left unset, no events are emitted.
func (m *Mock) SetEventPublisher(p awsevents.Publisher) {
	m.events.SetPublisher(p)
}

// eventState renders one alarm state for the event.
func eventState(value, reason, reasonData string, at time.Time) alarmEventState {
	return alarmEventState{
		Value:      value,
		Reason:     reason,
		ReasonData: reasonData,
		Timestamp:  at.UTC().Format(reasonDataTimeFormat),
	}
}

// eventMetricsLocked renders the alarm's metric query. The caller holds
// alarmMu.
func eventMetricsLocked(a *alarmData) []alarmEventMetric {
	// Alarms restored from an older snapshot have no query id yet.
	if a.MetricQueryID == "" {
		a.MetricQueryID = idgen.UUID()
	}

	stat := a.Stat
	if a.ExtendedStatistic != "" {
		stat = a.ExtendedStatistic
	}

	return []alarmEventMetric{{
		ID: a.MetricQueryID,
		MetricStat: alarmEventMetricRef{
			Metric: alarmEventMetricID{Dimensions: copyMap(a.Dimensions), Name: a.MetricName, Namespace: a.Namespace},
			Period: a.Period,
			Stat:   stat,
		},
		ReturnData: true,
	}}
}

// stateEventLocked builds the event for a transition out of prev. It runs
// after the alarm holds its new state. The caller holds alarmMu.
func stateEventLocked(a *alarmData, prev alarmEventState) *alarmStateEvent {
	return &alarmStateEvent{
		arn:        a.AlarmArn,
		detailType: eventAlarmStateChange,
		detail: alarmStateChangeDetail{
			AlarmName:     a.Name,
			State:         eventState(a.State, a.StateReason, a.StateReasonData, a.StateUpdatedTimestamp),
			PreviousState: prev,
			Configuration: alarmEventSettings{Description: a.AlarmDescription, Metrics: eventMetricsLocked(a)},
		},
	}
}

// configBodyLocked renders an alarm's configuration. The caller holds alarmMu.
func configBodyLocked(a *alarmData) alarmConfigBody {
	updated := a.ConfigUpdatedAt
	if updated.IsZero() {
		updated = a.StateUpdatedTimestamp
	}

	return alarmConfigBody{
		EvaluationPeriods:       a.EvaluationPeriods,
		Threshold:               a.Threshold,
		ComparisonOperator:      a.ComparisonOperator,
		TreatMissingData:        a.TreatMissingData,
		Metrics:                 eventMetricsLocked(a),
		AlarmName:               a.Name,
		Description:             a.AlarmDescription,
		ActionsEnabled:          a.ActionsEnabled,
		Timestamp:               updated.UTC().Format(reasonDataTimeFormat),
		OKActions:               append([]string{}, a.OKActions...),
		AlarmActions:            append([]string{}, a.AlarmActions...),
		InsufficientDataActions: append([]string{}, a.InsufficientDataActions...),
	}
}

// configEventLocked builds a configuration event for a. prev is the replaced
// configuration on update and nil otherwise. The caller holds alarmMu.
func configEventLocked(operation string, a, prev *alarmData) *alarmStateEvent {
	detail := alarmConfigChangeDetail{
		AlarmName:     a.Name,
		Operation:     operation,
		State:         alarmConfigState{Value: a.State, Timestamp: a.StateUpdatedTimestamp.UTC().Format(reasonDataTimeFormat)},
		Configuration: configBodyLocked(a),
	}

	if prev != nil {
		body := configBodyLocked(prev)
		detail.PreviousConfiguration = &body
	}

	return &alarmStateEvent{arn: a.AlarmArn, detailType: eventAlarmConfigChange, detail: detail}
}

// emitEvent publishes one alarm event. The caller must not hold
// alarmMu or mu, because a rule target may call straight back into this mock.
// ctx carries the hop depth, so a target that changes the alarm again cannot
// loop forever.
func (m *Mock) emitEvent(ctx context.Context, ev *alarmStateEvent) {
	if ev == nil {
		return
	}

	m.events.Emit(ctx, eventSource, ev.detailType, ev.detail, ev.arn)
}
