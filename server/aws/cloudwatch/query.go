package cloudwatch

// CloudWatch's AWS CLI (and older SDKs) use the classic AWS **query protocol**
// (form-encoded POST, `Action=...`, XML responses) rather than rpc-v2-cbor.
// This file adds that path so `aws cloudwatch ...` works against the emulator.
// Query requests are disambiguated from EC2 (which also claims form POSTs) by
// the SigV4 credential scope service, which is "monitoring" for CloudWatch.

import (
	"encoding/xml"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	queryNamespace = "http://monitoring.amazonaws.com/doc/2010-08-01/"
	queryRequestID = "00000000-0000-0000-0000-000000000000"
	sigV4Service   = "monitoring"
)

// isQueryRequest reports whether r is a CloudWatch query-protocol request:
// a form-encoded POST (or GET with Action) whose SigV4 credential scope names
// the "monitoring" service.
func isQueryRequest(r *http.Request) bool {
	if r.Header.Get(protocolHeader) == protocolValue {
		return false // rpc-v2-cbor, handled elsewhere
	}

	if r.URL.Query().Get("Action") == "" &&
		!(r.Method == http.MethodPost && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")) {
		return false
	}

	return awsquery.CredentialScopeService(r.Header.Get("Authorization")) == sigV4Service
}

// serveQuery handles a CloudWatch query-protocol request.
//
//nolint:gocyclo // first-match dispatch over many CloudWatch query actions.
func (h *Handler) serveQuery(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeQueryError(w, http.StatusBadRequest, "MalformedQueryString", err.Error())
		return
	}

	switch r.Form.Get("Action") {
	case opPutMetricData:
		h.queryPutMetricData(w, r)
	case opListMetrics:
		h.queryListMetrics(w, r)
	case opGetMetricStatistics:
		h.queryGetMetricStatistics(w, r)
	case opGetMetricData:
		h.queryGetMetricData(w, r)
	case opDescribeAlarmsForMetric:
		h.queryDescribeAlarmsForMetric(w, r)
	case opPutMetricAlarm:
		h.queryPutMetricAlarm(w, r)
	case opPutCompositeAlarm:
		h.queryPutCompositeAlarm(w, r)
	case opDescribeAlarms:
		h.queryDescribeAlarms(w, r)
	case opDeleteAlarms:
		h.queryDeleteAlarms(w, r)
	case opSetAlarmState:
		h.querySetAlarmState(w, r)
	case opEnableAlarmActions:
		h.querySetAlarmActionsEnabled(w, r, true)
	case opDisableAlarmActions:
		h.querySetAlarmActionsEnabled(w, r, false)
	case opDescribeAlarmHistory:
		h.queryDescribeAlarmHistory(w, r)
	case opPutDashboard:
		h.queryPutDashboard(w, r)
	case opGetDashboard:
		h.queryGetDashboard(w, r)
	case opListDashboards:
		h.queryListDashboards(w, r)
	case opDeleteDashboards:
		h.queryDeleteDashboards(w, r)
	case opPutMetricStream:
		h.queryPutMetricStream(w, r)
	case opGetMetricStream:
		h.queryGetMetricStream(w, r)
	case opListMetricStreams:
		h.queryListMetricStreams(w, r)
	case opDeleteMetricStream:
		h.queryDeleteMetricStream(w, r)
	case opStartMetricStreams:
		h.queryStartMetricStreams(w, r)
	case opStopMetricStreams:
		h.queryStopMetricStreams(w, r)
	case opTagResource:
		h.queryTagResource(w, r)
	case opUntagResource:
		h.queryUntagResource(w, r)
	case opListTagsForResource:
		h.queryListTagsForResource(w, r)
	default:
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "unsupported CloudWatch action: "+r.Form.Get("Action"))
	}
}

func (h *Handler) queryPutMetricData(w http.ResponseWriter, r *http.Request) {
	in := putMetricDataInput{Namespace: r.Form.Get("Namespace")}

	for i := 1; ; i++ {
		p := "MetricData.member." + strconv.Itoa(i) + "."
		name := r.Form.Get(p + "MetricName")
		if name == "" {
			break
		}

		d := putMetricDatumCBR{
			MetricName:      name,
			Unit:            r.Form.Get(p + "Unit"),
			Dimensions:      dimsToCBR(queryDimensions(r, p+"Dimensions.member.")),
			StatisticValues: queryStatisticValues(r, p+"StatisticValues."),
			Values:          queryFloatList(r, p+"Values.member."),
			Counts:          queryFloatList(r, p+"Counts.member."),
		}

		d.Value, _ = strconv.ParseFloat(r.Form.Get(p+"Value"), 64)

		if parsed, err := time.Parse(time.RFC3339, r.Form.Get(p+"Timestamp")); err == nil {
			d.Timestamp = &parsed
		}

		in.MetricData = append(in.MetricData, d)
	}

	if err := h.putMetricDataCore(r.Context(), &in); err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "PutMetricDataResponse", nil)
}

func (h *Handler) queryListMetrics(w http.ResponseWriter, r *http.Request) {
	res, err := h.listMetricsCore(r.Context(), queryListMetricsInput(r))
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	members := make([]metricMemberXML, 0, len(res.Metrics))
	for _, m := range res.Metrics {
		members = append(members, metricMemberXML{
			Namespace: m.Namespace, MetricName: m.MetricName, Dimensions: dimsToXML(m.Dimensions),
		})
	}

	writeQueryResponse(w, "ListMetricsResponse", listMetricsResultXML{Metrics: members, NextToken: res.NextToken})
}

func queryListMetricsInput(r *http.Request) listMetricsInput {
	in := listMetricsInput{
		Namespace:  r.Form.Get("Namespace"),
		MetricName: r.Form.Get("MetricName"),
		NextToken:  r.Form.Get("NextToken"),
	}

	// Value is optional on a DimensionFilter, so the list ends at the first
	// missing Name, not the first missing Value.
	for i := 1; ; i++ {
		p := "Dimensions.member." + strconv.Itoa(i) + "."

		name := r.Form.Get(p + "Name")
		if name == "" {
			break
		}

		in.Dimensions = append(in.Dimensions, dimensionFilterCBR{Name: name, Value: r.Form.Get(p + "Value")})
	}

	return in
}

func (h *Handler) queryGetMetricStatistics(w http.ResponseWriter, r *http.Request) {
	in := queryGetMetricStatisticsInput(r)

	res, err := h.getMetricStatisticsCore(r.Context(), &in)
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	points := make([]datapointXML, 0, len(res.Datapoints))
	for _, dp := range res.Datapoints {
		points = append(points, datapointXML{
			Timestamp: dp.Timestamp.Format(time.RFC3339), SampleCount: dp.SampleCount, Average: dp.Average,
			Sum: dp.Sum, Minimum: dp.Minimum, Maximum: dp.Maximum, Unit: dp.Unit,
		})
	}

	writeQueryResponse(w, "GetMetricStatisticsResponse", getStatsResultXML{Label: res.Label, Datapoints: points})
}

func queryGetMetricStatisticsInput(r *http.Request) getMetricStatisticsInput {
	in := getMetricStatisticsInput{
		Namespace:  r.Form.Get("Namespace"),
		MetricName: r.Form.Get("MetricName"),
		Statistics: queryStringList(r, "Statistics.member."),
		Dimensions: dimsToCBR(queryDimensions(r, "Dimensions.member.")),
		Unit:       r.Form.Get("Unit"),
	}

	in.Period, _ = strconv.Atoi(r.Form.Get("Period"))

	if t, err := time.Parse(time.RFC3339, r.Form.Get("StartTime")); err == nil {
		in.StartTime = &t
	}

	if t, err := time.Parse(time.RFC3339, r.Form.Get("EndTime")); err == nil {
		in.EndTime = &t
	}

	return in
}

func (h *Handler) queryPutMetricAlarm(w http.ResponseWriter, r *http.Request) {
	threshold, _ := strconv.ParseFloat(r.Form.Get("Threshold"), 64)
	period, _ := strconv.Atoi(r.Form.Get("Period"))
	evalPeriods, _ := strconv.Atoi(r.Form.Get("EvaluationPeriods"))
	datapointsToAlarm, _ := strconv.Atoi(r.Form.Get("DatapointsToAlarm"))

	err := h.putMetricAlarmCore(r.Context(), &mondriver.AlarmConfig{
		Name: r.Form.Get("AlarmName"), Namespace: r.Form.Get("Namespace"), MetricName: r.Form.Get("MetricName"),
		Dimensions: queryDimensions(r, "Dimensions.member."), ComparisonOperator: r.Form.Get("ComparisonOperator"),
		Threshold: threshold, Period: period, EvaluationPeriods: evalPeriods, DatapointsToAlarm: datapointsToAlarm,
		Stat: r.Form.Get("Statistic"), ExtendedStatistic: r.Form.Get("ExtendedStatistic"),
		Unit: r.Form.Get("Unit"), TreatMissingData: r.Form.Get("TreatMissingData"),
		AlarmDescription: r.Form.Get("AlarmDescription"), ActionsEnabled: queryOptBool(r, "ActionsEnabled"),
		AlarmActions:            queryStringList(r, "AlarmActions.member."),
		OKActions:               queryStringList(r, "OKActions.member."),
		InsufficientDataActions: queryStringList(r, "InsufficientDataActions.member."),
		Tags:                    queryTagPairs(r, "Tags.member."),
		Metrics:                 toDriverQueries(queryMetricDataQueries(r, "Metrics")),
		ThresholdMetricID:       r.Form.Get("ThresholdMetricId"),
	})
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "PutMetricAlarmResponse", nil)
}

// queryDescribeAlarms mirrors the rpc-v2-cbor describeAlarms: it renders the
// full MetricAlarm shape (not just a handful of fields), honors the
// AlarmNamePrefix / StateValue / ActionPrefix / AlarmTypes filters, and returns
// composite alarms alongside metric alarms. The query protocol is what the AWS
// CLI and the Terraform AWS provider actually speak to CloudWatch, so a
// truncated reply here left aws_cloudwatch_metric_alarm in perpetual drift.
func (h *Handler) queryDescribeAlarms(w http.ResponseWriter, r *http.Request) {
	in := describeAlarmsInput{
		AlarmNames:      queryStringList(r, "AlarmNames.member."),
		AlarmNamePrefix: r.Form.Get("AlarmNamePrefix"),
		AlarmTypes:      queryStringList(r, "AlarmTypes.member."),
		StateValue:      r.Form.Get("StateValue"),
		ActionPrefix:    r.Form.Get("ActionPrefix"),
	}

	members := make([]alarmMemberXML, 0)

	if wantsAlarmType(in.AlarmTypes, alarmTypeMetric) {
		alarms, err := h.monitoring.DescribeAlarms(r.Context(), in.AlarmNames)
		if err != nil {
			writeQueryDriverErr(w, err)
			return
		}

		for i := range alarms {
			if !alarmMatchesFilters(&alarms[i], &in) {
				continue
			}

			members = append(members, toAlarmMemberXML(&alarms[i]))
		}
	}

	sort.SliceStable(members, func(i, j int) bool { return members[i].AlarmName < members[j].AlarmName })

	size := maxAlarmPageSize
	if v, _ := strconv.Atoi(r.Form.Get("MaxRecords")); v > 0 {
		size = v
	}

	offset, err := offsetFromToken(r.Form.Get("NextToken"), errInvalidNextToken)
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	from, to, next := pageWindow(len(members), offset, size)

	result := describeAlarmsResultXML{MetricAlarms: members[from:to]}
	if next > 0 {
		result.NextToken = encodeOffsetToken(next)
	}

	// Composite alarms are a small, separate collection returned in full on the
	// first page so they aren't duplicated across metric-alarm pages.
	if offset == 0 && wantsAlarmType(in.AlarmTypes, alarmTypeComposite) {
		composites, err := h.compositeAlarmRows(r, &in)
		if err != nil {
			writeQueryDriverErr(w, err)
			return
		}

		result.CompositeAlarms = toCompositeAlarmMemberXMLs(composites)
	}

	writeQueryResponse(w, "DescribeAlarmsResponse", result)
}

// toAlarmMemberXML renders an AlarmInfo as the full query-protocol MetricAlarm
// member, matching every field the rpc-v2-cbor path returns so a Terraform read
// round-trips without drift.
//
//nolint:dupl // parallel to ops.go toMetricAlarmCBR but a distinct XML wire shape.
func toAlarmMemberXML(a *mondriver.AlarmInfo) alarmMemberXML {
	m := alarmMemberXML{
		AlarmName:               a.Name,
		AlarmArn:                a.AlarmArn,
		AlarmDescription:        a.AlarmDescription,
		Namespace:               a.Namespace,
		MetricName:              a.MetricName,
		Dimensions:              dimsToXML(a.Dimensions),
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
		Metrics:                 toQueriesXML(a.Metrics),
		ThresholdMetricID:       a.ThresholdMetricID,
	}

	if !a.StateUpdatedTimestamp.IsZero() {
		m.StateUpdatedTimestamp = a.StateUpdatedTimestamp.UTC().Format(time.RFC3339)
	}

	if !a.StateTransitionedTimestamp.IsZero() {
		m.StateTransitionedTimestamp = a.StateTransitionedTimestamp.UTC().Format(time.RFC3339)
	}

	return m
}

// dimsToXML renders a dimension map as sorted wire dimensions for stable output.
func dimsToXML(dims map[string]string) []dimensionXML {
	if len(dims) == 0 {
		return nil
	}

	keys := make([]string, 0, len(dims))
	for k := range dims {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]dimensionXML, 0, len(dims))
	for _, k := range keys {
		out = append(out, dimensionXML{Name: k, Value: dims[k]})
	}

	return out
}

// toCompositeAlarmMemberXMLs converts the shared composite-alarm rows to their
// query-protocol XML members.
func toCompositeAlarmMemberXMLs(rows []compositeAlarmCBR) []compositeAlarmMemberXML {
	out := make([]compositeAlarmMemberXML, 0, len(rows))

	for i := range rows {
		row := &rows[i]
		m := compositeAlarmMemberXML{
			AlarmName:               row.AlarmName,
			AlarmArn:                row.AlarmArn,
			AlarmRule:               row.AlarmRule,
			AlarmDescription:        row.AlarmDescription,
			StateValue:              row.StateValue,
			StateReason:             row.StateReason,
			ActionsEnabled:          row.ActionsEnabled,
			AlarmActions:            row.AlarmActions,
			OKActions:               row.OKActions,
			InsufficientDataActions: row.InsufficientDataActions,
		}

		if row.StateUpdatedTimestamp != nil {
			m.StateUpdatedTimestamp = row.StateUpdatedTimestamp.UTC().Format(time.RFC3339)
		}

		out = append(out, m)
	}

	return out
}

func (h *Handler) queryDeleteAlarms(w http.ResponseWriter, r *http.Request) {
	names := queryStringList(r, "AlarmNames.member.")

	// AWS tolerates incorrect alarm names: valid ones are still deleted and no
	// ResourceNotFound is returned.
	for _, name := range names {
		if err := h.monitoring.DeleteAlarm(r.Context(), name); err != nil && !cerrors.IsNotFound(err) {
			writeQueryDriverErr(w, err)
			return
		}
	}

	// DeleteAlarms accepts both metric and composite alarm names in one call; a
	// name that isn't a metric alarm (tolerated above) may be a composite alarm.
	if store, ok := h.monitoring.(compositeAlarmStore); ok {
		if err := store.DeleteCompositeAlarms(r.Context(), names); err != nil {
			writeQueryDriverErr(w, err)
			return
		}
	}

	writeQueryResponse(w, "DeleteAlarmsResponse", nil)
}

// queryPutCompositeAlarm is the query-protocol twin of putCompositeAlarm,
// backing `aws cloudwatch put-composite-alarm` and the Terraform
// aws_cloudwatch_composite_alarm resource (both speak the query protocol).
func (h *Handler) queryPutCompositeAlarm(w http.ResponseWriter, r *http.Request) {
	store, ok := h.monitoring.(compositeAlarmStore)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "composite alarms not supported")
		return
	}

	err := store.PutCompositeAlarm(r.Context(), mondriver.CompositeAlarmConfig{
		Name:                    r.Form.Get("AlarmName"),
		AlarmRule:               r.Form.Get("AlarmRule"),
		AlarmDescription:        r.Form.Get("AlarmDescription"),
		ActionsEnabled:          queryOptBool(r, "ActionsEnabled"),
		AlarmActions:            queryStringList(r, "AlarmActions.member."),
		OKActions:               queryStringList(r, "OKActions.member."),
		InsufficientDataActions: queryStringList(r, "InsufficientDataActions.member."),
		Tags:                    queryTagPairs(r, "Tags.member."),
	})
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "PutCompositeAlarmResponse", nil)
}

// querySetAlarmActionsEnabled is the query-protocol twin of
// setAlarmActionsEnabled, backing `aws cloudwatch enable-alarm-actions` /
// `disable-alarm-actions`.
func (h *Handler) querySetAlarmActionsEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	toggler, ok := h.monitoring.(alarmActionsToggler)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "alarm actions toggle not supported")
		return
	}

	if err := toggler.SetAlarmActionsEnabled(r.Context(), queryStringList(r, "AlarmNames.member."), enabled); err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	root := "EnableAlarmActionsResponse"
	if !enabled {
		root = "DisableAlarmActionsResponse"
	}

	writeQueryResponse(w, root, nil)
}

func (h *Handler) querySetAlarmState(w http.ResponseWriter, r *http.Request) {
	in := setAlarmStateInput{
		AlarmName:       formValue(r, "AlarmName"),
		StateValue:      formValue(r, "StateValue"),
		StateReason:     formValue(r, "StateReason"),
		StateReasonData: formValue(r, "StateReasonData"),
	}

	if err := h.setAlarmStateCore(r.Context(), &in); err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "SetAlarmStateResponse", nil)
}

// formValue returns a form field, or nil when the field is absent.
func formValue(r *http.Request, key string) *string {
	if _, ok := r.Form[key]; !ok {
		return nil
	}

	v := r.Form.Get(key)

	return &v
}

// ---- form list helpers ----

func queryDimensions(r *http.Request, prefix string) map[string]string {
	var out map[string]string

	for i := 1; ; i++ {
		name := r.Form.Get(prefix + strconv.Itoa(i) + ".Name")
		if name == "" {
			break
		}

		if out == nil {
			out = map[string]string{}
		}

		out[name] = r.Form.Get(prefix + strconv.Itoa(i) + ".Value")
	}

	return out
}

// queryStatisticValues parses a StatisticSet (SampleCount/Sum/Minimum/Maximum)
// from the query-protocol form, returning nil when no SampleCount is present.
func queryStatisticValues(r *http.Request, prefix string) *statisticSetCBR {
	raw := r.Form.Get(prefix + "SampleCount")
	if raw == "" {
		return nil
	}

	sampleCount, _ := strconv.ParseFloat(raw, 64)
	sum, _ := strconv.ParseFloat(r.Form.Get(prefix+"Sum"), 64)
	minimum, _ := strconv.ParseFloat(r.Form.Get(prefix+"Minimum"), 64)
	maximum, _ := strconv.ParseFloat(r.Form.Get(prefix+"Maximum"), 64)

	return &statisticSetCBR{SampleCount: sampleCount, Sum: sum, Minimum: minimum, Maximum: maximum}
}

// queryFloatList parses a 1-indexed list of floats (Values.member.N /
// Counts.member.N) from the query-protocol form.
func queryFloatList(r *http.Request, prefix string) []float64 {
	var out []float64

	for i := 1; ; i++ {
		v := r.Form.Get(prefix + strconv.Itoa(i))
		if v == "" {
			break
		}

		f, _ := strconv.ParseFloat(v, 64)
		out = append(out, f)
	}

	return out
}

// queryOptBool returns a *bool for a form field that AWS treats as optional
// with a default (e.g. ActionsEnabled defaults to true when absent). An absent
// or unparseable value yields nil so the backend applies its own default.
func queryOptBool(r *http.Request, field string) *bool {
	raw := r.Form.Get(field)
	if raw == "" {
		return nil
	}

	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}

	return &v
}

func queryStringList(r *http.Request, prefix string) []string {
	var out []string

	for i := 1; ; i++ {
		v := r.Form.Get(prefix + strconv.Itoa(i))
		if v == "" {
			break
		}

		out = append(out, v)
	}

	return out
}

// ---- XML response shapes (query protocol, 2010-08-01) ----

type metricMemberXML struct {
	Namespace  string         `xml:"Namespace"`
	MetricName string         `xml:"MetricName"`
	Dimensions []dimensionXML `xml:"Dimensions>member,omitempty"`
}

type listMetricsResultXML struct {
	XMLName   xml.Name          `xml:"ListMetricsResult"`
	Metrics   []metricMemberXML `xml:"Metrics>member"`
	NextToken string            `xml:"NextToken,omitempty"`
}

// datapointXML uses pointers so a requested statistic of 0 is still sent.
// A nil pointer means the statistic was not requested.
type datapointXML struct {
	Timestamp   string   `xml:"Timestamp"`
	SampleCount *float64 `xml:"SampleCount,omitempty"`
	Average     *float64 `xml:"Average,omitempty"`
	Sum         *float64 `xml:"Sum,omitempty"`
	Minimum     *float64 `xml:"Minimum,omitempty"`
	Maximum     *float64 `xml:"Maximum,omitempty"`
	Unit        string   `xml:"Unit,omitempty"`
}

type getStatsResultXML struct {
	XMLName    xml.Name       `xml:"GetMetricStatisticsResult"`
	Label      string         `xml:"Label"`
	Datapoints []datapointXML `xml:"Datapoints>member"`
}

type dimensionXML struct {
	Name  string `xml:"Name"`
	Value string `xml:"Value"`
}

type alarmMemberXML struct {
	AlarmName                  string               `xml:"AlarmName"`
	AlarmArn                   string               `xml:"AlarmArn,omitempty"`
	AlarmDescription           string               `xml:"AlarmDescription,omitempty"`
	Namespace                  string               `xml:"Namespace,omitempty"`
	MetricName                 string               `xml:"MetricName,omitempty"`
	Dimensions                 []dimensionXML       `xml:"Dimensions>member,omitempty"`
	StateValue                 string               `xml:"StateValue"`
	StateReason                string               `xml:"StateReason,omitempty"`
	StateReasonData            string               `xml:"StateReasonData,omitempty"`
	StateUpdatedTimestamp      string               `xml:"StateUpdatedTimestamp,omitempty"`
	StateTransitionedTimestamp string               `xml:"StateTransitionedTimestamp,omitempty"`
	ComparisonOperator         string               `xml:"ComparisonOperator"`
	Threshold                  float64              `xml:"Threshold"`
	Period                     int                  `xml:"Period,omitempty"`
	EvaluationPeriods          int                  `xml:"EvaluationPeriods,omitempty"`
	DatapointsToAlarm          int                  `xml:"DatapointsToAlarm,omitempty"`
	Statistic                  string               `xml:"Statistic,omitempty"`
	ExtendedStatistic          string               `xml:"ExtendedStatistic,omitempty"`
	Unit                       string               `xml:"Unit,omitempty"`
	TreatMissingData           string               `xml:"TreatMissingData,omitempty"`
	ActionsEnabled             bool                 `xml:"ActionsEnabled"`
	AlarmActions               []string             `xml:"AlarmActions>member,omitempty"`
	OKActions                  []string             `xml:"OKActions>member,omitempty"`
	InsufficientDataActions    []string             `xml:"InsufficientDataActions>member,omitempty"`
	Metrics                    []metricDataQueryXML `xml:"Metrics>member,omitempty"`
	ThresholdMetricID          string               `xml:"ThresholdMetricId,omitempty"`
}

type compositeAlarmMemberXML struct {
	AlarmName               string   `xml:"AlarmName"`
	AlarmArn                string   `xml:"AlarmArn,omitempty"`
	AlarmRule               string   `xml:"AlarmRule"`
	AlarmDescription        string   `xml:"AlarmDescription,omitempty"`
	StateValue              string   `xml:"StateValue"`
	StateReason             string   `xml:"StateReason,omitempty"`
	StateUpdatedTimestamp   string   `xml:"StateUpdatedTimestamp,omitempty"`
	ActionsEnabled          bool     `xml:"ActionsEnabled"`
	AlarmActions            []string `xml:"AlarmActions>member,omitempty"`
	OKActions               []string `xml:"OKActions>member,omitempty"`
	InsufficientDataActions []string `xml:"InsufficientDataActions>member,omitempty"`
}

type describeAlarmsResultXML struct {
	XMLName         xml.Name                  `xml:"DescribeAlarmsResult"`
	MetricAlarms    []alarmMemberXML          `xml:"MetricAlarms>member"`
	CompositeAlarms []compositeAlarmMemberXML `xml:"CompositeAlarms>member,omitempty"`
	NextToken       string                    `xml:"NextToken,omitempty"`
}

// writeQueryResponse writes an AWS query-protocol XML envelope. result may be
// nil for actions that return only ResponseMetadata.
func writeQueryResponse(w http.ResponseWriter, root string, result any) {
	type meta struct {
		RequestID string `xml:"RequestId"`
	}

	var buf strings.Builder

	buf.WriteString(`<?xml version="1.0"?>`)
	buf.WriteString(`<` + root + ` xmlns="` + queryNamespace + `">`)

	if result != nil {
		// result structs carry their own <ActionResult> XMLName.
		inner, err := xml.Marshal(result)
		if err != nil {
			writeQueryError(w, http.StatusInternalServerError, "InternalFailure", err.Error())
			return
		}

		buf.Write(inner)
	}

	m, _ := xml.Marshal(struct {
		meta `xml:"ResponseMetadata"`
	}{meta{RequestID: queryRequestID}})
	buf.Write(m)
	buf.WriteString(`</` + root + `>`)

	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(buf.String()))
}

func writeQueryError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<?xml version="1.0"?><ErrorResponse xmlns="` + queryNamespace +
		`"><Error><Type>Sender</Type><Code>` + code + `</Code><Message>` + xmlEscape(msg) +
		`</Message></Error><RequestId>` + queryRequestID + `</RequestId></ErrorResponse>`))
}

func writeQueryDriverErr(w http.ResponseWriter, err error) {
	if we, ok := asWireError(err); ok {
		writeQueryError(w, http.StatusBadRequest, we.code, we.msg)
		return
	}

	code, status := "InternalFailure", http.StatusInternalServerError

	switch {
	case cerrors.IsNotFound(err):
		code, status = "ResourceNotFound", http.StatusNotFound
	case cerrors.IsInvalidArgument(err):
		code, status = "InvalidParameterValue", http.StatusBadRequest
	}

	writeQueryError(w, status, code, err.Error())
}

func xmlEscape(s string) string {
	var b strings.Builder

	_ = xml.EscapeText(&b, []byte(s))

	return b.String()
}
