package cloudwatch_test

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/smithy-go"
)

// The suppressor fields round-trip through PutCompositeAlarm and
// DescribeAlarms, so aws_cloudwatch_composite_alarm shows no drift.
func TestSDKCompositeSuppressorRoundTrip(t *testing.T) {
	client, ctx := newCWClient(t)
	putChildAlarms(t, client, "cpu", "maint")

	if _, err := client.PutCompositeAlarm(ctx, &awscw.PutCompositeAlarmInput{
		AlarmName:                        aws.String("svc"),
		AlarmRule:                        aws.String("ALARM(cpu)"),
		ActionsSuppressor:                aws.String("maint"),
		ActionsSuppressorWaitPeriod:      aws.Int32(60),
		ActionsSuppressorExtensionPeriod: aws.Int32(90),
	}); err != nil {
		t.Fatalf("PutCompositeAlarm: %v", err)
	}

	out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"svc"}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}

	c := out.CompositeAlarms[0]
	if aws.ToString(c.ActionsSuppressor) != "maint" || aws.ToInt32(c.ActionsSuppressorWaitPeriod) != 60 ||
		aws.ToInt32(c.ActionsSuppressorExtensionPeriod) != 90 {
		t.Fatalf("suppressor = %v/%v/%v", aws.ToString(c.ActionsSuppressor),
			aws.ToInt32(c.ActionsSuppressorWaitPeriod), aws.ToInt32(c.ActionsSuppressorExtensionPeriod))
	}

	if c.StateTransitionedTimestamp == nil || c.AlarmConfigurationUpdatedTimestamp == nil || aws.ToString(c.StateReasonData) == "" {
		t.Fatal("computed composite fields are missing")
	}
}

func requireValidationError(t *testing.T, err error, msg string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationError" || !strings.Contains(apiErr.ErrorMessage(), msg) {
		t.Fatalf("want ValidationError containing %q, got %v", msg, err)
	}
}

// Before, DeleteAlarms deleted a referenced child and several composites, and
// PutCompositeAlarm accepted a rule over missing alarms.
func TestSDKCompositeGuards(t *testing.T) {
	client, ctx := newCWClient(t)
	putChildAlarms(t, client, "a")

	_, err := client.PutCompositeAlarm(ctx, &awscw.PutCompositeAlarmInput{
		AlarmName: aws.String("x"), AlarmRule: aws.String("ALARM(a) OR ALARM(nope)"),
	})
	requireValidationError(t, err, "Could not save the composite alarm as alarms [nope] in the alarm rule do not exist")

	for _, name := range []string{"p1", "p2"} {
		if _, err := client.PutCompositeAlarm(ctx, &awscw.PutCompositeAlarmInput{
			AlarmName: aws.String(name), AlarmRule: aws.String("ALARM(a)"),
		}); err != nil {
			t.Fatalf("PutCompositeAlarm %s: %v", name, err)
		}
	}

	_, err = client.DeleteAlarms(ctx, &awscw.DeleteAlarmsInput{AlarmNames: []string{"a"}})
	requireValidationError(t, err, "Cannot delete a as there are composite alarm(s) depending on it.")

	_, err = client.DeleteAlarms(ctx, &awscw.DeleteAlarmsInput{AlarmNames: []string{"p1", "p2"}})
	requireValidationError(t, err, "at most one composite alarm")

	out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{})
	if err != nil {
		t.Fatal(err)
	}

	if len(out.MetricAlarms) != 1 || len(out.CompositeAlarms) != 2 {
		t.Fatalf("a refused delete removed alarms: %d metric, %d composite", len(out.MetricAlarms), len(out.CompositeAlarms))
	}
}

func TestQueryCompositeGuards(t *testing.T) {
	post := newQueryPoster(t)

	if code, body := post(url.Values{
		"Action": {"PutMetricAlarm"}, "AlarmName": {"a"}, "Namespace": {"MyApp"}, "MetricName": {"A"},
		"ComparisonOperator": {"GreaterThanThreshold"}, "EvaluationPeriods": {"1"}, "Period": {"60"},
		"Threshold": {"10"}, "Statistic": {"Average"},
	}); code != 200 {
		t.Fatalf("PutMetricAlarm: %d %s", code, body)
	}

	if code, body := post(url.Values{
		"Action": {"PutCompositeAlarm"}, "AlarmName": {"svc"}, "AlarmRule": {"ALARM(a)"},
		"ActionsSuppressor": {"a"}, "ActionsSuppressorWaitPeriod": {"60"}, "ActionsSuppressorExtensionPeriod": {"90"},
	}); code != 200 {
		t.Fatalf("PutCompositeAlarm: %d %s", code, body)
	}

	_, body := post(url.Values{"Action": {"DescribeAlarms"}, "AlarmTypes.member.1": {"CompositeAlarm"}})
	for _, want := range []string{
		"<ActionsSuppressor>a</ActionsSuppressor>",
		"<ActionsSuppressorWaitPeriod>60</ActionsSuppressorWaitPeriod>",
		"<ActionsSuppressorExtensionPeriod>90</ActionsSuppressorExtensionPeriod>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("DescribeAlarms missing %s: %s", want, body)
		}
	}

	code, body := post(url.Values{"Action": {"DeleteAlarms"}, "AlarmNames.member.1": {"a"}})
	if code != 400 || !strings.Contains(body, "<Code>ValidationError</Code>") ||
		!strings.Contains(body, "Cannot delete a as there are composite alarm(s) depending on it.") {
		t.Fatalf("DeleteAlarms referenced: %d %s", code, body)
	}

	code, body = post(url.Values{
		"Action": {"PutCompositeAlarm"}, "AlarmName": {"bad"}, "AlarmRule": {"ALARM(a)"}, "ActionsSuppressor": {"a"},
	})
	if code != 400 || !strings.Contains(body, "<Code>ValidationError</Code>") {
		t.Fatalf("suppressor without periods: %d %s", code, body)
	}
}
