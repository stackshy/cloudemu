package cloudwatch

// This file implements the CloudWatch composite-alarm operations over the
// rpc-v2-cbor protocol, backing the aws_cloudwatch_composite_alarm Terraform
// resource: PutCompositeAlarm creates one, DescribeAlarms surfaces them in the
// CompositeAlarms list, and DeleteAlarms removes them. The store is an AWS-local
// optional capability so the shared Monitoring interface stays unchanged.

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	alarmTypeComposite = "CompositeAlarm"
	alarmTypeMetric    = "MetricAlarm"
)

// compositeAlarmStore is the AWS-local capability behind the composite-alarm
// operations.
type compositeAlarmStore interface {
	PutCompositeAlarm(ctx context.Context, cfg mondriver.CompositeAlarmConfig) error
	DescribeCompositeAlarms(ctx context.Context, names []string) ([]mondriver.CompositeAlarmInfo, error)
	DeleteCompositeAlarms(ctx context.Context, names []string) error
}

type putCompositeAlarmInput struct {
	AlarmName               string   `cbor:"AlarmName"`
	AlarmRule               string   `cbor:"AlarmRule"`
	AlarmDescription        string   `cbor:"AlarmDescription,omitempty"`
	ActionsEnabled          *bool    `cbor:"ActionsEnabled,omitempty"`
	AlarmActions            []string `cbor:"AlarmActions,omitempty"`
	OKActions               []string `cbor:"OKActions,omitempty"`
	InsufficientDataActions []string `cbor:"InsufficientDataActions,omitempty"`
	Tags                    []tagCBR `cbor:"Tags,omitempty"`
}

func (h *Handler) putCompositeAlarm(w http.ResponseWriter, r *http.Request, body []byte) {
	store, ok := h.monitoring.(compositeAlarmStore)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "composite alarms not supported")
		return
	}

	var in putCompositeAlarmInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	err := store.PutCompositeAlarm(r.Context(), mondriver.CompositeAlarmConfig{
		Name:                    in.AlarmName,
		AlarmRule:               in.AlarmRule,
		AlarmDescription:        in.AlarmDescription,
		ActionsEnabled:          in.ActionsEnabled,
		AlarmActions:            in.AlarmActions,
		OKActions:               in.OKActions,
		InsufficientDataActions: in.InsufficientDataActions,
		Tags:                    tagsToMap(in.Tags),
	})
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

type compositeAlarmCBR struct {
	AlarmName               string     `cbor:"AlarmName"`
	AlarmArn                string     `cbor:"AlarmArn,omitempty"`
	AlarmRule               string     `cbor:"AlarmRule"`
	AlarmDescription        string     `cbor:"AlarmDescription,omitempty"`
	StateValue              string     `cbor:"StateValue"`
	StateReason             string     `cbor:"StateReason,omitempty"`
	StateUpdatedTimestamp   *time.Time `cbor:"StateUpdatedTimestamp,omitempty"`
	ActionsEnabled          bool       `cbor:"ActionsEnabled"`
	AlarmActions            []string   `cbor:"AlarmActions,omitempty"`
	OKActions               []string   `cbor:"OKActions,omitempty"`
	InsufficientDataActions []string   `cbor:"InsufficientDataActions,omitempty"`
}

// wantsAlarmType reports whether a DescribeAlarms request that lists alarmTypes
// asks for the given type. An empty list means "both", matching modern AWS.
func wantsAlarmType(alarmTypes []string, want string) bool {
	if len(alarmTypes) == 0 {
		return true
	}

	for _, t := range alarmTypes {
		if t == want {
			return true
		}
	}

	return false
}

// compositeAlarmRows returns the composite alarms matching the DescribeAlarms
// filters (names, name prefix, state), sorted by name and rendered for the wire.
func (h *Handler) compositeAlarmRows(r *http.Request, in *describeAlarmsInput) ([]compositeAlarmCBR, error) {
	store, ok := h.monitoring.(compositeAlarmStore)
	if !ok {
		return nil, nil
	}

	alarms, err := store.DescribeCompositeAlarms(r.Context(), in.AlarmNames)
	if err != nil {
		return nil, err
	}

	rows := make([]compositeAlarmCBR, 0, len(alarms))

	for i := range alarms {
		a := &alarms[i]
		if in.AlarmNamePrefix != "" && !strings.HasPrefix(a.Name, in.AlarmNamePrefix) {
			continue
		}

		if in.StateValue != "" && a.State != in.StateValue {
			continue
		}

		rows = append(rows, toCompositeAlarmCBR(a))
	}

	sort.SliceStable(rows, func(i, j int) bool { return rows[i].AlarmName < rows[j].AlarmName })

	return rows, nil
}

func toCompositeAlarmCBR(a *mondriver.CompositeAlarmInfo) compositeAlarmCBR {
	c := compositeAlarmCBR{
		AlarmName:               a.Name,
		AlarmArn:                a.ARN,
		AlarmRule:               a.AlarmRule,
		AlarmDescription:        a.AlarmDescription,
		StateValue:              a.State,
		StateReason:             a.StateReason,
		ActionsEnabled:          a.ActionsEnabled,
		AlarmActions:            a.AlarmActions,
		OKActions:               a.OKActions,
		InsufficientDataActions: a.InsufficientDataActions,
	}

	if !a.StateUpdatedTimestamp.IsZero() {
		ts := a.StateUpdatedTimestamp.UTC()
		c.StateUpdatedTimestamp = &ts
	}

	return c
}
