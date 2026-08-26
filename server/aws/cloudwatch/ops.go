package cloudwatch

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const (
	statSum           = "Sum"
	statMinimum       = "Minimum"
	statMaximum       = "Maximum"
	statSampleCount   = "SampleCount"
	statAverage       = "Average"
	defaultMetricUnit = "Count"
)

// putMetricDataInput mirrors the AWS wire shape for the operation. Field
// names are CBOR-tagged so the decoder matches the JSON-ish names the SDK
// sends (CBOR preserves string keys).
type putMetricDataInput struct {
	Namespace  string              `cbor:"Namespace"`
	MetricData []putMetricDatumCBR `cbor:"MetricData"`
}

type statisticSetCBR struct {
	SampleCount float64 `cbor:"SampleCount"`
	Sum         float64 `cbor:"Sum"`
	Minimum     float64 `cbor:"Minimum"`
	Maximum     float64 `cbor:"Maximum"`
}

type putMetricDatumCBR struct {
	MetricName      string           `cbor:"MetricName"`
	Value           float64          `cbor:"Value"`
	Unit            string           `cbor:"Unit,omitempty"`
	Timestamp       *time.Time       `cbor:"Timestamp,omitempty"`
	Dimensions      []dimensionCBR   `cbor:"Dimensions,omitempty"`
	StatisticValues *statisticSetCBR `cbor:"StatisticValues,omitempty"`
	Values          []float64        `cbor:"Values,omitempty"`
	Counts          []float64        `cbor:"Counts,omitempty"`
}

type dimensionCBR struct {
	Name  string `cbor:"Name"`
	Value string `cbor:"Value"`
}

func (h *Handler) putMetricData(w http.ResponseWriter, r *http.Request, body []byte) {
	var in putMetricDataInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	data := make([]mondriver.MetricDatum, 0, len(in.MetricData))

	for i := range in.MetricData {
		d := &in.MetricData[i]
		// AWS defaults an omitted timestamp to request-receipt time; storing the
		// Go zero value instead would make the datapoint unqueryable and leave
		// alarms stuck in INSUFFICIENT_DATA.
		ts := time.Now().UTC()
		if d.Timestamp != nil {
			ts = *d.Timestamp
		}

		datum := mondriver.MetricDatum{
			Namespace:  in.Namespace,
			MetricName: d.MetricName,
			Value:      d.Value,
			Unit:       d.Unit,
			Dimensions: toDimensionMap(d.Dimensions),
			Timestamp:  ts,
			Values:     d.Values,
			Counts:     d.Counts,
		}

		if d.StatisticValues != nil {
			datum.StatisticValues = &mondriver.StatisticSet{
				SampleCount: d.StatisticValues.SampleCount,
				Sum:         d.StatisticValues.Sum,
				Minimum:     d.StatisticValues.Minimum,
				Maximum:     d.StatisticValues.Maximum,
			}
		}

		data = append(data, datum)
	}

	if err := h.monitoring.PutMetricData(r.Context(), data); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

// getMetricStatisticsInput mirrors the SDK's GetMetricStatistics request.
type getMetricStatisticsInput struct {
	Namespace  string         `cbor:"Namespace"`
	MetricName string         `cbor:"MetricName"`
	StartTime  *time.Time     `cbor:"StartTime,omitempty"`
	EndTime    *time.Time     `cbor:"EndTime,omitempty"`
	Period     int            `cbor:"Period"`
	Statistics []string       `cbor:"Statistics,omitempty"`
	Dimensions []dimensionCBR `cbor:"Dimensions,omitempty"`
}

type datapointCBR struct {
	Timestamp   time.Time `cbor:"Timestamp"`
	SampleCount float64   `cbor:"SampleCount,omitempty"`
	Average     float64   `cbor:"Average,omitempty"`
	Sum         float64   `cbor:"Sum,omitempty"`
	Minimum     float64   `cbor:"Minimum,omitempty"`
	Maximum     float64   `cbor:"Maximum,omitempty"`
	Unit        string    `cbor:"Unit,omitempty"`
}

type getMetricStatisticsOutput struct {
	Label      string         `cbor:"Label"`
	Datapoints []datapointCBR `cbor:"Datapoints"`
}

func (h *Handler) getMetricStatistics(w http.ResponseWriter, r *http.Request, body []byte) {
	var in getMetricStatisticsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	// Every requested statistic is returned on each datapoint. Callers routinely
	// ask for several (e.g. Average, Sum, Maximum) in one call and expect all of
	// them populated, so fall back to Average only when none was requested.
	stats := in.Statistics
	if len(stats) == 0 {
		stats = []string{statAverage}
	}

	start := time.Time{}
	if in.StartTime != nil {
		start = *in.StartTime
	}

	end := time.Time{}
	if in.EndTime != nil {
		end = *in.EndTime
	}

	if h.ipam != nil && in.Namespace == netdriver.IpamMetricNamespace {
		h.getIpamMetricStatistics(w, r, in.MetricName, toDimensionMap(in.Dimensions), stats)
		return
	}

	dims := toDimensionMap(in.Dimensions)
	acc := newDatapointAcc()

	for _, stat := range stats {
		result, err := h.monitoring.GetMetricData(r.Context(), mondriver.GetMetricInput{
			Namespace:  in.Namespace,
			MetricName: in.MetricName,
			Dimensions: dims,
			StartTime:  start,
			EndTime:    end,
			Period:     in.Period,
			Stat:       stat,
		})
		if err != nil {
			writeDriverErr(w, err)
			return
		}

		acc.add(result, stat)
	}

	writeCBORResponse(w, getMetricStatisticsOutput{
		Label:      in.MetricName,
		Datapoints: acc.datapoints(),
	})
}

// getIpamMetricStatistics returns a single datapoint for a derived AWS/IPAM
// metric, matched by name and (if supplied) dimensions, populating every
// requested statistic.
func (h *Handler) getIpamMetricStatistics(
	w http.ResponseWriter, r *http.Request, name string, dims map[string]string, stats []string,
) {
	for _, mtr := range h.ipam.IpamMetrics(r.Context()) {
		if mtr.MetricName != name || !dimensionsMatch(mtr.Dimensions, dims) {
			continue
		}

		dp := datapointCBR{Timestamp: time.Unix(0, 0).UTC(), Unit: mtr.Unit}
		for _, stat := range stats {
			setDatapointStat(&dp, stat, mtr.Value)
		}

		writeCBORResponse(w, getMetricStatisticsOutput{Label: name, Datapoints: []datapointCBR{dp}})

		return
	}

	writeCBORResponse(w, getMetricStatisticsOutput{Label: name, Datapoints: nil})
}

// datapointAcc merges per-statistic MetricDataResults into one datapoint per
// timestamp, so a multi-statistic GetMetricStatistics call returns each
// datapoint with all requested statistics populated.
type datapointAcc struct {
	byTS  map[int64]*datapointCBR
	order []int64
	unit  string
}

func newDatapointAcc() *datapointAcc {
	return &datapointAcc{byTS: map[int64]*datapointCBR{}}
}

// add folds one statistic's result into the accumulator.
func (a *datapointAcc) add(res *mondriver.MetricDataResult, stat string) {
	if res == nil {
		return
	}

	if a.unit == "" {
		a.unit = res.Unit
	}

	for i := range res.Timestamps {
		ts := res.Timestamps[i].UTC()
		key := ts.UnixNano()

		dp, ok := a.byTS[key]
		if !ok {
			dp = &datapointCBR{Timestamp: ts}
			a.byTS[key] = dp
			a.order = append(a.order, key)
		}

		setDatapointStat(dp, stat, res.Values[i])
	}
}

// datapoints returns the merged datapoints in ascending timestamp order, each
// stamped with the resolved unit.
func (a *datapointAcc) datapoints() []datapointCBR {
	unit := a.unit
	if unit == "" {
		unit = defaultMetricUnit
	}

	sort.Slice(a.order, func(i, j int) bool { return a.order[i] < a.order[j] })

	out := make([]datapointCBR, 0, len(a.order))

	for _, key := range a.order {
		dp := a.byTS[key]
		dp.Unit = unit
		out = append(out, *dp)
	}

	return out
}

// dimensionsMatch reports whether every requested dimension is present in have.
func dimensionsMatch(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}

	return true
}

func setDatapointStat(dp *datapointCBR, stat string, value float64) {
	switch stat {
	case statSum:
		dp.Sum = value
	case statMinimum:
		dp.Minimum = value
	case statMaximum:
		dp.Maximum = value
	case statSampleCount:
		dp.SampleCount = value
	default:
		dp.Average = value
	}
}

type dimensionFilterCBR struct {
	Name  string `cbor:"Name"`
	Value string `cbor:"Value,omitempty"`
}

type listMetricsInput struct {
	Namespace  string               `cbor:"Namespace,omitempty"`
	MetricName string               `cbor:"MetricName,omitempty"`
	Dimensions []dimensionFilterCBR `cbor:"Dimensions,omitempty"`
	NextToken  string               `cbor:"NextToken,omitempty"`
}

type metricCBR struct {
	Namespace  string         `cbor:"Namespace"`
	MetricName string         `cbor:"MetricName"`
	Dimensions []dimensionCBR `cbor:"Dimensions,omitempty"`
}

type listMetricsOutput struct {
	Metrics   []metricCBR `cbor:"Metrics"`
	NextToken string      `cbor:"NextToken,omitempty"`
}

// listMetricsPageSize is the number of metrics AWS returns per ListMetrics page.
const listMetricsPageSize = 500

func (h *Handler) listMetrics(w http.ResponseWriter, r *http.Request, body []byte) {
	var in listMetricsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	// An exact AWS/IPAM request returns only the synthetic IPAM metrics.
	if h.ipam != nil && in.Namespace == netdriver.IpamMetricNamespace {
		writeCBORResponse(w, listMetricsOutput{Metrics: h.ipamMetricRows(r)})
		return
	}

	rows, err := h.allMetricRows(r)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	if h.ipam != nil && in.Namespace == "" {
		rows = append(rows, h.ipamMetricRows(r)...)
	}

	matched := filterMetricRows(rows, in)
	sort.SliceStable(matched, func(i, j int) bool {
		return metricRowKey(matched[i]) < metricRowKey(matched[j])
	})

	from, to, next := pageWindow(len(matched), decodeOffsetToken(in.NextToken), listMetricsPageSize)

	resp := listMetricsOutput{Metrics: matched[from:to]}
	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	writeCBORResponse(w, resp)
}

// metricRowKey renders a metric row as a stable sort key over namespace, metric
// name, then its sorted dimension pairs — a deterministic order for paging.
func metricRowKey(m metricCBR) string {
	parts := make([]string, 0, len(m.Dimensions))
	for _, d := range m.Dimensions {
		parts = append(parts, d.Name+"="+d.Value)
	}

	sort.Strings(parts)

	return m.Namespace + "\x00" + m.MetricName + "\x00" + strings.Join(parts, ",")
}

// filterMetricRows applies the ListMetrics namespace / metric-name / dimension
// filters AWS honors server-side.
func filterMetricRows(rows []metricCBR, in listMetricsInput) []metricCBR {
	out := make([]metricCBR, 0, len(rows))

	for _, row := range rows {
		if in.Namespace != "" && row.Namespace != in.Namespace {
			continue
		}

		if in.MetricName != "" && row.MetricName != in.MetricName {
			continue
		}

		if !rowMatchesDimensionFilters(row, in.Dimensions) {
			continue
		}

		out = append(out, row)
	}

	return out
}

// rowMatchesDimensionFilters reports whether a metric row satisfies every
// DimensionFilter: a filter with a Value requires an exact match, a filter with
// only a Name requires that dimension to be present.
func rowMatchesDimensionFilters(row metricCBR, filters []dimensionFilterCBR) bool {
	if len(filters) == 0 {
		return true
	}

	have := make(map[string]string, len(row.Dimensions))
	for _, d := range row.Dimensions {
		have[d.Name] = d.Value
	}

	for _, f := range filters {
		v, ok := have[f.Name]
		if !ok {
			return false
		}

		if f.Value != "" && v != f.Value {
			return false
		}
	}

	return true
}

// detailedMetricLister is the AWS-local capability that enumerates every metric
// with its namespace, backing a namespace-less ListMetrics. The shared
// Monitoring interface only lists names within a single namespace.
type detailedMetricLister interface {
	ListMetricsDetailed(ctx context.Context) ([]mondriver.MetricIdentifier, error)
}

// allMetricRows lists every real metric tagged with its true namespace, using
// the detailed lister when available and otherwise degrading to the
// empty-namespace name list.
func (h *Handler) allMetricRows(r *http.Request) ([]metricCBR, error) {
	if dl, ok := h.monitoring.(detailedMetricLister); ok {
		ids, err := dl.ListMetricsDetailed(r.Context())
		if err != nil {
			return nil, err
		}

		out := make([]metricCBR, 0, len(ids))
		for _, id := range ids {
			out = append(out, metricCBR{
				Namespace:  id.Namespace,
				MetricName: id.MetricName,
				Dimensions: dimsToCBR(id.Dimensions),
			})
		}

		return out, nil
	}

	names, err := h.monitoring.ListMetrics(r.Context(), "")
	if err != nil {
		return nil, err
	}

	out := make([]metricCBR, 0, len(names))
	for _, name := range names {
		out = append(out, metricCBR{MetricName: name})
	}

	return out, nil
}

// ipamMetricRows returns the derived AWS/IPAM metrics with their dimensions.
func (h *Handler) ipamMetricRows(r *http.Request) []metricCBR {
	metrics := h.ipam.IpamMetrics(r.Context())
	out := make([]metricCBR, 0, len(metrics))

	for _, mtr := range metrics {
		dims := make([]dimensionCBR, 0, len(mtr.Dimensions))
		for k, v := range mtr.Dimensions {
			dims = append(dims, dimensionCBR{Name: k, Value: v})
		}

		out = append(out, metricCBR{Namespace: netdriver.IpamMetricNamespace, MetricName: mtr.MetricName, Dimensions: dims})
	}

	return out
}

type tagCBR struct {
	Key   string `cbor:"Key"`
	Value string `cbor:"Value"`
}

type putMetricAlarmInput struct {
	AlarmName               string         `cbor:"AlarmName"`
	AlarmDescription        string         `cbor:"AlarmDescription,omitempty"`
	Namespace               string         `cbor:"Namespace"`
	MetricName              string         `cbor:"MetricName"`
	ComparisonOperator      string         `cbor:"ComparisonOperator"`
	Threshold               float64        `cbor:"Threshold"`
	Period                  int            `cbor:"Period"`
	EvaluationPeriods       int            `cbor:"EvaluationPeriods"`
	DatapointsToAlarm       int            `cbor:"DatapointsToAlarm,omitempty"`
	Statistic               string         `cbor:"Statistic,omitempty"`
	ExtendedStatistic       string         `cbor:"ExtendedStatistic,omitempty"`
	Unit                    string         `cbor:"Unit,omitempty"`
	TreatMissingData        string         `cbor:"TreatMissingData,omitempty"`
	Dimensions              []dimensionCBR `cbor:"Dimensions,omitempty"`
	AlarmActions            []string       `cbor:"AlarmActions,omitempty"`
	OKActions               []string       `cbor:"OKActions,omitempty"`
	InsufficientDataActions []string       `cbor:"InsufficientDataActions,omitempty"`
	ActionsEnabled          *bool          `cbor:"ActionsEnabled,omitempty"`
	Tags                    []tagCBR       `cbor:"Tags,omitempty"`
}

// validComparisonOperators is the closed CloudWatch ComparisonOperator enum. A
// value outside this set is rejected with a ValidationError, matching AWS,
// rather than silently stored (which would leave the alarm unable to fire).
//
//nolint:gochecknoglobals // fixed lookup table for a closed enum.
var validComparisonOperators = map[string]bool{
	"GreaterThanOrEqualToThreshold":            true,
	"GreaterThanThreshold":                     true,
	"LessThanThreshold":                        true,
	"LessThanOrEqualToThreshold":               true,
	"LessThanLowerOrGreaterThanUpperThreshold": true,
	"LessThanLowerThreshold":                   true,
	"GreaterThanUpperThreshold":                true,
}

// comparisonOperatorValid reports whether op is empty (unset — AWS allows metric-
// math/anomaly alarms to omit it) or a member of the closed enum.
func comparisonOperatorValid(op string) bool {
	return op == "" || validComparisonOperators[op]
}

func (h *Handler) putMetricAlarm(w http.ResponseWriter, r *http.Request, body []byte) {
	var in putMetricAlarmInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	if !comparisonOperatorValid(in.ComparisonOperator) {
		writeCBORError(w, http.StatusBadRequest, "ValidationError",
			"Invalid ComparisonOperator: "+in.ComparisonOperator)
		return
	}

	cfg := mondriver.AlarmConfig{
		Name:                    in.AlarmName,
		Namespace:               in.Namespace,
		MetricName:              in.MetricName,
		Dimensions:              toDimensionMap(in.Dimensions),
		ComparisonOperator:      in.ComparisonOperator,
		Threshold:               in.Threshold,
		Period:                  in.Period,
		EvaluationPeriods:       in.EvaluationPeriods,
		DatapointsToAlarm:       in.DatapointsToAlarm,
		Stat:                    in.Statistic,
		ExtendedStatistic:       in.ExtendedStatistic,
		Unit:                    in.Unit,
		TreatMissingData:        in.TreatMissingData,
		AlarmActions:            in.AlarmActions,
		OKActions:               in.OKActions,
		InsufficientDataActions: in.InsufficientDataActions,
		AlarmDescription:        in.AlarmDescription,
		ActionsEnabled:          in.ActionsEnabled,
		Tags:                    tagsToMap(in.Tags),
	}

	if err := h.monitoring.CreateAlarm(r.Context(), cfg); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

func tagsToMap(tags []tagCBR) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

type describeAlarmsInput struct {
	AlarmNames      []string `cbor:"AlarmNames,omitempty"`
	AlarmNamePrefix string   `cbor:"AlarmNamePrefix,omitempty"`
	StateValue      string   `cbor:"StateValue,omitempty"`
	ActionPrefix    string   `cbor:"ActionPrefix,omitempty"`
	MaxRecords      int      `cbor:"MaxRecords,omitempty"`
	NextToken       string   `cbor:"NextToken,omitempty"`
}

// maxAlarmPageSize is the AWS cap on DescribeAlarms MaxRecords, used as the page
// size when a caller pages with a NextToken but omits MaxRecords.
const maxAlarmPageSize = 100

type metricAlarmCBR struct {
	AlarmName               string         `cbor:"AlarmName"`
	AlarmArn                string         `cbor:"AlarmArn,omitempty"`
	AlarmDescription        string         `cbor:"AlarmDescription,omitempty"`
	Namespace               string         `cbor:"Namespace"`
	MetricName              string         `cbor:"MetricName"`
	Dimensions              []dimensionCBR `cbor:"Dimensions,omitempty"`
	StateValue              string         `cbor:"StateValue"`
	StateReason             string         `cbor:"StateReason,omitempty"`
	StateUpdatedTimestamp   *time.Time     `cbor:"StateUpdatedTimestamp,omitempty"`
	ComparisonOperator      string         `cbor:"ComparisonOperator"`
	Threshold               float64        `cbor:"Threshold"`
	Period                  int            `cbor:"Period,omitempty"`
	EvaluationPeriods       int            `cbor:"EvaluationPeriods,omitempty"`
	DatapointsToAlarm       int            `cbor:"DatapointsToAlarm,omitempty"`
	Statistic               string         `cbor:"Statistic,omitempty"`
	ExtendedStatistic       string         `cbor:"ExtendedStatistic,omitempty"`
	Unit                    string         `cbor:"Unit,omitempty"`
	TreatMissingData        string         `cbor:"TreatMissingData,omitempty"`
	ActionsEnabled          bool           `cbor:"ActionsEnabled"`
	AlarmActions            []string       `cbor:"AlarmActions,omitempty"`
	OKActions               []string       `cbor:"OKActions,omitempty"`
	InsufficientDataActions []string       `cbor:"InsufficientDataActions,omitempty"`
}

type describeAlarmsOutput struct {
	MetricAlarms []metricAlarmCBR `cbor:"MetricAlarms"`
	NextToken    string           `cbor:"NextToken,omitempty"`
}

func (h *Handler) describeAlarms(w http.ResponseWriter, r *http.Request, body []byte) {
	var in describeAlarmsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	alarms, err := h.monitoring.DescribeAlarms(r.Context(), in.AlarmNames)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	matched := make([]metricAlarmCBR, 0, len(alarms))

	for i := range alarms {
		if !alarmMatchesFilters(&alarms[i], &in) {
			continue
		}

		matched = append(matched, toMetricAlarmCBR(&alarms[i]))
	}

	// Without paging inputs, preserve the historical "return everything" shape so
	// existing callers are unaffected. Deterministic ordering only matters once a
	// caller pages, so sort just in that branch.
	if in.MaxRecords == 0 && in.NextToken == "" {
		writeCBORResponse(w, describeAlarmsOutput{MetricAlarms: matched})
		return
	}

	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].AlarmName < matched[j].AlarmName
	})

	size := in.MaxRecords
	if size <= 0 {
		size = maxAlarmPageSize
	}

	from, to, next := pageWindow(len(matched), decodeOffsetToken(in.NextToken), size)

	resp := describeAlarmsOutput{MetricAlarms: matched[from:to]}
	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	writeCBORResponse(w, resp)
}

// alarmMatchesFilters applies the DescribeAlarms filter fields (name prefix,
// state, action prefix) that AWS honors server-side.
func alarmMatchesFilters(a *mondriver.AlarmInfo, in *describeAlarmsInput) bool {
	if in.AlarmNamePrefix != "" && !strings.HasPrefix(a.Name, in.AlarmNamePrefix) {
		return false
	}

	if in.StateValue != "" && a.State != in.StateValue {
		return false
	}

	if in.ActionPrefix != "" && !anyActionHasPrefix(a, in.ActionPrefix) {
		return false
	}

	return true
}

func anyActionHasPrefix(a *mondriver.AlarmInfo, prefix string) bool {
	for _, actions := range [][]string{a.AlarmActions, a.OKActions, a.InsufficientDataActions} {
		for _, act := range actions {
			if strings.HasPrefix(act, prefix) {
				return true
			}
		}
	}

	return false
}

func toMetricAlarmCBR(a *mondriver.AlarmInfo) metricAlarmCBR {
	m := metricAlarmCBR{
		AlarmName:               a.Name,
		AlarmArn:                a.AlarmArn,
		AlarmDescription:        a.AlarmDescription,
		Namespace:               a.Namespace,
		MetricName:              a.MetricName,
		Dimensions:              dimsToCBR(a.Dimensions),
		StateValue:              a.State,
		StateReason:             a.StateReason,
		ComparisonOperator:      a.ComparisonOperator,
		Threshold:               a.Threshold,
		Period:                  a.Period,
		EvaluationPeriods:       a.EvaluationPeriods,
		DatapointsToAlarm:       a.DatapointsToAlarm,
		Statistic:               a.Statistic,
		ExtendedStatistic:       a.ExtendedStatistic,
		Unit:                    a.Unit,
		TreatMissingData:        a.TreatMissingData,
		ActionsEnabled:          a.ActionsEnabled,
		AlarmActions:            a.AlarmActions,
		OKActions:               a.OKActions,
		InsufficientDataActions: a.InsufficientDataActions,
	}

	if !a.StateUpdatedTimestamp.IsZero() {
		ts := a.StateUpdatedTimestamp.UTC()
		m.StateUpdatedTimestamp = &ts
	}

	return m
}

// dimsToCBR renders a dimension map as sorted wire dimensions for stable output.
func dimsToCBR(dims map[string]string) []dimensionCBR {
	if len(dims) == 0 {
		return nil
	}

	keys := make([]string, 0, len(dims))
	for k := range dims {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]dimensionCBR, 0, len(dims))
	for _, k := range keys {
		out = append(out, dimensionCBR{Name: k, Value: dims[k]})
	}

	return out
}

type deleteAlarmsInput struct {
	AlarmNames []string `cbor:"AlarmNames"`
}

func (h *Handler) deleteAlarms(w http.ResponseWriter, r *http.Request, body []byte) {
	var in deleteAlarmsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	for _, name := range in.AlarmNames {
		if err := h.monitoring.DeleteAlarm(r.Context(), name); err != nil {
			writeDriverErr(w, err)
			return
		}
	}

	writeCBORResponse(w, struct{}{})
}

type setAlarmStateInput struct {
	AlarmName   string `cbor:"AlarmName"`
	StateValue  string `cbor:"StateValue"`
	StateReason string `cbor:"StateReason"`
}

// setAlarmState is the SDK (rpc-v2-cbor) side of SetAlarmState — the query/CLI
// path already had it, but SDK clients got UnknownOperationException.
func (h *Handler) setAlarmState(w http.ResponseWriter, r *http.Request, body []byte) {
	var in setAlarmStateInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	if err := h.monitoring.SetAlarmState(r.Context(), in.AlarmName, in.StateValue, in.StateReason); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

func toDimensionMap(dims []dimensionCBR) map[string]string {
	if len(dims) == 0 {
		return nil
	}

	out := make(map[string]string, len(dims))

	for _, d := range dims {
		if d.Name != "" {
			out[d.Name] = d.Value
		}
	}

	return out
}
