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
	Expression string         `cbor:"Expression,omitempty"`
	ReturnData *bool          `cbor:"ReturnData,omitempty"`
}

type getMetricDataInput struct {
	MetricDataQueries []metricDataQueryCBR `cbor:"MetricDataQueries"`
	StartTime         *time.Time           `cbor:"StartTime,omitempty"`
	EndTime           *time.Time           `cbor:"EndTime,omitempty"`
	MaxDatapoints     int                  `cbor:"MaxDatapoints,omitempty"`
	NextToken         string               `cbor:"NextToken,omitempty"`
}

// defaultMaxDatapoints is the AWS GetMetricData datapoint budget per page when a
// caller omits MaxDatapoints; results past it spill onto a NextToken page.
const defaultMaxDatapoints = 100800

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

	eval := newMathEvaluator(r.Context(), h.monitoring, in.MetricDataQueries, start, end)

	out := make([]metricDataResultCBR, 0, len(in.MetricDataQueries))

	for i := range in.MetricDataQueries {
		q := in.MetricDataQueries[i]

		series, err := eval.resolve(q.ID)
		if err != nil {
			writeDriverErr(w, err)
			return
		}

		// A query with ReturnData explicitly false is an input to other queries
		// only (e.g. a raw metric feeding a math expression) and is not emitted
		// as a data row. When omitted, ReturnData defaults to true.
		if q.ReturnData != nil && !*q.ReturnData {
			continue
		}

		out = append(out, buildMetricDataResult(q, series))
	}

	rows, next := pageMetricData(out, &in)

	resp := getMetricDataOutput{MetricDataResults: rows}
	if next != "" {
		resp.NextToken = next
	}

	writeCBORResponse(w, resp)
}

// pageMetricData returns the leading result rows that fit the MaxDatapoints
// budget from the NextToken offset, plus the token for the next page (empty on
// the last row). At least one row is returned so a row larger than the budget
// still makes progress instead of stalling the paginator.
func pageMetricData(rows []metricDataResultCBR, in *getMetricDataInput) (page []metricDataResultCBR, next string) {
	budget := in.MaxDatapoints
	if budget <= 0 {
		budget = defaultMaxDatapoints
	}

	start := decodeOffsetToken(in.NextToken)
	if start > len(rows) {
		start = len(rows)
	}

	used, end := 0, start
	for end < len(rows) {
		n := len(rows[end].Values)
		if end > start && used+n > budget {
			break
		}

		used += n
		end++
	}

	if end < len(rows) {
		return rows[start:end], encodeOffsetToken(end)
	}

	return rows[start:end], ""
}

func buildMetricDataResult(q metricDataQueryCBR, series mathSeries) metricDataResultCBR {
	row := metricDataResultCBR{ID: q.ID, Label: metricDataLabel(q), StatusCode: statusCodeComplete}

	row.Timestamps = make([]time.Time, len(series.timestamps))
	for i := range series.timestamps {
		row.Timestamps[i] = series.timestamps[i].UTC()
	}

	row.Values = series.values

	return row
}

// metricDataLabel resolves the response label for a query: the caller-supplied
// Label, else the metric name for a MetricStat query, else the query Id.
func metricDataLabel(q metricDataQueryCBR) string {
	if q.Label != "" {
		return q.Label
	}

	if q.MetricStat != nil {
		return q.MetricStat.Metric.MetricName
	}

	return q.ID
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

	items, next := pageAlarmHistory(filterAlarmHistory(entries, &in), &in)

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
// entry retrievable instead of dropping the tail past MaxRecords.
func pageAlarmHistory(items []alarmHistoryItemCBR, in *describeAlarmHistoryInput) (page []alarmHistoryItemCBR, next string) {
	size := in.MaxRecords
	if size <= 0 {
		size = alarmHistoryPageSize
	}

	from, to, nextOff := pageWindow(len(items), decodeOffsetToken(in.NextToken), size)
	if nextOff > 0 {
		return items[from:to], encodeOffsetToken(nextOff)
	}

	return items[from:to], ""
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
