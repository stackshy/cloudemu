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

// newQueryPoster returns a helper that POSTs a CloudWatch query-protocol form to
// a fresh handler-backed test server and returns the status code and body.
func newQueryPoster(t *testing.T) func(url.Values) (int, string) {
	t.Helper()

	h := cwserver.New(cwprovider.New(config.NewOptions()))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	return func(form url.Values) (int, string) {
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
}

// TestQueryDescribeAlarmsFullFidelity guards against the Terraform-drift bug
// where the query-protocol DescribeAlarms returned only a handful of fields:
// aws_cloudwatch_metric_alarm reads DescribeAlarms on every refresh, so a
// missing field looks like drift and never converges.
func TestQueryDescribeAlarmsFullFidelity(t *testing.T) {
	post := newQueryPoster(t)

	if code, body := post(url.Values{
		"Action": {"PutMetricAlarm"}, "AlarmName": {"cpu"}, "Namespace": {"AWS/EC2"}, "MetricName": {"CPUUtilization"},
		"ComparisonOperator": {"GreaterThanThreshold"}, "EvaluationPeriods": {"3"}, "DatapointsToAlarm": {"2"},
		"Period": {"60"}, "Threshold": {"75"}, "Statistic": {"Average"}, "Unit": {"Percent"},
		"TreatMissingData": {"notBreaching"}, "AlarmDescription": {"desc"}, "ActionsEnabled": {"false"},
		"Dimensions.member.1.Name": {"InstanceId"}, "Dimensions.member.1.Value": {"i-abc"},
		"AlarmActions.member.1":            {"arn:aws:sns:us-east-1:123456789012:t1"},
		"InsufficientDataActions.member.1": {"arn:aws:sns:us-east-1:123456789012:t2"},
	}); code != 200 {
		t.Fatalf("PutMetricAlarm: code=%d body=%s", code, body)
	}

	code, body := post(url.Values{"Action": {"DescribeAlarms"}, "AlarmNames.member.1": {"cpu"}})
	if code != 200 {
		t.Fatalf("DescribeAlarms: code=%d body=%s", code, body)
	}

	for _, want := range []string{
		"<AlarmArn>arn:aws:cloudwatch:us-east-1:123456789012:alarm:cpu</AlarmArn>",
		"<AlarmDescription>desc</AlarmDescription>",
		"<Period>60</Period>",
		"<EvaluationPeriods>3</EvaluationPeriods>",
		"<DatapointsToAlarm>2</DatapointsToAlarm>",
		"<Statistic>Average</Statistic>",
		"<Unit>Percent</Unit>",
		"<TreatMissingData>notBreaching</TreatMissingData>",
		"<ComparisonOperator>GreaterThanThreshold</ComparisonOperator>",
		"<ActionsEnabled>false</ActionsEnabled>",
		"<Name>InstanceId</Name>",
		"<Value>i-abc</Value>",
		"arn:aws:sns:us-east-1:123456789012:t1",
		"arn:aws:sns:us-east-1:123456789012:t2",
		"<StateValue>INSUFFICIENT_DATA</StateValue>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("DescribeAlarms missing %q\nbody: %s", want, body)
		}
	}
}

// TestQueryDescribeAlarmsFilters verifies the query DescribeAlarms honors the
// AlarmNamePrefix, StateValue, and AlarmTypes filters like the rpc-v2-cbor path.
func TestQueryDescribeAlarmsFilters(t *testing.T) {
	post := newQueryPoster(t)

	for _, name := range []string{"prod-a", "prod-b", "dev-a"} {
		post(url.Values{
			"Action": {"PutMetricAlarm"}, "AlarmName": {name}, "Namespace": {"AWS/EC2"}, "MetricName": {"CPUUtilization"},
			"ComparisonOperator": {"GreaterThanThreshold"}, "EvaluationPeriods": {"1"}, "Period": {"60"}, "Threshold": {"1"},
			"Statistic": {"Average"},
		})
	}

	_, body := post(url.Values{"Action": {"DescribeAlarms"}, "AlarmNamePrefix": {"prod-"}})
	if !strings.Contains(body, "prod-a") || !strings.Contains(body, "prod-b") || strings.Contains(body, "dev-a") {
		t.Fatalf("AlarmNamePrefix filter failed: %s", body)
	}

	// AlarmTypes=CompositeAlarm must exclude all metric alarms.
	_, body = post(url.Values{"Action": {"DescribeAlarms"}, "AlarmTypes.member.1": {"CompositeAlarm"}})
	if strings.Contains(body, "<AlarmName>prod-a</AlarmName>") {
		t.Fatalf("AlarmTypes=CompositeAlarm should exclude metric alarms: %s", body)
	}
}

// TestQueryDashboards verifies the query-protocol dashboard operations that back
// the aws_cloudwatch_dashboard Terraform resource and the AWS CLI.
func TestQueryDashboards(t *testing.T) {
	post := newQueryPoster(t)

	if code, body := post(url.Values{
		"Action": {"PutDashboard"}, "DashboardName": {"ops"}, "DashboardBody": {`{"widgets":[]}`},
	}); code != 200 || !strings.Contains(body, "PutDashboardResult") {
		t.Fatalf("PutDashboard: code=%d body=%s", code, body)
	}

	code, body := post(url.Values{"Action": {"GetDashboard"}, "DashboardName": {"ops"}})
	if code != 200 || !strings.Contains(body, `<DashboardBody>{&#34;widgets&#34;:[]}</DashboardBody>`) {
		t.Fatalf("GetDashboard: code=%d body=%s", code, body)
	}
	// Dashboards are global: the ARN carries an empty region.
	if !strings.Contains(body, "<DashboardArn>arn:aws:cloudwatch::123456789012:dashboard/ops</DashboardArn>") {
		t.Fatalf("GetDashboard ARN not region-less: %s", body)
	}

	if code, body := post(url.Values{"Action": {"ListDashboards"}}); code != 200 ||
		!strings.Contains(body, "<DashboardName>ops</DashboardName>") {
		t.Fatalf("ListDashboards: code=%d body=%s", code, body)
	}

	if code, body := post(url.Values{"Action": {"DeleteDashboards"}, "DashboardNames.member.1": {"ops"}}); code != 200 {
		t.Fatalf("DeleteDashboards: code=%d body=%s", code, body)
	}

	// Deleting a missing dashboard is a ResourceNotFound (all-or-nothing).
	if code, body := post(url.Values{"Action": {"GetDashboard"}, "DashboardName": {"ops"}}); code != 404 {
		t.Fatalf("GetDashboard after delete should be 404: code=%d body=%s", code, body)
	}
}

// TestQueryDescribeAlarmHistory verifies the query-protocol DescribeAlarmHistory
// surfaces the transition recorded by SetAlarmState.
func TestQueryDescribeAlarmHistory(t *testing.T) {
	post := newQueryPoster(t)

	post(url.Values{
		"Action": {"PutMetricAlarm"}, "AlarmName": {"h1"}, "Namespace": {"AWS/EC2"}, "MetricName": {"CPUUtilization"},
		"ComparisonOperator": {"GreaterThanThreshold"}, "EvaluationPeriods": {"1"}, "Period": {"60"}, "Threshold": {"1"},
		"Statistic": {"Average"},
	})
	post(url.Values{"Action": {"SetAlarmState"}, "AlarmName": {"h1"}, "StateValue": {"ALARM"}, "StateReason": {"boom"}})

	code, body := post(url.Values{"Action": {"DescribeAlarmHistory"}, "AlarmName": {"h1"}})
	if code != 200 {
		t.Fatalf("DescribeAlarmHistory: code=%d body=%s", code, body)
	}

	if !strings.Contains(body, "<AlarmName>h1</AlarmName>") ||
		!strings.Contains(body, "<HistoryItemType>StateUpdate</HistoryItemType>") {
		t.Fatalf("DescribeAlarmHistory missing entry: %s", body)
	}
}
