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
	eventSource           = "aws.cloudwatch"
	eventAlarmStateChange = "CloudWatch Alarm State Change"
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

// alarmStateEvent is a state change event waiting to be published.
type alarmStateEvent struct {
	arn    string
	detail alarmStateChangeDetail
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

// stateEventLocked builds the event for a transition out of prev. It runs
// after the alarm holds its new state. The caller holds alarmMu.
func stateEventLocked(a *alarmData, prev alarmEventState) *alarmStateEvent {
	// Alarms restored from an older snapshot have no query id yet.
	if a.MetricQueryID == "" {
		a.MetricQueryID = idgen.UUID()
	}

	stat := a.Stat
	if a.ExtendedStatistic != "" {
		stat = a.ExtendedStatistic
	}

	return &alarmStateEvent{
		arn: a.AlarmArn,
		detail: alarmStateChangeDetail{
			AlarmName:     a.Name,
			State:         eventState(a.State, a.StateReason, a.StateReasonData, a.StateUpdatedTimestamp),
			PreviousState: prev,
			Configuration: alarmEventSettings{
				Description: a.AlarmDescription,
				Metrics: []alarmEventMetric{{
					ID: a.MetricQueryID,
					MetricStat: alarmEventMetricRef{
						Metric: alarmEventMetricID{Dimensions: copyMap(a.Dimensions), Name: a.MetricName, Namespace: a.Namespace},
						Period: a.Period,
						Stat:   stat,
					},
					ReturnData: true,
				}},
			},
		},
	}
}

// emitStateEvent publishes one state change event. The caller must not hold
// alarmMu or mu, because a rule target may call straight back into this mock.
// ctx carries the hop depth, so a target that changes the alarm again cannot
// loop forever.
func (m *Mock) emitStateEvent(ctx context.Context, ev *alarmStateEvent) {
	m.events.Emit(ctx, eventSource, eventAlarmStateChange, ev.detail, ev.arn)
}
