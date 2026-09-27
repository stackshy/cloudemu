package cloudwatch_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cwprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	cwserver "github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
)

// jsonTarget is the X-Amz-Target prefix botocore sends for CloudWatch over
// awsJson1_0.
const jsonTarget = "GraniteServiceVersion20100801."

type jsonResp struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func newJSONServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts := httptest.NewServer(cwserver.New(cwprovider.New(config.NewOptions())))
	t.Cleanup(ts.Close)

	return ts
}

func jsonCall(t *testing.T, ts *httptest.Server, op, body string) jsonResp {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(body)) //nolint:noctx // test request
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", jsonTarget+op)
	req.Header.Set("Authorization", monitoringAuth)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	out := jsonResp{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
	if err := json.Unmarshal(raw, &out.body); err != nil {
		t.Fatalf("%s: response is not a JSON object: %v: %q", op, err, raw)
	}

	return out
}

func jsonList(t *testing.T, r jsonResp, key string) []any {
	t.Helper()

	v, ok := r.body[key].([]any)
	if !ok {
		t.Fatalf("%s is not a JSON list: %s", key, r.raw)
	}

	return v
}

func TestJSONProtocolMatches(t *testing.T) {
	h := cwserver.New(cwprovider.New(config.NewOptions()))

	mk := func(target string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/x-amz-json-1.0")
		r.Header.Set("X-Amz-Target", target)

		return r
	}

	if !h.Matches(mk(jsonTarget + "DescribeAlarms")) {
		t.Fatal("Matches must claim a GraniteServiceVersion20100801 target")
	}

	for _, target := range []string{
		"DynamoDB_20120810.ListTables",
		"Logs_20140328.DescribeLogGroups",
		"GraniteServiceVersion20100801",
		"GraniteServiceVersion20100801X.DescribeAlarms",
	} {
		if h.Matches(mk(target)) {
			t.Fatalf("Matches must not claim target %q", target)
		}
	}
}

// TestJSONProtocolEveryOp drives every CloudWatch operation served on query
// and CBOR through the awsJson1_0 codec, in lifecycle order.
func TestJSONProtocolEveryOp(t *testing.T) {
	ts := newJSONServer(t)
	now := time.Now().Unix()
	alarmARN := ""
	streamARN := ""

	steps := []struct {
		op    string
		body  func() string
		check func(t *testing.T, r jsonResp)
	}{
		{op: "PutMetricData", body: func() string {
			return fmt.Sprintf(`{"Namespace":"App","MetricData":[{"MetricName":"Latency","Value":12.5,"Unit":"Milliseconds",`+
				`"Timestamp":%d,"Dimensions":[{"Name":"Host","Value":"a"}]},`+
				`{"MetricName":"Latency","Values":[1,2],"Counts":[1,1],"Timestamp":%d.5,"Dimensions":[{"Name":"Host","Value":"b"}]}]}`,
				now, now)
		}},
		{op: "GetMetricStatistics", body: func() string {
			return fmt.Sprintf(`{"Namespace":"App","MetricName":"Latency","Dimensions":[{"Name":"Host","Value":"a"}],`+
				`"StartTime":%d,"EndTime":%d,"Period":60,"Statistics":["Sum","Maximum"]}`, now-600, now+600)
		}, check: func(t *testing.T, r jsonResp) {
			dps := jsonList(t, r, "Datapoints")
			if len(dps) != 1 {
				t.Fatalf("want 1 datapoint: %s", r.raw)
			}

			dp := dps[0].(map[string]any)
			if _, ok := dp["Timestamp"].(float64); !ok {
				t.Fatalf("Timestamp must be epoch seconds: %s", r.raw)
			}

			if dp["Sum"] != 12.5 {
				t.Fatalf("Sum = %v: %s", dp["Sum"], r.raw)
			}
		}},
		{op: "GetMetricData", body: func() string {
			return fmt.Sprintf(`{"StartTime":%d,"EndTime":%d,"MetricDataQueries":[{"Id":"m1","MetricStat":{"Metric":`+
				`{"Namespace":"App","MetricName":"Latency","Dimensions":[{"Name":"Host","Value":"a"}]},"Period":60,"Stat":"Sum"}}]}`,
				now-600, now+600)
		}, check: func(t *testing.T, r jsonResp) {
			res := jsonList(t, r, "MetricDataResults")
			if len(res) != 1 {
				t.Fatalf("want 1 result: %s", r.raw)
			}

			m := res[0].(map[string]any)
			ts, ok := m["Timestamps"].([]any)
			if !ok || len(ts) != 1 {
				t.Fatalf("Timestamps: %s", r.raw)
			}

			if _, ok := ts[0].(float64); !ok {
				t.Fatalf("Timestamps must be epoch seconds: %s", r.raw)
			}
		}},
		{op: "ListMetrics", body: func() string { return `{"Namespace":"App"}` }, check: func(t *testing.T, r jsonResp) {
			if n := len(jsonList(t, r, "Metrics")); n != 2 {
				t.Fatalf("want 2 metrics, got %d: %s", n, r.raw)
			}
		}},
		{op: "PutMetricAlarm", body: func() string {
			return `{"AlarmName":"high","Namespace":"App","MetricName":"Latency","ComparisonOperator":"GreaterThanThreshold",` +
				`"Threshold":100,"Period":60,"EvaluationPeriods":1,"Statistic":"Average",` +
				`"Dimensions":[{"Name":"Host","Value":"a"}],"Tags":[{"Key":"env","Value":"dev"}]}`
		}},
		{op: "DescribeAlarms", body: func() string { return `{"AlarmNames":["high"]}` }, check: func(t *testing.T, r jsonResp) {
			alarms := jsonList(t, r, "MetricAlarms")
			if len(alarms) != 1 {
				t.Fatalf("want 1 alarm: %s", r.raw)
			}

			a := alarms[0].(map[string]any)
			if a["Threshold"] != float64(100) || a["Period"] != float64(60) {
				t.Fatalf("alarm fields: %s", r.raw)
			}

			if _, ok := a["StateUpdatedTimestamp"].(float64); !ok {
				t.Fatalf("StateUpdatedTimestamp must be epoch seconds: %s", r.raw)
			}

			alarmARN, _ = a["AlarmArn"].(string)
		}},
		{op: "DescribeAlarmsForMetric", body: func() string {
			return `{"Namespace":"App","MetricName":"Latency","Dimensions":[{"Name":"Host","Value":"a"}]}`
		}, check: func(t *testing.T, r jsonResp) {
			if n := len(jsonList(t, r, "MetricAlarms")); n != 1 {
				t.Fatalf("want 1 alarm: %s", r.raw)
			}
		}},
		{op: "SetAlarmState", body: func() string {
			return `{"AlarmName":"high","StateValue":"ALARM","StateReason":"test"}`
		}},
		{op: "DescribeAlarmHistory", body: func() string { return `{"AlarmName":"high"}` }, check: func(t *testing.T, r jsonResp) {
			items := jsonList(t, r, "AlarmHistoryItems")
			if len(items) == 0 {
				t.Fatalf("want history: %s", r.raw)
			}

			if _, ok := items[0].(map[string]any)["Timestamp"].(float64); !ok {
				t.Fatalf("history Timestamp must be epoch seconds: %s", r.raw)
			}
		}},
		{op: "DisableAlarmActions", body: func() string { return `{"AlarmNames":["high"]}` }},
		{op: "EnableAlarmActions", body: func() string { return `{"AlarmNames":["high"]}` }},
		{op: "PutCompositeAlarm", body: func() string {
			return `{"AlarmName":"combo","AlarmRule":"ALARM(\"high\")"}`
		}},
		{op: "TagResource", body: func() string {
			return fmt.Sprintf(`{"ResourceARN":%q,"Tags":[{"Key":"team","Value":"core"}]}`, alarmARN)
		}},
		{op: "ListTagsForResource", body: func() string {
			return fmt.Sprintf(`{"ResourceARN":%q}`, alarmARN)
		}, check: func(t *testing.T, r jsonResp) {
			if n := len(jsonList(t, r, "Tags")); n != 2 {
				t.Fatalf("want 2 tags: %s", r.raw)
			}
		}},
		{op: "UntagResource", body: func() string {
			return fmt.Sprintf(`{"ResourceARN":%q,"TagKeys":["team","env"]}`, alarmARN)
		}},
		{op: "ListTagsForResource", body: func() string {
			return fmt.Sprintf(`{"ResourceARN":%q}`, alarmARN)
		}, check: func(t *testing.T, r jsonResp) {
			if n := len(jsonList(t, r, "Tags")); n != 0 {
				t.Fatalf("want an empty Tags list: %s", r.raw)
			}
		}},
		{op: "DeleteAlarms", body: func() string { return `{"AlarmNames":["combo","high"]}` }},
		{op: "DescribeAlarms", body: func() string { return `{}` }, check: func(t *testing.T, r jsonResp) {
			if n := len(jsonList(t, r, "MetricAlarms")); n != 0 {
				t.Fatalf("want an empty MetricAlarms list: %s", r.raw)
			}
		}},
		{op: "PutDashboard", body: func() string {
			return `{"DashboardName":"main","DashboardBody":"{\"widgets\":[]}"}`
		}, check: func(t *testing.T, r jsonResp) {
			if n := len(jsonList(t, r, "DashboardValidationMessages")); n != 0 {
				t.Fatalf("want an empty validation list: %s", r.raw)
			}
		}},
		{op: "GetDashboard", body: func() string { return `{"DashboardName":"main"}` }, check: func(t *testing.T, r jsonResp) {
			if r.body["DashboardBody"] != `{"widgets":[]}` {
				t.Fatalf("DashboardBody: %s", r.raw)
			}
		}},
		{op: "ListDashboards", body: func() string { return `{}` }, check: func(t *testing.T, r jsonResp) {
			entries := jsonList(t, r, "DashboardEntries")
			if len(entries) != 1 {
				t.Fatalf("want 1 dashboard: %s", r.raw)
			}

			if _, ok := entries[0].(map[string]any)["LastModified"].(float64); !ok {
				t.Fatalf("LastModified must be epoch seconds: %s", r.raw)
			}
		}},
		{op: "DeleteDashboards", body: func() string { return `{"DashboardNames":["main"]}` }},
		{op: "PutMetricStream", body: func() string {
			return `{"Name":"s1","FirehoseArn":"arn:aws:firehose:us-east-1:123456789012:deliverystream/f",` +
				`"RoleArn":"arn:aws:iam::123456789012:role/r","OutputFormat":"json","IncludeFilters":[{"Namespace":"App"}]}`
		}, check: func(t *testing.T, r jsonResp) {
			streamARN, _ = r.body["Arn"].(string)
			if streamARN == "" {
				t.Fatalf("Arn: %s", r.raw)
			}
		}},
		{op: "GetMetricStream", body: func() string { return `{"Name":"s1"}` }, check: func(t *testing.T, r jsonResp) {
			if r.body["State"] != "running" {
				t.Fatalf("State: %s", r.raw)
			}

			if _, ok := r.body["CreationDate"].(float64); !ok {
				t.Fatalf("CreationDate must be epoch seconds: %s", r.raw)
			}
		}},
		{op: "StopMetricStreams", body: func() string { return `{"Names":["s1"]}` }},
		{op: "StartMetricStreams", body: func() string { return `{"Names":["s1"]}` }},
		{op: "ListMetricStreams", body: func() string { return `{}` }, check: func(t *testing.T, r jsonResp) {
			if n := len(jsonList(t, r, "Entries")); n != 1 {
				t.Fatalf("want 1 stream: %s", r.raw)
			}
		}},
		{op: "DeleteMetricStream", body: func() string { return `{"Name":"s1"}` }},
		{op: "PutAnomalyDetector", body: func() string {
			return `{"SingleMetricAnomalyDetector":{"Namespace":"App","MetricName":"Latency","Stat":"Average"}}`
		}},
		{op: "DescribeAnomalyDetectors", body: func() string { return `{}` }, check: func(t *testing.T, r jsonResp) {
			if n := len(jsonList(t, r, "AnomalyDetectors")); n != 1 {
				t.Fatalf("want 1 detector: %s", r.raw)
			}
		}},
		{op: "DeleteAnomalyDetector", body: func() string {
			return `{"SingleMetricAnomalyDetector":{"Namespace":"App","MetricName":"Latency","Stat":"Average"}}`
		}},
	}

	for i, s := range steps {
		r := jsonCall(t, ts, s.op, s.body())
		if r.status != http.StatusOK {
			t.Fatalf("step %d %s: status %d: %s", i, s.op, r.status, r.raw)
		}

		if ct := r.header.Get("Content-Type"); ct != "application/x-amz-json-1.0" {
			t.Fatalf("step %d %s: Content-Type = %q", i, s.op, ct)
		}

		if s.check != nil {
			s.check(t, r)
		}
	}
}

func TestJSONProtocolListMetricsPaging(t *testing.T) {
	ts := newJSONServer(t)

	var data []string
	for i := range 520 {
		data = append(data, fmt.Sprintf(`{"MetricName":"M%03d","Value":1}`, i))
	}

	for start := 0; start < len(data); start += 500 {
		end := min(start+500, len(data))
		body := `{"Namespace":"Paged","MetricData":[` + strings.Join(data[start:end], ",") + `]}`

		if r := jsonCall(t, ts, "PutMetricData", body); r.status != http.StatusOK {
			t.Fatalf("PutMetricData: %d %s", r.status, r.raw)
		}
	}

	first := jsonCall(t, ts, "ListMetrics", `{"Namespace":"Paged"}`)
	token, _ := first.body["NextToken"].(string)

	if len(jsonList(t, first, "Metrics")) != 500 || token == "" {
		t.Fatalf("first page: want 500 metrics and a NextToken: %d %v", len(jsonList(t, first, "Metrics")), first.body["NextToken"])
	}

	second := jsonCall(t, ts, "ListMetrics", fmt.Sprintf(`{"Namespace":"Paged","NextToken":%q}`, token))
	if len(jsonList(t, second, "Metrics")) != 20 || second.body["NextToken"] != nil {
		t.Fatalf("second page: %s", second.raw)
	}
}

func TestJSONProtocolErrors(t *testing.T) {
	ts := newJSONServer(t)

	tests := []struct {
		name       string
		op         string
		body       string
		status     int
		errType    string
		queryError string
	}{
		{
			name: "dashboard not found", op: "GetDashboard", body: `{"DashboardName":"nope"}`,
			status: http.StatusNotFound, errType: "ResourceNotFound", queryError: "ResourceNotFound;Sender",
		},
		{
			name: "invalid parameter value", op: "SetAlarmState", body: `{"AlarmName":"x","StateValue":"BOGUS","StateReason":"r"}`,
			status: http.StatusBadRequest, errType: "ValidationException", queryError: "ValidationError;Sender",
		},
		{
			name: "missing parameter", op: "PutMetricAlarm", body: `{"AlarmName":"a"}`,
			status: http.StatusBadRequest,
		},
		{
			name: "bad next token", op: "DescribeAlarms", body: `{"NextToken":"!!!"}`,
			status: http.StatusBadRequest, errType: "InvalidNextToken", queryError: "InvalidNextToken;Sender",
		},
		{
			name: "list metrics bad next token", op: "ListMetrics", body: `{"NextToken":"!!!"}`,
			status: http.StatusBadRequest, errType: "InvalidParameterValueException", queryError: "InvalidParameterValue;Sender",
		},
		{
			name: "anomaly detector not found", op: "DeleteAnomalyDetector",
			body:   `{"SingleMetricAnomalyDetector":{"Namespace":"App","MetricName":"Nope","Stat":"Average"}}`,
			status: http.StatusNotFound, errType: "ResourceNotFoundException", queryError: "ResourceNotFoundException;Sender",
		},
		{
			name: "malformed json", op: "DescribeAlarms", body: `{"AlarmNames":`,
			status: http.StatusBadRequest, errType: "SerializationException",
		},
		{
			name: "trailing data", op: "DescribeAlarms", body: `{"AlarmNames":[]} {"x":1}`,
			status: http.StatusBadRequest, errType: "SerializationException",
		},
		{
			name: "unknown operation", op: "NoSuchOperation", body: `{}`,
			status: http.StatusBadRequest, errType: "UnknownOperationException",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := jsonCall(t, ts, tt.op, tt.body)
			if r.status != tt.status {
				t.Fatalf("status = %d, want %d: %s", r.status, tt.status, r.raw)
			}

			errType, _ := r.body["__type"].(string)
			if errType == "" || r.header.Get("X-Amzn-Errortype") != errType {
				t.Fatalf("__type %q and X-Amzn-Errortype %q must match: %s", errType, r.header.Get("X-Amzn-Errortype"), r.raw)
			}

			if tt.errType != "" && errType != tt.errType {
				t.Fatalf("__type = %q, want %q", errType, tt.errType)
			}

			if tt.queryError != "" && r.header.Get("X-Amzn-Query-Error") != tt.queryError {
				t.Fatalf("X-Amzn-Query-Error = %q, want %q", r.header.Get("X-Amzn-Query-Error"), tt.queryError)
			}

			if msg, _ := r.body["message"].(string); msg == "" {
				t.Fatalf("message must be set: %s", r.raw)
			}
		})
	}
}

// TestJSONProtocolEmptyCollections checks that list outputs come back as JSON
// lists, not null, when there is nothing to return.
func TestJSONProtocolEmptyCollections(t *testing.T) {
	ts := newJSONServer(t)

	for op, key := range map[string]string{
		"ListMetrics":    "Metrics",
		"DescribeAlarms": "MetricAlarms",
		"ListDashboards": "DashboardEntries",
	} {
		r := jsonCall(t, ts, op, `{}`)
		if r.status != http.StatusOK {
			t.Fatalf("%s: %d %s", op, r.status, r.raw)
		}

		if n := len(jsonList(t, r, key)); n != 0 {
			t.Fatalf("%s: want an empty %s: %s", op, key, r.raw)
		}
	}

	// An empty request body is the same as {}.
	if r := jsonCall(t, ts, "ListMetrics", ""); r.status != http.StatusOK {
		t.Fatalf("empty body: %d %s", r.status, r.raw)
	}
}
