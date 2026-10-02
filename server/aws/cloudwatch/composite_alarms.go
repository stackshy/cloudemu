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
	"time"

	"github.com/fxamacker/cbor/v2"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
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

// alarmBatchDeleter deletes metric and composite alarms in one checked call.
// The AWS backend implements it so the one-composite limit and the
// referenced-alarm guard apply before anything is deleted.
type alarmBatchDeleter interface {
	DeleteAlarms(ctx context.Context, names []string) error
}

// compositeErr maps a backend error of the composite alarm operations to the
// ValidationError that CloudWatch returns for them.
func compositeErr(err error) error {
	if cerrors.IsInvalidArgument(err) || cerrors.IsFailedPrecondition(err) {
		return newWireError(errValidation, cerrors.Message(err))
	}

	return err
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
	// The suppressor fields are ints in the model. Both periods are required
	// with ActionsSuppressor.
	ActionsSuppressor                string `cbor:"ActionsSuppressor,omitempty"`
	ActionsSuppressorWaitPeriod      *int   `cbor:"ActionsSuppressorWaitPeriod,omitempty"`
	ActionsSuppressorExtensionPeriod *int   `cbor:"ActionsSuppressorExtensionPeriod,omitempty"`
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

		ActionsSuppressor:                in.ActionsSuppressor,
		ActionsSuppressorWaitPeriod:      in.ActionsSuppressorWaitPeriod,
		ActionsSuppressorExtensionPeriod: in.ActionsSuppressorExtensionPeriod,
	})
	if err != nil {
		writeDriverErr(w, compositeErr(err))
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

	StateReasonData                    string     `cbor:"StateReasonData,omitempty"`
	StateTransitionedTimestamp         *time.Time `cbor:"StateTransitionedTimestamp,omitempty"`
	AlarmConfigurationUpdatedTimestamp *time.Time `cbor:"AlarmConfigurationUpdatedTimestamp,omitempty"`
	ActionsSuppressor                  string     `cbor:"ActionsSuppressor,omitempty"`
	ActionsSuppressorWaitPeriod        *int       `cbor:"ActionsSuppressorWaitPeriod,omitempty"`
	ActionsSuppressorExtensionPeriod   *int       `cbor:"ActionsSuppressorExtensionPeriod,omitempty"`
	ActionsSuppressedBy                string     `cbor:"ActionsSuppressedBy,omitempty"`
	ActionsSuppressedReason            string     `cbor:"ActionsSuppressedReason,omitempty"`
}

// wantsAlarmType reports whether a DescribeAlarms request that lists alarmTypes
// asks for the given type. An empty list means metric alarms only, as the
// DescribeAlarms API documents.
func wantsAlarmType(alarmTypes []string, want string) bool {
	if len(alarmTypes) == 0 {
		return want == alarmTypeMetric
	}

	for _, t := range alarmTypes {
		if t == want {
			return true
		}
	}

	return false
}

// alarmQuerier is the AWS backend capability that answers DescribeAlarms with
// every filter applied in the provider.
type alarmQuerier interface {
	QueryAlarms(ctx context.Context, q *mondriver.AlarmQuery) (*mondriver.AlarmQueryResult, error)
}

// queryAlarmsCore runs the DescribeAlarms filters for both protocols. A backend
// without alarmQuerier has metric alarms only.
func (h *Handler) queryAlarmsCore(ctx context.Context, in *describeAlarmsInput) (*mondriver.AlarmQueryResult, error) {
	if qa, ok := h.monitoring.(alarmQuerier); ok {
		res, err := qa.QueryAlarms(ctx, &mondriver.AlarmQuery{
			Names:        in.AlarmNames,
			NamePrefix:   in.AlarmNamePrefix,
			StateValue:   in.StateValue,
			ActionPrefix: in.ActionPrefix,
			AlarmTypes:   in.AlarmTypes,
			ChildrenOf:   in.ChildrenOfAlarmName,
			ParentsOf:    in.ParentsOfAlarmName,
		})
		if err != nil {
			return nil, compositeErr(err)
		}

		return res, nil
	}

	res := &mondriver.AlarmQueryResult{MetricAlarms: []mondriver.AlarmInfo{}}
	if !wantsAlarmType(in.AlarmTypes, alarmTypeMetric) {
		return res, nil
	}

	alarms, err := h.monitoring.DescribeAlarms(ctx, in.AlarmNames)
	if err != nil {
		return nil, err
	}

	for i := range alarms {
		if alarmMatchesFilters(&alarms[i], in) {
			res.MetricAlarms = append(res.MetricAlarms, alarms[i])
		}
	}

	sort.SliceStable(res.MetricAlarms, func(i, j int) bool { return res.MetricAlarms[i].Name < res.MetricAlarms[j].Name })

	return res, nil
}

// alarmsPage is one DescribeAlarms page.
type alarmsPage struct {
	metric    []mondriver.AlarmInfo
	composite []mondriver.CompositeAlarmInfo
	next      string
}

// describeAlarmsPage runs the query and pages metric alarms and then
// composite alarms as one list, so MaxRecords and NextToken cover both.
func (h *Handler) describeAlarmsPage(ctx context.Context, in *describeAlarmsInput) (*alarmsPage, error) {
	res, err := h.queryAlarmsCore(ctx, in)
	if err != nil {
		return nil, err
	}

	size := in.MaxRecords
	if size <= 0 {
		size = maxAlarmPageSize
	}

	offset, err := offsetFromToken(in.NextToken, errInvalidNextToken)
	if err != nil {
		return nil, err
	}

	nm := len(res.MetricAlarms)
	from, to, next := pageWindow(nm+len(res.CompositeAlarms), offset, size)

	page := &alarmsPage{
		metric:    res.MetricAlarms[min(from, nm):min(to, nm)],
		composite: res.CompositeAlarms[max(from-nm, 0):max(to-nm, 0)],
	}

	if next > 0 {
		page.next = encodeOffsetToken(next)
	}

	return page, nil
}

// familyQuery reports whether a request uses ChildrenOfAlarmName or
// ParentsOfAlarmName. Those answer with only the alarm name, ARN, state and
// state timestamp.
func familyQuery(in *describeAlarmsInput) bool {
	return in.ChildrenOfAlarmName != "" || in.ParentsOfAlarmName != ""
}

// familyAlarmCBR is an alarm row of a children or parents query.
type familyAlarmCBR struct {
	AlarmName             string     `cbor:"AlarmName"`
	AlarmArn              string     `cbor:"AlarmArn,omitempty"`
	StateValue            string     `cbor:"StateValue,omitempty"`
	StateUpdatedTimestamp *time.Time `cbor:"StateUpdatedTimestamp,omitempty"`
}

type describeFamilyOutput struct {
	MetricAlarms    []familyAlarmCBR `cbor:"MetricAlarms"`
	CompositeAlarms []familyAlarmCBR `cbor:"CompositeAlarms"`
	NextToken       string           `cbor:"NextToken,omitempty"`
}

// familyRows renders a family page with the brief rows.
func familyRows(page *alarmsPage) (metric, composite []familyAlarmCBR) {
	metric = make([]familyAlarmCBR, 0, len(page.metric))
	for i := range page.metric {
		a := &page.metric[i]
		metric = append(metric, familyAlarmCBR{a.Name, a.AlarmArn, a.State, optTime(a.StateUpdatedTimestamp)})
	}

	composite = make([]familyAlarmCBR, 0, len(page.composite))
	for i := range page.composite {
		c := &page.composite[i]
		composite = append(composite, familyAlarmCBR{c.Name, c.ARN, c.State, optTime(c.StateUpdatedTimestamp)})
	}

	return metric, composite
}

// compositeRows renders composite alarms for the wire.
func compositeRows(alarms []mondriver.CompositeAlarmInfo) []compositeAlarmCBR {
	rows := make([]compositeAlarmCBR, 0, len(alarms))
	for i := range alarms {
		rows = append(rows, toCompositeAlarmCBR(&alarms[i]))
	}

	return rows
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

	c.StateReasonData = a.StateReasonData
	c.StateUpdatedTimestamp = optTime(a.StateUpdatedTimestamp)
	c.StateTransitionedTimestamp = optTime(a.StateTransitionedTimestamp)
	c.AlarmConfigurationUpdatedTimestamp = optTime(a.AlarmConfigurationUpdatedTimestamp)
	c.ActionsSuppressedBy = a.ActionsSuppressedBy
	c.ActionsSuppressedReason = a.ActionsSuppressedReason

	// The periods are only reported with a suppressor, so a zero period still
	// round-trips for Terraform.
	if a.ActionsSuppressor != "" {
		wait, extension := a.ActionsSuppressorWaitPeriod, a.ActionsSuppressorExtensionPeriod
		c.ActionsSuppressor = a.ActionsSuppressor
		c.ActionsSuppressorWaitPeriod = &wait
		c.ActionsSuppressorExtensionPeriod = &extension
	}

	return c
}

// optTime returns a UTC copy of t, or nil for the zero time.
func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	ts := t.UTC()

	return &ts
}

// deleteAlarmsCore runs DeleteAlarms for both protocols. AWS tolerates
// incorrect alarm names: the correctly named alarms are still deleted and no
// ResourceNotFound is returned.
func (h *Handler) deleteAlarmsCore(ctx context.Context, names []string) error {
	if d, ok := h.monitoring.(alarmBatchDeleter); ok {
		return compositeErr(d.DeleteAlarms(ctx, names))
	}

	for _, name := range names {
		if err := h.monitoring.DeleteAlarm(ctx, name); err != nil && !cerrors.IsNotFound(err) {
			return err
		}
	}

	// A name that is not a metric alarm may be a composite alarm.
	if store, ok := h.monitoring.(compositeAlarmStore); ok {
		return compositeErr(store.DeleteCompositeAlarms(ctx, names))
	}

	return nil
}
