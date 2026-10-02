package cloudwatch_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

// wireProtocol sends one operation with a generic input over one CloudWatch
// protocol and returns the HTTP status, the error code a client sees ("" on
// success) and the raw body.
type wireProtocol struct {
	name string
	call func(t *testing.T, ts *httptest.Server, op string, in map[string]any) (int, string, string)
}

var queryCodePattern = regexp.MustCompile(`<Code>([^<]+)</Code>`)

func doCW(t *testing.T, req *http.Request) (int, http.Header, []byte) {
	t.Helper()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, resp.Header, raw
}

// wireProtocols covers query (the AWS CLI v2), rpc-v2-cbor (the Go SDK) and
// awsJson1_0 (botocore 1.43+).
func wireProtocols() []wireProtocol {
	return []wireProtocol{
		{name: "query", call: func(t *testing.T, ts *httptest.Server, op string, in map[string]any) (int, string, string) {
			t.Helper()

			form := url.Values{"Action": {op}, "Version": {"2010-08-01"}}
			flattenQuery(form, "", in)

			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode())) //nolint:noctx // test request
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Authorization", monitoringAuth)

			status, _, raw := doCW(t, req)
			code := ""

			if m := queryCodePattern.FindSubmatch(raw); status != http.StatusOK && m != nil {
				code = string(m[1])
			}

			return status, code, string(raw)
		}},
		{name: "cbor", call: func(t *testing.T, ts *httptest.Server, op string, in map[string]any) (int, string, string) {
			t.Helper()

			body, err := cbor.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}

			req, _ := http.NewRequest(http.MethodPost, //nolint:noctx // test request
				ts.URL+"/service/GraniteServiceVersion20100801/operation/"+op, bytes.NewReader(body))
			req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
			req.Header.Set("Content-Type", "application/cbor")

			status, _, raw := doCW(t, req)

			var out map[string]any
			_ = cbor.Unmarshal(raw, &out)

			code, _ := out["__type"].(string)
			if status == http.StatusOK {
				code = ""
			}

			return status, code, fmt.Sprint(out)
		}},
		{name: "json", call: func(t *testing.T, ts *httptest.Server, op string, in map[string]any) (int, string, string) {
			t.Helper()

			body, _ := json.Marshal(in)

			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", bytes.NewReader(body)) //nolint:noctx // test request
			req.Header.Set("Content-Type", "application/x-amz-json-1.0")
			req.Header.Set("X-Amz-Target", jsonTarget+op)

			status, hdr, raw := doCW(t, req)

			// botocore takes the code from X-Amzn-Query-Error on this
			// awsQueryCompatible service.
			code, _, _ := strings.Cut(hdr.Get("X-Amzn-Query-Error"), ";")

			return status, code, string(raw)
		}},
	}
}

// flattenQuery writes in as query-protocol parameters: nested members as
// "A.B" and list items as "A.member.N".
func flattenQuery(form url.Values, prefix string, in any) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}

		return prefix + "." + k
	}

	switch v := in.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		for _, k := range keys {
			flattenQuery(form, join(k), v[k])
		}
	case []any:
		for i, e := range v {
			flattenQuery(form, prefix+".member."+strconv.Itoa(i+1), e)
		}
	default:
		form.Set(prefix, fmt.Sprint(v))
	}
}

func alarmInput(overrides map[string]any) map[string]any {
	in := map[string]any{
		"AlarmName": "a1", "Namespace": "App", "MetricName": "Latency",
		"ComparisonOperator": "GreaterThanThreshold", "Threshold": 1.5,
		"Period": 60, "EvaluationPeriods": 2, "Statistic": "Average",
	}

	for k, v := range overrides {
		in[k] = v
	}

	return in
}

// TestValidationParity checks the request errors that each protocol must
// answer with the same code and status (tracker row CW-X13).
func TestValidationParity(t *testing.T) {
	var dims []any
	for i := range 31 {
		dims = append(dims, map[string]any{"Name": "d" + strconv.Itoa(i), "Value": "v"})
	}

	missingARN := "arn:aws:cloudwatch:us-east-1:123456789012:alarm:missing"

	tests := []struct {
		name   string
		op     string
		in     map[string]any
		status int
		code   string
	}{
		{"tag missing alarm", "TagResource",
			map[string]any{"ResourceARN": missingARN, "Tags": []any{map[string]any{"Key": "k", "Value": "v"}}},
			http.StatusNotFound, "ResourceNotFoundException"},
		{"untag missing alarm", "UntagResource",
			map[string]any{"ResourceARN": missingARN, "TagKeys": []any{"k"}}, http.StatusNotFound, "ResourceNotFoundException"},
		{"list tags missing alarm", "ListTagsForResource",
			map[string]any{"ResourceARN": missingARN}, http.StatusNotFound, "ResourceNotFoundException"},
		{"list tags missing stream", "ListTagsForResource",
			map[string]any{"ResourceARN": "arn:aws:cloudwatch:us-east-1:123456789012:metric-stream/missing"},
			http.StatusNotFound, "ResourceNotFoundException"},
		{"31 dimensions", "PutMetricData", map[string]any{"Namespace": "App", "MetricData": []any{
			map[string]any{"MetricName": "m", "Value": 1, "Dimensions": dims},
		}}, http.StatusBadRequest, "InvalidParameterValue"},
		{"period 61", "PutMetricAlarm", alarmInput(map[string]any{"Period": 61}), http.StatusBadRequest, "ValidationError"},
		{"sub-hour period over a day", "PutMetricAlarm", alarmInput(map[string]any{"Period": 1800, "EvaluationPeriods": 49}),
			http.StatusBadRequest, "ValidationError"},
		{"sub-hour period at a day ok", "PutMetricAlarm",
			alarmInput(map[string]any{"Period": 1800, "EvaluationPeriods": 48, "DatapointsToAlarm": 1}), http.StatusOK, ""},
		{"hour period over a week", "PutMetricAlarm", alarmInput(map[string]any{"Period": 3600, "EvaluationPeriods": 169}),
			http.StatusBadRequest, "ValidationError"},
		{"hour period at a week ok", "PutMetricAlarm",
			alarmInput(map[string]any{"Period": 3600, "EvaluationPeriods": 168, "DatapointsToAlarm": 1}), http.StatusOK, ""},
		{"6h period 8 times ok", "PutMetricAlarm",
			alarmInput(map[string]any{"Period": 21600, "EvaluationPeriods": 8, "DatapointsToAlarm": 1}), http.StatusOK, ""},
		{"day period 7 times ok", "PutMetricAlarm",
			alarmInput(map[string]any{"Period": 86400, "EvaluationPeriods": 7, "DatapointsToAlarm": 1}), http.StatusOK, ""},
		{"day period 8 times", "PutMetricAlarm", alarmInput(map[string]any{"Period": 86400, "EvaluationPeriods": 8}),
			http.StatusBadRequest, "ValidationError"},
		{"bad statistic", "PutMetricAlarm", alarmInput(map[string]any{"Statistic": "Median"}),
			http.StatusBadRequest, "ValidationError"},
		{"datapoints over periods", "PutMetricAlarm", alarmInput(map[string]any{"DatapointsToAlarm": 3}),
			http.StatusBadRequest, "ValidationError"},
		{"dashboard body not json", "PutDashboard", map[string]any{"DashboardName": "d", "DashboardBody": "{not json"},
			http.StatusBadRequest, "InvalidParameterInput"},
		{"dashboard body not an object", "PutDashboard", map[string]any{"DashboardName": "d", "DashboardBody": `"x"`},
			http.StatusBadRequest, "InvalidParameterInput"},
		{"stream include and exclude", "PutMetricStream", map[string]any{
			"Name": "s", "FirehoseArn": "arn:aws:firehose:us-east-1:123456789012:deliverystream/f",
			"RoleArn": "arn:aws:iam::123456789012:role/r", "OutputFormat": "json",
			"IncludeFilters": []any{map[string]any{"Namespace": "A"}},
			"ExcludeFilters": []any{map[string]any{"Namespace": "B"}},
		}, http.StatusBadRequest, "InvalidParameterValue"},
		{"stream bad firehose arn", "PutMetricStream", map[string]any{
			"Name": "s", "FirehoseArn": "bad", "RoleArn": "arn:aws:iam::123456789012:role/r", "OutputFormat": "json",
		}, http.StatusBadRequest, "InvalidParameterValue"},
		{"stream bad role arn", "PutMetricStream", map[string]any{
			"Name": "s", "FirehoseArn": "arn:aws:firehose:us-east-1:123456789012:deliverystream/f",
			"RoleArn": "arn:aws:iam::123456789012", "OutputFormat": "json",
		}, http.StatusBadRequest, "InvalidParameterValue"},
		{"period 30 and 20 datapoints ok", "PutMetricAlarm",
			alarmInput(map[string]any{"Period": 30, "EvaluationPeriods": 20, "DatapointsToAlarm": 20}), http.StatusOK, ""},
		{"30 dimensions ok", "PutMetricData", map[string]any{"Namespace": "App", "MetricData": []any{
			map[string]any{"MetricName": "m", "Value": 1, "Dimensions": dims[:30]},
		}}, http.StatusOK, ""},
	}

	for _, p := range wireProtocols() {
		t.Run(p.name, func(t *testing.T) {
			ts := newJSONServer(t)

			for _, tt := range tests {
				status, code, raw := p.call(t, ts, tt.op, tt.in)
				if status != tt.status || code != tt.code {
					t.Errorf("%s: got %d %q, want %d %q: %s", tt.name, status, code, tt.status, tt.code, raw)
				}
			}
		})
	}
}

// TestAlarmConfigurationUpdatedTimestamp checks DescribeAlarms returns the
// time the alarm configuration was last put, on every protocol.
func TestAlarmConfigurationUpdatedTimestamp(t *testing.T) {
	for _, p := range wireProtocols() {
		t.Run(p.name, func(t *testing.T) {
			ts := newJSONServer(t)

			if status, _, raw := p.call(t, ts, "PutMetricAlarm", alarmInput(nil)); status != http.StatusOK {
				t.Fatalf("PutMetricAlarm: %d %s", status, raw)
			}

			status, _, raw := p.call(t, ts, "DescribeAlarms", map[string]any{"AlarmNames": []any{"a1"}})
			if status != http.StatusOK || !strings.Contains(raw, "AlarmConfigurationUpdatedTimestamp") {
				t.Fatalf("DescribeAlarms: %d, want AlarmConfigurationUpdatedTimestamp: %s", status, raw)
			}
		})
	}
}
