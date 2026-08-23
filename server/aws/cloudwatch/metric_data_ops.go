package cloudwatch

// This file implements the CloudWatch read/tag operations added for wire
// fidelity: GetMetricData (the modern primary read API), DescribeAlarmHistory,
// DescribeAlarmsForMetric, EnableAlarmActions/DisableAlarmActions, and the
// alarm tagging operations. The alarm-action and tag operations use AWS-local
// optional interfaces so the shared Monitoring interface is unchanged.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const statusCodeComplete = "Complete"

// alarmActionsToggler is the AWS-local capability behind
// EnableAlarmActions / DisableAlarmActions.
type alarmActionsToggler interface {
	SetAlarmActionsEnabled(ctx context.Context, names []string, enabled bool) error
}

// alarmTagger is the AWS-local capability behind the alarm tag operations.
type alarmTagger interface {
	AddAlarmTags(ctx context.Context, alarmName string, tags map[string]string) error
	RemoveAlarmTags(ctx context.Context, alarmName string, keys []string) error
	AlarmTags(ctx context.Context, alarmName string) (map[string]string, error)
}

type metricCBRRef struct {
	Namespace  string         `cbor:"Namespace"`
	MetricName string         `cbor:"MetricName"`
	Dimensions []dimensionCBR `cbor:"Dimensions,omitempty"`
}

type metricStatCBR struct {
	Metric metricCBRRef `cbor:"Metric"`
	Period int          `cbor:"Period"`
	Stat   string       `cbor:"Stat"`
	Unit   string       `cbor:"Unit,omitempty"`
}

type metricDataQueryCBR struct {
	ID         string         `cbor:"Id"`
	Label      string         `cbor:"Label,omitempty"`
	MetricStat *metricStatCBR `cbor:"MetricStat,omitempty"`
	ReturnData *bool          `cbor:"ReturnData,omitempty"`
}

type getMetricDataInput struct {
	MetricDataQueries []metricDataQueryCBR `cbor:"MetricDataQueries"`
	StartTime         *time.Time           `cbor:"StartTime,omitempty"`
	EndTime           *time.Time           `cbor:"EndTime,omitempty"`
}

type metricDataResultCBR struct {
	ID         string      `cbor:"Id"`
	Label      string      `cbor:"Label,omitempty"`
	Timestamps []time.Time `cbor:"Timestamps,omitempty"`
	Values     []float64   `cbor:"Values,omitempty"`
	StatusCode string      `cbor:"StatusCode,omitempty"`
}

type getMetricDataOutput struct {
	MetricDataResults []metricDataResultCBR `cbor:"MetricDataResults"`
}

// getMetricData implements the modern GetMetricData read API used by SDK v2,
// dashboards, and Grafana.
func (h *Handler) getMetricData(w http.ResponseWriter, r *http.Request, body []byte) {
	var in getMetricDataInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	start := timeOrZero(in.StartTime)
	end := timeOrZero(in.EndTime)

	out := make([]metricDataResultCBR, 0, len(in.MetricDataQueries))

	for _, q := range in.MetricDataQueries {
		if q.MetricStat == nil {
			continue // math-expression queries are not modeled
		}

		res, err := h.monitoring.GetMetricData(r.Context(), mondriver.GetMetricInput{
			Namespace:  q.MetricStat.Metric.Namespace,
			MetricName: q.MetricStat.Metric.MetricName,
			Dimensions: toDimensionMap(q.MetricStat.Metric.Dimensions),
			StartTime:  start,
			EndTime:    end,
			Period:     q.MetricStat.Period,
			Stat:       q.MetricStat.Stat,
		})
		if err != nil {
			writeDriverErr(w, err)
			return
		}

		out = append(out, buildMetricDataResult(q, res))
	}

	writeCBORResponse(w, getMetricDataOutput{MetricDataResults: out})
}

func buildMetricDataResult(q metricDataQueryCBR, res *mondriver.MetricDataResult) metricDataResultCBR {
	label := q.Label
	if label == "" {
		label = q.MetricStat.Metric.MetricName
	}

	row := metricDataResultCBR{ID: q.ID, Label: label, StatusCode: statusCodeComplete}

	if res != nil {
		row.Timestamps = make([]time.Time, len(res.Timestamps))
		for i := range res.Timestamps {
			row.Timestamps[i] = res.Timestamps[i].UTC()
		}

		row.Values = res.Values
	}

	return row
}

type describeAlarmsForMetricInput struct {
	Namespace  string         `cbor:"Namespace"`
	MetricName string         `cbor:"MetricName"`
	Dimensions []dimensionCBR `cbor:"Dimensions,omitempty"`
	Statistic  string         `cbor:"Statistic,omitempty"`
	Period     int            `cbor:"Period,omitempty"`
}

// describeAlarmsForMetric returns the alarms configured against a specific
// metric, filtered server-side by namespace, metric name, and dimensions.
func (h *Handler) describeAlarmsForMetric(w http.ResponseWriter, r *http.Request, body []byte) {
	var in describeAlarmsForMetricInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	alarms, err := h.monitoring.DescribeAlarms(r.Context(), nil)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	wantDims := toDimensionMap(in.Dimensions)
	out := make([]metricAlarmCBR, 0, len(alarms))

	for i := range alarms {
		if alarmMatchesMetric(&alarms[i], &in, wantDims) {
			out = append(out, toMetricAlarmCBR(&alarms[i]))
		}
	}

	writeCBORResponse(w, describeAlarmsOutput{MetricAlarms: out})
}

// alarmMatchesMetric reports whether an alarm targets the metric described by a
// DescribeAlarmsForMetric request.
func alarmMatchesMetric(a *mondriver.AlarmInfo, in *describeAlarmsForMetricInput, wantDims map[string]string) bool {
	if a.Namespace != in.Namespace || a.MetricName != in.MetricName {
		return false
	}

	if in.Period != 0 && a.Period != in.Period {
		return false
	}

	if in.Statistic != "" && a.Statistic != in.Statistic {
		return false
	}

	return dimensionsEqual(a.Dimensions, wantDims)
}

func dimensionsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if b[k] != v {
			return false
		}
	}

	return true
}

type describeAlarmHistoryInput struct {
	AlarmName       string `cbor:"AlarmName,omitempty"`
	HistoryItemType string `cbor:"HistoryItemType,omitempty"`
	MaxRecords      int    `cbor:"MaxRecords,omitempty"`
}

type alarmHistoryItemCBR struct {
	AlarmName       string    `cbor:"AlarmName"`
	Timestamp       time.Time `cbor:"Timestamp"`
	HistoryItemType string    `cbor:"HistoryItemType"`
	HistorySummary  string    `cbor:"HistorySummary,omitempty"`
	HistoryData     string    `cbor:"HistoryData,omitempty"`
}

type describeAlarmHistoryOutput struct {
	AlarmHistoryItems []alarmHistoryItemCBR `cbor:"AlarmHistoryItems"`
}

// describeAlarmHistory surfaces the transition history recorded internally on
// every alarm state change.
func (h *Handler) describeAlarmHistory(w http.ResponseWriter, r *http.Request, body []byte) {
	var in describeAlarmHistoryInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	entries, err := h.monitoring.GetAlarmHistory(r.Context(), in.AlarmName, in.MaxRecords)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	out := make([]alarmHistoryItemCBR, 0, len(entries))
	for i := range entries {
		out = append(out, alarmHistoryItemCBR{
			AlarmName:       entries[i].AlarmName,
			Timestamp:       entries[i].Timestamp.UTC(),
			HistoryItemType: "StateUpdate",
			HistorySummary:  entries[i].Reason,
			HistoryData:     alarmHistoryData(&entries[i]),
		})
	}

	writeCBORResponse(w, describeAlarmHistoryOutput{AlarmHistoryItems: out})
}

func alarmHistoryData(e *mondriver.AlarmHistoryEntry) string {
	return `{"oldState":{"stateValue":"` + e.OldState + `"},"newState":{"stateValue":"` + e.NewState + `"}}`
}

type alarmNamesInput struct {
	AlarmNames []string `cbor:"AlarmNames,omitempty"`
}

// setAlarmActionsEnabled backs EnableAlarmActions / DisableAlarmActions.
func (h *Handler) setAlarmActionsEnabled(w http.ResponseWriter, r *http.Request, body []byte, enabled bool) {
	var in alarmNamesInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	toggler, ok := h.monitoring.(alarmActionsToggler)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "alarm actions toggle not supported")
		return
	}

	if err := toggler.SetAlarmActionsEnabled(r.Context(), in.AlarmNames, enabled); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

type tagResourceInput struct {
	ResourceARN string   `cbor:"ResourceARN"`
	Tags        []tagCBR `cbor:"Tags,omitempty"`
}

type untagResourceInput struct {
	ResourceARN string   `cbor:"ResourceARN"`
	TagKeys     []string `cbor:"TagKeys,omitempty"`
}

type listTagsForResourceInput struct {
	ResourceARN string `cbor:"ResourceARN"`
}

type listTagsForResourceOutput struct {
	Tags []tagCBR `cbor:"Tags"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request, body []byte) {
	var in tagResourceInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	tagger, ok := h.monitoring.(alarmTagger)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "tagging not supported")
		return
	}

	if err := tagger.AddAlarmTags(r.Context(), alarmNameFromARN(in.ResourceARN), tagsToMap(in.Tags)); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request, body []byte) {
	var in untagResourceInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	tagger, ok := h.monitoring.(alarmTagger)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "tagging not supported")
		return
	}

	if err := tagger.RemoveAlarmTags(r.Context(), alarmNameFromARN(in.ResourceARN), in.TagKeys); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request, body []byte) {
	var in listTagsForResourceInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	tagger, ok := h.monitoring.(alarmTagger)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "tagging not supported")
		return
	}

	tags, err := tagger.AlarmTags(r.Context(), alarmNameFromARN(in.ResourceARN))
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, listTagsForResourceOutput{Tags: mapToTags(tags)})
}

// alarmNameFromARN extracts the alarm name from a CloudWatch alarm ARN of the
// form arn:aws:cloudwatch:region:account:alarm:NAME. A bare name is returned
// unchanged.
func alarmNameFromARN(arn string) string {
	if i := strings.Index(arn, ":alarm:"); i >= 0 {
		return arn[i+len(":alarm:"):]
	}

	return arn
}

func mapToTags(tags map[string]string) []tagCBR {
	out := make([]tagCBR, 0, len(tags))
	for k, v := range tags {
		out = append(out, tagCBR{Key: k, Value: v})
	}

	return out
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}

	return *t
}
