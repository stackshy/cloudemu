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
	case opPutMetricAlarm:
		h.queryPutMetricAlarm(w, r)
	case opDescribeAlarms:
		h.queryDescribeAlarms(w, r)
	case opDeleteAlarms:
		h.queryDeleteAlarms(w, r)
	case opSetAlarmState:
		h.querySetAlarmState(w, r)
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
	ns := r.Form.Get("Namespace")

	var data []mondriver.MetricDatum

	for i := 1; ; i++ {
		p := "MetricData.member." + strconv.Itoa(i) + "."
		name := r.Form.Get(p + "MetricName")
		if name == "" {
			break
		}

		val, _ := strconv.ParseFloat(r.Form.Get(p+"Value"), 64)

		ts := time.Now().UTC()
		if raw := r.Form.Get(p + "Timestamp"); raw != "" {
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
				ts = parsed
			}
		}

		datum := mondriver.MetricDatum{
			Namespace: ns, MetricName: name, Value: val, Unit: r.Form.Get(p + "Unit"),
			Dimensions: queryDimensions(r, p+"Dimensions.member."), Timestamp: ts,
			StatisticValues: queryStatisticValues(r, p+"StatisticValues."),
			Values:          queryFloatList(r, p+"Values.member."),
			Counts:          queryFloatList(r, p+"Counts.member."),
		}

		data = append(data, datum)
	}

	if err := h.monitoring.PutMetricData(r.Context(), data); err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "PutMetricDataResponse", nil)
}

func (h *Handler) queryListMetrics(w http.ResponseWriter, r *http.Request) {
	names, err := h.monitoring.ListMetrics(r.Context(), r.Form.Get("Namespace"))
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	sort.Strings(names)

	ns := r.Form.Get("Namespace")
	from, to, next := pageWindow(len(names), decodeOffsetToken(r.Form.Get("NextToken")), listMetricsPageSize)

	members := make([]metricMemberXML, 0, to-from)
	for _, n := range names[from:to] {
		members = append(members, metricMemberXML{Namespace: ns, MetricName: n})
	}

	result := listMetricsResultXML{Metrics: members}
	if next > 0 {
		result.NextToken = encodeOffsetToken(next)
	}

	writeQueryResponse(w, "ListMetricsResponse", result)
}

func (h *Handler) queryGetMetricStatistics(w http.ResponseWriter, r *http.Request) {
	stats := queryStringList(r, "Statistics.member.")
	if len(stats) == 0 {
		stats = []string{statAverage}
	}

	start, _ := time.Parse(time.RFC3339, r.Form.Get("StartTime"))
	end, _ := time.Parse(time.RFC3339, r.Form.Get("EndTime"))
	period, _ := strconv.Atoi(r.Form.Get("Period"))
	dims := queryDimensions(r, "Dimensions.member.")

	acc := newQueryDatapointAcc()

	for _, stat := range stats {
		res, err := h.monitoring.GetMetricData(r.Context(), mondriver.GetMetricInput{
			Namespace: r.Form.Get("Namespace"), MetricName: r.Form.Get("MetricName"),
			Dimensions: dims, StartTime: start, EndTime: end, Period: period, Stat: stat,
		})
		if err != nil {
			writeQueryDriverErr(w, err)
			return
		}

		acc.add(res, stat)
	}

	writeQueryResponse(w, "GetMetricStatisticsResponse",
		getStatsResultXML{Label: r.Form.Get("MetricName"), Datapoints: acc.datapoints()})
}

// queryDatapointAcc merges per-statistic results into one XML datapoint per
// timestamp so a multi-statistic GetMetricStatistics returns every requested
// statistic on each datapoint.
type queryDatapointAcc struct {
	byTS  map[int64]*datapointXML
	order []int64
	unit  string
}

func newQueryDatapointAcc() *queryDatapointAcc {
	return &queryDatapointAcc{byTS: map[int64]*datapointXML{}}
}

func (a *queryDatapointAcc) add(res *mondriver.MetricDataResult, stat string) {
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
			dp = &datapointXML{Timestamp: ts.Format(time.RFC3339)}
			a.byTS[key] = dp
			a.order = append(a.order, key)
		}

		setQueryStat(dp, stat, res.Values[i])
	}
}

func (a *queryDatapointAcc) datapoints() []datapointXML {
	unit := a.unit
	if unit == "" {
		unit = defaultMetricUnit
	}

	sort.Slice(a.order, func(i, j int) bool { return a.order[i] < a.order[j] })

	out := make([]datapointXML, 0, len(a.order))

	for _, key := range a.order {
		dp := a.byTS[key]
		dp.Unit = unit
		out = append(out, *dp)
	}

	return out
}

func (h *Handler) queryPutMetricAlarm(w http.ResponseWriter, r *http.Request) {
	comparisonOperator := r.Form.Get("ComparisonOperator")
	if !comparisonOperatorValid(comparisonOperator) {
		writeQueryError(w, http.StatusBadRequest, "ValidationError", "Invalid ComparisonOperator: "+comparisonOperator)
		return
	}

	threshold, _ := strconv.ParseFloat(r.Form.Get("Threshold"), 64)
	period, _ := strconv.Atoi(r.Form.Get("Period"))
	evalPeriods, _ := strconv.Atoi(r.Form.Get("EvaluationPeriods"))
	datapointsToAlarm, _ := strconv.Atoi(r.Form.Get("DatapointsToAlarm"))

	err := h.monitoring.CreateAlarm(r.Context(), mondriver.AlarmConfig{
		Name: r.Form.Get("AlarmName"), Namespace: r.Form.Get("Namespace"), MetricName: r.Form.Get("MetricName"),
		Dimensions: queryDimensions(r, "Dimensions.member."), ComparisonOperator: comparisonOperator,
		Threshold: threshold, Period: period, EvaluationPeriods: evalPeriods, DatapointsToAlarm: datapointsToAlarm,
		Stat: r.Form.Get("Statistic"), ExtendedStatistic: r.Form.Get("ExtendedStatistic"),
		Unit: r.Form.Get("Unit"), TreatMissingData: r.Form.Get("TreatMissingData"),
		AlarmDescription: r.Form.Get("AlarmDescription"), ActionsEnabled: queryOptBool(r, "ActionsEnabled"),
		AlarmActions:            queryStringList(r, "AlarmActions.member."),
		OKActions:               queryStringList(r, "OKActions.member."),
		InsufficientDataActions: queryStringList(r, "InsufficientDataActions.member."),
		Tags:                    queryTagPairs(r, "Tags.member."),
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

	offset := decodeOffsetToken(r.Form.Get("NextToken"))
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
		m.StateUpdatedTimestamp = a.StateUpdatedTimestamp.UTC().Format(time.RFC3339)
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
	// AWS tolerates incorrect alarm names: valid ones are still deleted and no
	// ResourceNotFound is returned.
	for _, name := range queryStringList(r, "AlarmNames.member.") {
		if err := h.monitoring.DeleteAlarm(r.Context(), name); err != nil && !cerrors.IsNotFound(err) {
			writeQueryDriverErr(w, err)
			return
		}
	}

	writeQueryResponse(w, "DeleteAlarmsResponse", nil)
}

func (h *Handler) querySetAlarmState(w http.ResponseWriter, r *http.Request) {
	err := h.monitoring.SetAlarmState(r.Context(), r.Form.Get("AlarmName"), r.Form.Get("StateValue"), r.Form.Get("StateReason"))
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "SetAlarmStateResponse", nil)
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
func queryStatisticValues(r *http.Request, prefix string) *mondriver.StatisticSet {
	raw := r.Form.Get(prefix + "SampleCount")
	if raw == "" {
		return nil
	}

	sampleCount, _ := strconv.ParseFloat(raw, 64)
	sum, _ := strconv.ParseFloat(r.Form.Get(prefix+"Sum"), 64)
	minimum, _ := strconv.ParseFloat(r.Form.Get(prefix+"Minimum"), 64)
	maximum, _ := strconv.ParseFloat(r.Form.Get(prefix+"Maximum"), 64)

	return &mondriver.StatisticSet{SampleCount: sampleCount, Sum: sum, Minimum: minimum, Maximum: maximum}
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

func setQueryStat(dp *datapointXML, stat string, v float64) {
	switch stat {
	case "Sum":
		dp.Sum = v
	case "Minimum":
		dp.Minimum = v
	case "Maximum":
		dp.Maximum = v
	case "SampleCount":
		dp.SampleCount = v
	default:
		dp.Average = v
	}
}

// ---- XML response shapes (query protocol, 2010-08-01) ----

type metricMemberXML struct {
	Namespace  string `xml:"Namespace"`
	MetricName string `xml:"MetricName"`
}

type listMetricsResultXML struct {
	XMLName   xml.Name          `xml:"ListMetricsResult"`
	Metrics   []metricMemberXML `xml:"Metrics>member"`
	NextToken string            `xml:"NextToken,omitempty"`
}

type datapointXML struct {
	Timestamp   string  `xml:"Timestamp"`
	SampleCount float64 `xml:"SampleCount,omitempty"`
	Average     float64 `xml:"Average,omitempty"`
	Sum         float64 `xml:"Sum,omitempty"`
	Minimum     float64 `xml:"Minimum,omitempty"`
	Maximum     float64 `xml:"Maximum,omitempty"`
	Unit        string  `xml:"Unit,omitempty"`
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
	AlarmName               string         `xml:"AlarmName"`
	AlarmArn                string         `xml:"AlarmArn,omitempty"`
	AlarmDescription        string         `xml:"AlarmDescription,omitempty"`
	Namespace               string         `xml:"Namespace,omitempty"`
	MetricName              string         `xml:"MetricName,omitempty"`
	Dimensions              []dimensionXML `xml:"Dimensions>member,omitempty"`
	StateValue              string         `xml:"StateValue"`
	StateReason             string         `xml:"StateReason,omitempty"`
	StateUpdatedTimestamp   string         `xml:"StateUpdatedTimestamp,omitempty"`
	ComparisonOperator      string         `xml:"ComparisonOperator"`
	Threshold               float64        `xml:"Threshold"`
	Period                  int            `xml:"Period,omitempty"`
	EvaluationPeriods       int            `xml:"EvaluationPeriods,omitempty"`
	DatapointsToAlarm       int            `xml:"DatapointsToAlarm,omitempty"`
	Statistic               string         `xml:"Statistic,omitempty"`
	ExtendedStatistic       string         `xml:"ExtendedStatistic,omitempty"`
	Unit                    string         `xml:"Unit,omitempty"`
	TreatMissingData        string         `xml:"TreatMissingData,omitempty"`
	ActionsEnabled          bool           `xml:"ActionsEnabled"`
	AlarmActions            []string       `xml:"AlarmActions>member,omitempty"`
	OKActions               []string       `xml:"OKActions>member,omitempty"`
	InsufficientDataActions []string       `xml:"InsufficientDataActions>member,omitempty"`
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
