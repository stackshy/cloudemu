package cloudwatch_test

import (
	"net/url"
	"strings"
	"testing"
)

// TestQueryCompositeAlarmLifecycle exercises the composite-alarm operations over
// the query protocol (the path `aws cloudwatch put-composite-alarm` and the
// Terraform aws_cloudwatch_composite_alarm resource use): PutCompositeAlarm →
// DescribeAlarms surfaces it in CompositeAlarms → DeleteAlarms removes it. These
// were previously missing from the query dispatch, breaking the resource end to
// end.
func TestQueryCompositeAlarmLifecycle(t *testing.T) {
	post := newQueryPoster(t)

	rule := `ALARM("cpu-high")`

	if code, body := post(url.Values{
		"Action": {"PutCompositeAlarm"}, "AlarmName": {"app-unhealthy"}, "AlarmRule": {rule},
		"AlarmDescription": {"app is unhealthy"}, "AlarmActions.member.1": {"arn:aws:sns:us-east-1:000000000000:ops"},
	}); code != 200 || !strings.Contains(body, "PutCompositeAlarmResponse") {
		t.Fatalf("PutCompositeAlarm: code=%d body=%s", code, body)
	}

	code, body := post(url.Values{"Action": {"DescribeAlarms"}, "AlarmTypes.member.1": {"CompositeAlarm"}})
	if code != 200 {
		t.Fatalf("DescribeAlarms: code=%d body=%s", code, body)
	}

	for _, want := range []string{
		"<CompositeAlarms>", "<AlarmName>app-unhealthy</AlarmName>",
		"<AlarmRule>ALARM(&#34;cpu-high&#34;)</AlarmRule>",
		"<StateValue>INSUFFICIENT_DATA</StateValue>",
		"arn:aws:sns:us-east-1:000000000000:ops",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("DescribeAlarms body missing %q: %s", want, body)
		}
	}

	if code, _ := post(url.Values{"Action": {"DeleteAlarms"}, "AlarmNames.member.1": {"app-unhealthy"}}); code != 200 {
		t.Fatalf("DeleteAlarms: code=%d", code)
	}

	code, body = post(url.Values{"Action": {"DescribeAlarms"}, "AlarmTypes.member.1": {"CompositeAlarm"}})
	if code != 200 || strings.Contains(body, "app-unhealthy") {
		t.Fatalf("composite alarm still present after DeleteAlarms: code=%d body=%s", code, body)
	}
}

// TestQueryAlarmTagsRoundTrip confirms TagResource / ListTagsForResource work
// over the query protocol for both metric and composite alarms, which share the
// arn:...:alarm:NAME shape. Terraform calls ListTagsForResource on every read,
// so an unrecognized composite-alarm ARN left the resource unusable.
func TestQueryAlarmTagsRoundTrip(t *testing.T) {
	post := newQueryPoster(t)

	cases := []struct {
		name   string
		create url.Values
		arn    string
	}{
		{
			name: "metric",
			create: url.Values{
				"Action": {"PutMetricAlarm"}, "AlarmName": {"m1"}, "Namespace": {"MyApp"}, "MetricName": {"Requests"},
				"ComparisonOperator": {"GreaterThanThreshold"}, "EvaluationPeriods": {"1"}, "Period": {"60"},
				"Threshold": {"10"}, "Statistic": {"Average"},
			},
			arn: "arn:aws:cloudwatch:us-east-1:000000000000:alarm:m1",
		},
		{
			name: "composite",
			create: url.Values{
				"Action": {"PutCompositeAlarm"}, "AlarmName": {"c1"}, "AlarmRule": {`ALARM("m1")`},
			},
			arn: "arn:aws:cloudwatch:us-east-1:000000000000:alarm:c1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, body := post(tc.create); code != 200 {
				t.Fatalf("create: code=%d body=%s", code, body)
			}

			if code, body := post(url.Values{
				"Action": {"TagResource"}, "ResourceARN": {tc.arn},
				"Tags.member.1.Key": {"Env"}, "Tags.member.1.Value": {"prod"},
			}); code != 200 {
				t.Fatalf("TagResource: code=%d body=%s", code, body)
			}

			code, body := post(url.Values{"Action": {"ListTagsForResource"}, "ResourceARN": {tc.arn}})
			if code != 200 || !strings.Contains(body, "<Key>Env</Key>") || !strings.Contains(body, "<Value>prod</Value>") {
				t.Fatalf("ListTagsForResource: code=%d body=%s", code, body)
			}
		})
	}
}

// TestQueryEnableDisableAlarmActions confirms the query-protocol
// EnableAlarmActions / DisableAlarmActions operations toggle ActionsEnabled.
func TestQueryEnableDisableAlarmActions(t *testing.T) {
	post := newQueryPoster(t)

	if code, body := post(url.Values{
		"Action": {"PutMetricAlarm"}, "AlarmName": {"a1"}, "Namespace": {"MyApp"}, "MetricName": {"Requests"},
		"ComparisonOperator": {"GreaterThanThreshold"}, "EvaluationPeriods": {"1"}, "Period": {"60"},
		"Threshold": {"10"}, "Statistic": {"Average"},
	}); code != 200 {
		t.Fatalf("PutMetricAlarm: code=%d body=%s", code, body)
	}

	if code, _ := post(url.Values{"Action": {"DisableAlarmActions"}, "AlarmNames.member.1": {"a1"}}); code != 200 {
		t.Fatalf("DisableAlarmActions: code=%d", code)
	}

	code, body := post(url.Values{"Action": {"DescribeAlarms"}, "AlarmNames.member.1": {"a1"}})
	if code != 200 || !strings.Contains(body, "<ActionsEnabled>false</ActionsEnabled>") {
		t.Fatalf("after Disable: code=%d body=%s", code, body)
	}

	if code, _ := post(url.Values{"Action": {"EnableAlarmActions"}, "AlarmNames.member.1": {"a1"}}); code != 200 {
		t.Fatalf("EnableAlarmActions: code=%d", code)
	}

	code, body = post(url.Values{"Action": {"DescribeAlarms"}, "AlarmNames.member.1": {"a1"}})
	if code != 200 || !strings.Contains(body, "<ActionsEnabled>true</ActionsEnabled>") {
		t.Fatalf("after Enable: code=%d body=%s", code, body)
	}
}
