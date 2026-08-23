package cloudwatch

// CloudWatch's AWS CLI (and older SDKs) use the classic AWS **query protocol**
// (form-encoded POST, `Action=...`, XML responses) rather than rpc-v2-cbor.
// This file adds that path so `aws cloudwatch ...` works against the emulator.
// Query requests are disambiguated from EC2 (which also claims form POSTs) by
// the SigV4 credential scope service, which is "monitoring" for CloudWatch.

import (
	"encoding/xml"
	"net/http"
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
func (h *Handler) serveQuery(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeQueryError(w, http.StatusBadRequest, "MalformedQueryString", err.Error())
		return
	}

	switch r.Form.Get("Action") {
	case "PutMetricData":
		h.queryPutMetricData(w, r)
	case "ListMetrics":
		h.queryListMetrics(w, r)
	case "GetMetricStatistics":
		h.queryGetMetricStatistics(w, r)
	case "PutMetricAlarm":
		h.queryPutMetricAlarm(w, r)
	case "DescribeAlarms":
		h.queryDescribeAlarms(w, r)
	case "DeleteAlarms":
		h.queryDeleteAlarms(w, r)
	case "SetAlarmState":
		h.querySetAlarmState(w, r)
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

		data = append(data, mondriver.MetricDatum{
			Namespace: ns, MetricName: name, Value: val, Unit: r.Form.Get(p + "Unit"),
			Dimensions: queryDimensions(r, p+"Dimensions.member."), Timestamp: ts,
		})
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

	ns := r.Form.Get("Namespace")
	members := make([]metricMemberXML, 0, len(names))

	for _, n := range names {
		members = append(members, metricMemberXML{Namespace: ns, MetricName: n})
	}

	writeQueryResponse(w, "ListMetricsResponse", listMetricsResultXML{Metrics: members})
}

func (h *Handler) queryGetMetricStatistics(w http.ResponseWriter, r *http.Request) {
	stat := r.Form.Get("Statistics.member.1")
	if stat == "" {
		stat = "Average"
	}

	start, _ := time.Parse(time.RFC3339, r.Form.Get("StartTime"))
	end, _ := time.Parse(time.RFC3339, r.Form.Get("EndTime"))
	period, _ := strconv.Atoi(r.Form.Get("Period"))

	res, err := h.monitoring.GetMetricData(r.Context(), mondriver.GetMetricInput{
		Namespace: r.Form.Get("Namespace"), MetricName: r.Form.Get("MetricName"),
		Dimensions: queryDimensions(r, "Dimensions.member."), StartTime: start, EndTime: end,
		Period: period, Stat: stat,
	})
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	var dps []datapointXML

	if res != nil {
		for i := range res.Timestamps {
			dp := datapointXML{Timestamp: res.Timestamps[i].UTC().Format(time.RFC3339), Unit: "Count"}
			setQueryStat(&dp, stat, res.Values[i])
			dps = append(dps, dp)
		}
	}

	writeQueryResponse(w, "GetMetricStatisticsResponse", getStatsResultXML{Label: r.Form.Get("MetricName"), Datapoints: dps})
}

func (h *Handler) queryPutMetricAlarm(w http.ResponseWriter, r *http.Request) {
	threshold, _ := strconv.ParseFloat(r.Form.Get("Threshold"), 64)
	period, _ := strconv.Atoi(r.Form.Get("Period"))
	evalPeriods, _ := strconv.Atoi(r.Form.Get("EvaluationPeriods"))

	err := h.monitoring.CreateAlarm(r.Context(), mondriver.AlarmConfig{
		Name: r.Form.Get("AlarmName"), Namespace: r.Form.Get("Namespace"), MetricName: r.Form.Get("MetricName"),
		Dimensions: queryDimensions(r, "Dimensions.member."), ComparisonOperator: r.Form.Get("ComparisonOperator"),
		Threshold: threshold, Period: period, EvaluationPeriods: evalPeriods, Stat: r.Form.Get("Statistic"),
		AlarmActions: queryStringList(r, "AlarmActions.member."), OKActions: queryStringList(r, "OKActions.member."),
	})
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "PutMetricAlarmResponse", nil)
}

func (h *Handler) queryDescribeAlarms(w http.ResponseWriter, r *http.Request) {
	alarms, err := h.monitoring.DescribeAlarms(r.Context(), queryStringList(r, "AlarmNames.member."))
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	members := make([]alarmMemberXML, 0, len(alarms))
	for i := range alarms {
		members = append(members, alarmMemberXML{
			AlarmName: alarms[i].Name, Namespace: alarms[i].Namespace, MetricName: alarms[i].MetricName,
			StateValue: alarms[i].State, ComparisonOperator: alarms[i].ComparisonOperator, Threshold: alarms[i].Threshold,
		})
	}

	writeQueryResponse(w, "DescribeAlarmsResponse", describeAlarmsResultXML{MetricAlarms: members})
}

func (h *Handler) queryDeleteAlarms(w http.ResponseWriter, r *http.Request) {
	for _, name := range queryStringList(r, "AlarmNames.member.") {
		if err := h.monitoring.DeleteAlarm(r.Context(), name); err != nil {
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
	XMLName xml.Name          `xml:"ListMetricsResult"`
	Metrics []metricMemberXML `xml:"Metrics>member"`
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

type alarmMemberXML struct {
	AlarmName          string  `xml:"AlarmName"`
	Namespace          string  `xml:"Namespace"`
	MetricName         string  `xml:"MetricName"`
	StateValue         string  `xml:"StateValue"`
	ComparisonOperator string  `xml:"ComparisonOperator"`
	Threshold          float64 `xml:"Threshold"`
}

type describeAlarmsResultXML struct {
	XMLName      xml.Name         `xml:"DescribeAlarmsResult"`
	MetricAlarms []alarmMemberXML `xml:"MetricAlarms>member"`
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
