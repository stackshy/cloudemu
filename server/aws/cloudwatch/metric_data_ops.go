package cloudwatch

// This file holds the CBOR codecs for GetMetricData and
// DescribeAlarmsForMetric (their logic is in core_metric_data.go), plus
// DescribeAlarmHistory, EnableAlarmActions/DisableAlarmActions, and the
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
	Expression string         `cbor:"Expression,omitempty"`
	ReturnData *bool          `cbor:"ReturnData,omitempty"`
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
	NextToken         string                `cbor:"NextToken,omitempty"`
}

// getMetricData is the CBOR codec for GetMetricData.
func (h *Handler) getMetricData(w http.ResponseWriter, r *http.Request, body []byte) {
	var in getMetricDataInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	res, err := h.getMetricDataCore(r.Context(), &in)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	rows := make([]metricDataResultCBR, 0, len(res.Rows))

	for i := range res.Rows {
		row := &res.Rows[i]
		rows = append(rows, metricDataResultCBR{
			ID: row.ID, Label: row.Label, Timestamps: row.Timestamps, Values: row.Values, StatusCode: row.StatusCode,
		})
	}

	writeCBORResponse(w, getMetricDataOutput{MetricDataResults: rows, NextToken: res.NextToken})
}

// describeAlarmsForMetric is the CBOR codec for DescribeAlarmsForMetric.
func (h *Handler) describeAlarmsForMetric(w http.ResponseWriter, r *http.Request, body []byte) {
	var in describeAlarmsForMetricInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	alarms, err := h.describeAlarmsForMetricCore(r.Context(), &in)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	out := make([]metricAlarmCBR, 0, len(alarms))
	for i := range alarms {
		out = append(out, toMetricAlarmCBR(&alarms[i]))
	}

	writeCBORResponse(w, describeAlarmsOutput{MetricAlarms: out})
}

type describeAlarmHistoryInput struct {
	AlarmName       string     `cbor:"AlarmName,omitempty"`
	HistoryItemType string     `cbor:"HistoryItemType,omitempty"`
	StartDate       *time.Time `cbor:"StartDate,omitempty"`
	EndDate         *time.Time `cbor:"EndDate,omitempty"`
	ScanBy          string     `cbor:"ScanBy,omitempty"`
	MaxRecords      int        `cbor:"MaxRecords,omitempty"`
	NextToken       string     `cbor:"NextToken,omitempty"`
}

// alarmHistoryPageSize is the AWS cap on DescribeAlarmHistory MaxRecords, used as
// the page size when a caller pages but omits MaxRecords.
const alarmHistoryPageSize = 100

// scanByAscending requests oldest-first ordering; the default (and any other
// value) is TimestampDescending, newest-first.
const scanByAscending = "TimestampAscending"

// historyTypeStateUpdate is the default HistoryItemType for a recorded entry.
const historyTypeStateUpdate = "StateUpdate"

type alarmHistoryItemCBR struct {
	AlarmName       string    `cbor:"AlarmName"`
	Timestamp       time.Time `cbor:"Timestamp"`
	HistoryItemType string    `cbor:"HistoryItemType"`
	HistorySummary  string    `cbor:"HistorySummary,omitempty"`
	HistoryData     string    `cbor:"HistoryData,omitempty"`
}

type describeAlarmHistoryOutput struct {
	AlarmHistoryItems []alarmHistoryItemCBR `cbor:"AlarmHistoryItems"`
	NextToken         string                `cbor:"NextToken,omitempty"`
}

// describeAlarmHistory surfaces the transition history recorded internally on
// every alarm state change.
func (h *Handler) describeAlarmHistory(w http.ResponseWriter, r *http.Request, body []byte) {
	var in describeAlarmHistoryInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	// Fetch the full history (newest-first) and apply the request filters here so
	// MaxRecords is honored after HistoryItemType / date-window filtering.
	entries, err := h.monitoring.GetAlarmHistory(r.Context(), in.AlarmName, 0)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	items, next, err := pageAlarmHistory(filterAlarmHistory(entries, &in), &in)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	resp := describeAlarmHistoryOutput{AlarmHistoryItems: items}
	if next != "" {
		resp.NextToken = next
	}

	writeCBORResponse(w, resp)
}

// filterAlarmHistory applies the DescribeAlarmHistory request filters to the
// newest-first entries — HistoryItemType, the StartDate/EndDate window, then
// ScanBy ordering — returning every match in wire order. Paging is applied
// separately by pageAlarmHistory so entries past the first page stay reachable.
func filterAlarmHistory(entries []mondriver.AlarmHistoryEntry, in *describeAlarmHistoryInput) []alarmHistoryItemCBR {
	start := timeOrZero(in.StartDate)
	end := timeOrZero(in.EndDate)

	kept := make([]mondriver.AlarmHistoryEntry, 0, len(entries))

	for i := range entries {
		if historyEntryMatches(&entries[i], in, start, end) {
			kept = append(kept, entries[i])
		}
	}

	if in.ScanBy == scanByAscending {
		reverseHistory(kept)
	}

	return historyItemsToCBR(kept)
}

// pageAlarmHistory returns the requested page of history items and the NextToken
// for the following page (empty on the last page). Paging by offset keeps every
// entry retrievable instead of dropping the tail past MaxRecords. A bad
// NextToken returns InvalidNextToken, as the API reference documents.
func pageAlarmHistory(items []alarmHistoryItemCBR, in *describeAlarmHistoryInput) ([]alarmHistoryItemCBR, string, error) {
	size := in.MaxRecords
	if size <= 0 {
		size = alarmHistoryPageSize
	}

	offset, err := offsetFromToken(in.NextToken, errInvalidNextToken)
	if err != nil {
		return nil, "", err
	}

	from, to, nextOff := pageWindow(len(items), offset, size)
	if nextOff > 0 {
		return items[from:to], encodeOffsetToken(nextOff), nil
	}

	return items[from:to], "", nil
}

// historyEntryMatches reports whether an entry passes the HistoryItemType and
// StartDate/EndDate filters of a DescribeAlarmHistory request.
func historyEntryMatches(e *mondriver.AlarmHistoryEntry, in *describeAlarmHistoryInput, start, end time.Time) bool {
	if in.HistoryItemType != "" && historyItemType(e) != in.HistoryItemType {
		return false
	}

	if !start.IsZero() && e.Timestamp.Before(start) {
		return false
	}

	if !end.IsZero() && e.Timestamp.After(end) {
		return false
	}

	return true
}

// reverseHistory reverses the entries in place (newest-first to oldest-first).
func reverseHistory(entries []mondriver.AlarmHistoryEntry) {
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
}

func historyItemsToCBR(entries []mondriver.AlarmHistoryEntry) []alarmHistoryItemCBR {
	out := make([]alarmHistoryItemCBR, 0, len(entries))
	for i := range entries {
		out = append(out, alarmHistoryItemCBR{
			AlarmName:       entries[i].AlarmName,
			Timestamp:       entries[i].Timestamp.UTC(),
			HistoryItemType: historyItemType(&entries[i]),
			HistorySummary:  entries[i].Reason,
			HistoryData:     alarmHistoryData(&entries[i]),
		})
	}

	return out
}

// historyItemType returns the entry's classification, defaulting to StateUpdate
// for entries recorded before the field existed.
func historyItemType(e *mondriver.AlarmHistoryEntry) string {
	if e.HistoryItemType == "" {
		return historyTypeStateUpdate
	}

	return e.HistoryItemType
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

	if name, ok := metricStreamNameFromARN(in.ResourceARN); ok {
		tagger, ok := h.monitoring.(metricStreamTagger)
		if !ok {
			writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "tagging not supported")
			return
		}

		if err := tagger.AddMetricStreamTags(r.Context(), name, tagsToMap(in.Tags)); err != nil {
			writeDriverErr(w, err)
			return
		}

		writeCBORResponse(w, struct{}{})

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

	if name, ok := metricStreamNameFromARN(in.ResourceARN); ok {
		tagger, ok := h.monitoring.(metricStreamTagger)
		if !ok {
			writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "tagging not supported")
			return
		}

		if err := tagger.RemoveMetricStreamTags(r.Context(), name, in.TagKeys); err != nil {
			writeDriverErr(w, err)
			return
		}

		writeCBORResponse(w, struct{}{})

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

	if name, ok := metricStreamNameFromARN(in.ResourceARN); ok {
		tagger, ok := h.monitoring.(metricStreamTagger)
		if !ok {
			writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "tagging not supported")
			return
		}

		tags, err := tagger.MetricStreamTags(r.Context(), name)
		if err != nil {
			writeDriverErr(w, err)
			return
		}

		writeCBORResponse(w, listTagsForResourceOutput{Tags: mapToTags(tags)})

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
