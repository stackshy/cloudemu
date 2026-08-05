package cloudwatch_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cwprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	cwserver "github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
)

// monitoringAuth is a SigV4 Authorization header whose credential scope names
// the "monitoring" service — exactly what the AWS CLI sends for CloudWatch.
const monitoringAuth = "AWS4-HMAC-SHA256 Credential=test/20260804/us-east-1/monitoring/aws4_request, SignedHeaders=host, Signature=x"

// TestQueryProtocol verifies the CloudWatch handler serves the classic query
// protocol (form-encoded POST + XML) that the AWS CLI uses — regression guard
// for issue #319 (CloudWatch was previously stolen by the EC2 handler).
func TestQueryProtocol(t *testing.T) {
	h := cwserver.New(cwprovider.New(config.NewOptions()))
	ts := httptest.NewServer(h)

	t.Cleanup(ts.Close)

	post := func(form url.Values) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", monitoringAuth)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()

		b, _ := io.ReadAll(resp.Body)

		return resp.StatusCode, string(b)
	}

	// The handler must claim monitoring-scoped form POSTs.
	if !h.Matches(mustReq(monitoringAuth)) {
		t.Fatal("Matches should be true for a monitoring-scoped form POST")
	}
	// ...and must NOT claim ec2-scoped ones (those belong to the EC2 handler).
	if h.Matches(mustReq(strings.Replace(monitoringAuth, "monitoring", "ec2", 1))) {
		t.Fatal("Matches must be false for an ec2-scoped request")
	}

	// PutMetricData → 200 + PutMetricDataResponse.
	if code, body := post(url.Values{
		"Action": {"PutMetricData"}, "Namespace": {"MyApp"},
		"MetricData.member.1.MetricName": {"Requests"}, "MetricData.member.1.Value": {"42"},
	}); code != 200 || !strings.Contains(body, "PutMetricDataResponse") {
		t.Fatalf("PutMetricData: code=%d body=%s", code, body)
	}

	// ListMetrics → the metric we just put is present.
	code, body := post(url.Values{"Action": {"ListMetrics"}, "Namespace": {"MyApp"}})
	if code != 200 || !strings.Contains(body, "<MetricName>Requests</MetricName>") {
		t.Fatalf("ListMetrics: code=%d body=%s", code, body)
	}
	if !strings.Contains(body, "ListMetricsResult") {
		t.Fatalf("ListMetrics missing result wrapper: %s", body)
	}

	// PutMetricAlarm + DescribeAlarms round-trip.
	if code, body := post(url.Values{
		"Action": {"PutMetricAlarm"}, "AlarmName": {"a1"}, "Namespace": {"MyApp"}, "MetricName": {"Requests"},
		"ComparisonOperator": {"GreaterThanThreshold"}, "EvaluationPeriods": {"1"}, "Period": {"60"},
		"Threshold": {"10"}, "Statistic": {"Average"},
	}); code != 200 {
		t.Fatalf("PutMetricAlarm: code=%d body=%s", code, body)
	}

	if code, body := post(url.Values{"Action": {"DescribeAlarms"}}); code != 200 || !strings.Contains(body, "<AlarmName>a1</AlarmName>") {
		t.Fatalf("DescribeAlarms: code=%d body=%s", code, body)
	}

	// SetAlarmState + DeleteAlarms.
	if code, _ := post(url.Values{"Action": {"SetAlarmState"}, "AlarmName": {"a1"}, "StateValue": {"ALARM"}, "StateReason": {"t"}}); code != 200 {
		t.Fatalf("SetAlarmState: code=%d", code)
	}

	if code, _ := post(url.Values{"Action": {"DeleteAlarms"}, "AlarmNames.member.1": {"a1"}}); code != 200 {
		t.Fatalf("DeleteAlarms: code=%d", code)
	}
}

func mustReq(auth string) *http.Request {
	r, _ := http.NewRequest(http.MethodPost, "http://x/", strings.NewReader("Action=ListMetrics"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Authorization", auth)

	return r
}
