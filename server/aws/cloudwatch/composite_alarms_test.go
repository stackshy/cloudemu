package cloudwatch_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// TestSDKCompositeAlarmRoundTrip drives the aws-sdk-go-v2 client through the
// aws_cloudwatch_composite_alarm flow: PutCompositeAlarm, then DescribeAlarms
// surfacing it in the CompositeAlarms list (separate from MetricAlarms).
func TestSDKCompositeAlarmRoundTrip(t *testing.T) {
	client, ctx := newCWClient(t)

	rule := `ALARM("cpu-high") OR ALARM("mem-high")`

	putChildAlarms(t, client, "cpu-high", "mem-high")

	if _, err := client.PutCompositeAlarm(ctx, &awscw.PutCompositeAlarmInput{
		AlarmName:        aws.String("app-unhealthy"),
		AlarmRule:        aws.String(rule),
		AlarmDescription: aws.String("app is unhealthy"),
		AlarmActions:     []string{"arn:aws:sns:us-east-1:123456789012:ops"},
	}); err != nil {
		t.Fatalf("PutCompositeAlarm: %v", err)
	}

	out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm, cwtypes.AlarmTypeCompositeAlarm}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}

	if len(out.CompositeAlarms) != 1 {
		t.Fatalf("CompositeAlarms = %d, want 1", len(out.CompositeAlarms))
	}
	c := out.CompositeAlarms[0]
	if aws.ToString(c.AlarmName) != "app-unhealthy" {
		t.Fatalf("AlarmName = %q, want app-unhealthy", aws.ToString(c.AlarmName))
	}
	if aws.ToString(c.AlarmRule) != rule {
		t.Fatalf("AlarmRule = %q, want %q", aws.ToString(c.AlarmRule), rule)
	}
	if len(c.AlarmActions) != 1 || c.AlarmActions[0] != "arn:aws:sns:us-east-1:123456789012:ops" {
		t.Fatalf("AlarmActions = %v, want the one SNS action", c.AlarmActions)
	}
	if aws.ToString(c.AlarmArn) == "" {
		t.Fatal("AlarmArn is empty")
	}
	// Evaluated at creation: neither child is in ALARM, so the rule is false.
	if c.StateValue != cwtypes.StateValueOk {
		t.Fatalf("StateValue = %q, want OK", c.StateValue)
	}
}

// TestSDKDescribeAlarmsAlarmTypesFilter confirms the AlarmTypes filter selects
// composite vs metric alarms, so a request for one type excludes the other.
func TestSDKDescribeAlarmsAlarmTypesFilter(t *testing.T) {
	client, ctx := newCWClient(t)

	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("cpu-high"),
		Namespace:          aws.String("AWS/EC2"),
		MetricName:         aws.String("CPUUtilization"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(80),
		EvaluationPeriods:  aws.Int32(1),
		Period:             aws.Int32(60),
		Statistic:          cwtypes.StatisticAverage,
	}); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}

	if _, err := client.PutCompositeAlarm(ctx, &awscw.PutCompositeAlarmInput{
		AlarmName: aws.String("app-unhealthy"),
		AlarmRule: aws.String(`ALARM("cpu-high")`),
	}); err != nil {
		t.Fatalf("PutCompositeAlarm: %v", err)
	}

	// Composite only.
	comp, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeCompositeAlarm},
	})
	if err != nil {
		t.Fatalf("DescribeAlarms composite: %v", err)
	}
	if len(comp.CompositeAlarms) != 1 || len(comp.MetricAlarms) != 0 {
		t.Fatalf("composite-only filter: MetricAlarms=%d CompositeAlarms=%d, want 0/1",
			len(comp.MetricAlarms), len(comp.CompositeAlarms))
	}

	// Metric only.
	metr, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm},
	})
	if err != nil {
		t.Fatalf("DescribeAlarms metric: %v", err)
	}
	if len(metr.MetricAlarms) != 1 || len(metr.CompositeAlarms) != 0 {
		t.Fatalf("metric-only filter: MetricAlarms=%d CompositeAlarms=%d, want 1/0",
			len(metr.MetricAlarms), len(metr.CompositeAlarms))
	}

	// Metric alarms only when unset, as the DescribeAlarms API documents.
	unset, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{})
	if err != nil {
		t.Fatalf("DescribeAlarms unset: %v", err)
	}
	if len(unset.MetricAlarms) != 1 || len(unset.CompositeAlarms) != 0 {
		t.Fatalf("no filter: MetricAlarms=%d CompositeAlarms=%d, want 1/0",
			len(unset.MetricAlarms), len(unset.CompositeAlarms))
	}

	both, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm, cwtypes.AlarmTypeCompositeAlarm}})
	if err != nil {
		t.Fatalf("DescribeAlarms both: %v", err)
	}
	if len(both.MetricAlarms) != 1 || len(both.CompositeAlarms) != 1 {
		t.Fatalf("both types: MetricAlarms=%d CompositeAlarms=%d, want 1/1",
			len(both.MetricAlarms), len(both.CompositeAlarms))
	}
}

// TestSDKDeleteAlarmsDeletesComposite confirms DeleteAlarms removes composite
// alarms too (they share the DeleteAlarms operation with metric alarms).
func TestSDKDeleteAlarmsDeletesComposite(t *testing.T) {
	client, ctx := newCWClient(t)

	putChildAlarms(t, client, "cpu-high")

	if _, err := client.PutCompositeAlarm(ctx, &awscw.PutCompositeAlarmInput{
		AlarmName: aws.String("app-unhealthy"),
		AlarmRule: aws.String(`ALARM("cpu-high")`),
	}); err != nil {
		t.Fatalf("PutCompositeAlarm: %v", err)
	}

	if _, err := client.DeleteAlarms(ctx, &awscw.DeleteAlarmsInput{
		AlarmNames: []string{"app-unhealthy"},
	}); err != nil {
		t.Fatalf("DeleteAlarms: %v", err)
	}

	out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm, cwtypes.AlarmTypeCompositeAlarm}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	if len(out.CompositeAlarms) != 0 {
		t.Fatalf("CompositeAlarms after delete = %d, want 0", len(out.CompositeAlarms))
	}
}

// putChildAlarms creates metric alarms for a composite rule to reference.
func putChildAlarms(t *testing.T, client *awscw.Client, names ...string) {
	t.Helper()

	for _, name := range names {
		if _, err := client.PutMetricAlarm(context.Background(), &awscw.PutMetricAlarmInput{
			AlarmName:          aws.String(name),
			Namespace:          aws.String("Child/App"),
			MetricName:         aws.String(name),
			Statistic:          cwtypes.StatisticMaximum,
			Period:             aws.Int32(60),
			EvaluationPeriods:  aws.Int32(1),
			Threshold:          aws.Float64(5),
			ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		}); err != nil {
			t.Fatalf("PutMetricAlarm %s: %v", name, err)
		}
	}
}
