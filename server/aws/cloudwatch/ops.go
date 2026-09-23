package cloudwatch

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	statSum         = "Sum"
	statMinimum     = "Minimum"
	statMaximum     = "Maximum"
	statSampleCount = "SampleCount"
	statAverage     = "Average"
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

	if err := h.putMetricDataCore(r.Context(), &in); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

// datapointCBR uses pointers so a requested statistic of 0 is still sent.
// With a plain float64 and omitempty, the SDK decoded a 0 Sum as nil.
type datapointCBR struct {
	Timestamp   time.Time `cbor:"Timestamp"`
	SampleCount *float64  `cbor:"SampleCount,omitempty"`
	Average     *float64  `cbor:"Average,omitempty"`
	Sum         *float64  `cbor:"Sum,omitempty"`
	Minimum     *float64  `cbor:"Minimum,omitempty"`
	Maximum     *float64  `cbor:"Maximum,omitempty"`
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

	res, err := h.getMetricStatisticsCore(r.Context(), &in)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	out := getMetricStatisticsOutput{Label: res.Label}
	for _, dp := range res.Datapoints {
		out.Datapoints = append(out.Datapoints, datapointCBR(dp))
	}

	writeCBORResponse(w, out)
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

func (h *Handler) listMetrics(w http.ResponseWriter, r *http.Request, body []byte) {
	var in listMetricsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	res, err := h.listMetricsCore(r.Context(), in)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	out := listMetricsOutput{Metrics: make([]metricCBR, 0, len(res.Metrics)), NextToken: res.NextToken}
	for _, m := range res.Metrics {
		out.Metrics = append(out.Metrics, metricCBR{
			Namespace: m.Namespace, MetricName: m.MetricName, Dimensions: dimsToCBR(m.Dimensions),
		})
	}

	writeCBORResponse(w, out)
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

func (h *Handler) putMetricAlarm(w http.ResponseWriter, r *http.Request, body []byte) {
	var in putMetricAlarmInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
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

	if err := h.putMetricAlarmCore(r.Context(), &cfg); err != nil {
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
	AlarmTypes      []string `cbor:"AlarmTypes,omitempty"`
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
	StateReasonData         string         `cbor:"StateReasonData,omitempty"`
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
	MetricAlarms    []metricAlarmCBR    `cbor:"MetricAlarms"`
	CompositeAlarms []compositeAlarmCBR `cbor:"CompositeAlarms,omitempty"`
	NextToken       string              `cbor:"NextToken,omitempty"`
}

func (h *Handler) describeAlarms(w http.ResponseWriter, r *http.Request, body []byte) {
	var in describeAlarmsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	matched := make([]metricAlarmCBR, 0)

	// AlarmTypes selects metric alarms, composite alarms, or (when omitted) both.
	if wantsAlarmType(in.AlarmTypes, alarmTypeMetric) {
		alarms, err := h.monitoring.DescribeAlarms(r.Context(), in.AlarmNames)
		if err != nil {
			writeDriverErr(w, err)
			return
		}

		for i := range alarms {
			if !alarmMatchesFilters(&alarms[i], &in) {
				continue
			}

			matched = append(matched, toMetricAlarmCBR(&alarms[i]))
		}
	}

	// Always paginate: real CloudWatch caps a page at 100 alarms and returns a
	// NextToken for the rest, so an unpaged "return everything" reply would drop
	// alarms past 100 for callers that don't pass paging inputs.
	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].AlarmName < matched[j].AlarmName
	})

	size := in.MaxRecords
	if size <= 0 {
		size = maxAlarmPageSize
	}

	offset, err := offsetFromToken(in.NextToken, errInvalidNextToken)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	from, to, next := pageWindow(len(matched), offset, size)

	resp := describeAlarmsOutput{MetricAlarms: matched[from:to]}
	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	// Composite alarms are a small, separate collection; return them all on the
	// first page (offset 0) so they aren't duplicated across metric-alarm pages.
	if offset == 0 && wantsAlarmType(in.AlarmTypes, alarmTypeComposite) {
		composites, err := h.compositeAlarmRows(r, &in)
		if err != nil {
			writeDriverErr(w, err)
			return
		}

		resp.CompositeAlarms = composites
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

//nolint:dupl // parallel to query.go toAlarmMemberXML but a distinct CBOR wire shape.
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
		StateReasonData:         a.StateReasonData,
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

	// AWS tolerates incorrect alarm names: the correctly named alarms are still
	// deleted and no ResourceNotFound is returned. Skip not-found names so a
	// batch that includes an already-gone alarm (e.g. terraform destroy) never
	// fails spuriously or leaves a half-deleted state.
	for _, name := range in.AlarmNames {
		if err := h.monitoring.DeleteAlarm(r.Context(), name); err != nil && !cerrors.IsNotFound(err) {
			writeDriverErr(w, err)
			return
		}
	}

	// DeleteAlarms accepts both metric and composite alarm names in one call; a
	// name that isn't a metric alarm (tolerated above) may be a composite alarm.
	if store, ok := h.monitoring.(compositeAlarmStore); ok {
		if err := store.DeleteCompositeAlarms(r.Context(), in.AlarmNames); err != nil {
			writeDriverErr(w, err)
			return
		}
	}

	writeCBORResponse(w, struct{}{})
}

// setAlarmState is the SDK (rpc-v2-cbor) side of SetAlarmState.
func (h *Handler) setAlarmState(w http.ResponseWriter, r *http.Request, body []byte) {
	var in setAlarmStateInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	if err := h.setAlarmStateCore(r.Context(), &in); err != nil {
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
