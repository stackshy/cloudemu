package cloudwatch_test

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// putAlarm creates a fully-specified alarm for the metadata tests.
func putAlarm(t *testing.T, client *awscw.Client, name string) {
	t.Helper()

	_, err := client.PutMetricAlarm(t.Context(), &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String(name),
		AlarmDescription:   aws.String("desc-" + name),
		Namespace:          aws.String("MyApp"),
		MetricName:         aws.String("Errors"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(5),
		EvaluationPeriods:  aws.Int32(3),
		Period:             aws.Int32(60),
		Statistic:          cwtypes.StatisticAverage,
		AlarmActions:       []string{"arn:aws:sns:us-east-1:123456789012:alerts"},
		Dimensions: []cwtypes.Dimension{{
			Name:  aws.String("Service"),
			Value: aws.String("api"),
		}},
	})
	if err != nil {
		t.Fatalf("PutMetricAlarm(%s): %v", name, err)
	}
}

// TestSDKDescribeAlarmsFields covers finding 2: DescribeAlarms must populate the
// full MetricAlarm shape, not just six fields.
func TestSDKDescribeAlarmsFields(t *testing.T) {
	client, ctx := newCWClient(t)
	putAlarm(t, client, "a1")

	out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"a1"}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}

	if len(out.MetricAlarms) != 1 {
		t.Fatalf("alarms = %+v, want 1", out.MetricAlarms)
	}

	a := out.MetricAlarms[0]
	if aws.ToString(a.AlarmArn) == "" {
		t.Errorf("AlarmArn empty")
	}
	if aws.ToString(a.AlarmDescription) != "desc-a1" {
		t.Errorf("AlarmDescription = %q, want desc-a1", aws.ToString(a.AlarmDescription))
	}
	if aws.ToInt32(a.Period) != 60 {
		t.Errorf("Period = %d, want 60", aws.ToInt32(a.Period))
	}
	if aws.ToInt32(a.EvaluationPeriods) != 3 {
		t.Errorf("EvaluationPeriods = %d, want 3", aws.ToInt32(a.EvaluationPeriods))
	}
	if a.Statistic != cwtypes.StatisticAverage {
		t.Errorf("Statistic = %q, want Average", a.Statistic)
	}
	if !aws.ToBool(a.ActionsEnabled) {
		t.Errorf("ActionsEnabled = false, want true (default)")
	}
	if len(a.AlarmActions) != 1 {
		t.Errorf("AlarmActions = %+v, want 1", a.AlarmActions)
	}
	if a.StateUpdatedTimestamp == nil || a.StateUpdatedTimestamp.IsZero() {
		t.Errorf("StateUpdatedTimestamp = %v, want non-zero", a.StateUpdatedTimestamp)
	}
	if len(a.Dimensions) != 1 || aws.ToString(a.Dimensions[0].Name) != "Service" {
		t.Errorf("Dimensions = %+v, want Service=api", a.Dimensions)
	}
}

// TestSDKDescribeAlarmsFilters covers finding 5: AlarmNamePrefix / StateValue /
// MaxRecords must be honored server-side.
func TestSDKDescribeAlarmsFilters(t *testing.T) {
	client, ctx := newCWClient(t)
	putAlarm(t, client, "prod-a")
	putAlarm(t, client, "prod-b")
	putAlarm(t, client, "dev-a")

	byPrefix, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{
		AlarmNamePrefix: aws.String("prod-"),
	})
	if err != nil {
		t.Fatalf("DescribeAlarms prefix: %v", err)
	}
	if len(byPrefix.MetricAlarms) != 2 {
		t.Fatalf("prefix filter = %d alarms, want 2", len(byPrefix.MetricAlarms))
	}

	// Force one alarm into ALARM and filter by state.
	if _, err := client.SetAlarmState(ctx, &awscw.SetAlarmStateInput{
		AlarmName:   aws.String("prod-a"),
		StateValue:  cwtypes.StateValueAlarm,
		StateReason: aws.String("test"),
	}); err != nil {
		t.Fatalf("SetAlarmState: %v", err)
	}

	byState, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{
		StateValue: cwtypes.StateValueAlarm,
	})
	if err != nil {
		t.Fatalf("DescribeAlarms state: %v", err)
	}
	if len(byState.MetricAlarms) != 1 || aws.ToString(byState.MetricAlarms[0].AlarmName) != "prod-a" {
		t.Fatalf("state filter = %+v, want only prod-a", byState.MetricAlarms)
	}

	capped, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{MaxRecords: aws.Int32(2)})
	if err != nil {
		t.Fatalf("DescribeAlarms maxrecords: %v", err)
	}
	if len(capped.MetricAlarms) != 2 {
		t.Fatalf("maxrecords = %d alarms, want 2", len(capped.MetricAlarms))
	}
}

// TestSDKDescribeAlarmsPagination guards that DescribeAlarms honors MaxRecords
// and hands back a NextToken the caller can follow. Without pagination the first
// page returns everything and NextToken is empty, wedging the SDK paginator.
func TestSDKDescribeAlarmsPagination(t *testing.T) {
	client, ctx := newCWClient(t)
	putAlarm(t, client, "pg-a")
	putAlarm(t, client, "pg-b")
	putAlarm(t, client, "pg-c")

	first, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{MaxRecords: aws.Int32(2)})
	if err != nil {
		t.Fatalf("DescribeAlarms page 1: %v", err)
	}

	if len(first.MetricAlarms) != 2 {
		t.Fatalf("page 1 = %d alarms, want 2 (MaxRecords ignored?)", len(first.MetricAlarms))
	}

	if aws.ToString(first.NextToken) == "" {
		t.Fatalf("page 1 NextToken empty, want a token for the third alarm")
	}

	second, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{
		MaxRecords: aws.Int32(2),
		NextToken:  first.NextToken,
	})
	if err != nil {
		t.Fatalf("DescribeAlarms page 2: %v", err)
	}

	if len(second.MetricAlarms) != 1 || aws.ToString(second.MetricAlarms[0].AlarmName) != "pg-c" {
		t.Fatalf("page 2 = %+v, want one alarm pg-c", second.MetricAlarms)
	}

	if aws.ToString(second.NextToken) != "" {
		t.Fatalf("page 2 NextToken = %q, want empty (pagination finished)", aws.ToString(second.NextToken))
	}
}

// TestSDKDescribeAlarmHistory covers finding 4: transition history recorded on
// state changes must be readable via DescribeAlarmHistory.
func TestSDKDescribeAlarmHistory(t *testing.T) {
	client, ctx := newCWClient(t)
	putAlarm(t, client, "h1")

	if _, err := client.SetAlarmState(ctx, &awscw.SetAlarmStateInput{
		AlarmName:   aws.String("h1"),
		StateValue:  cwtypes.StateValueAlarm,
		StateReason: aws.String("crossed"),
	}); err != nil {
		t.Fatalf("SetAlarmState: %v", err)
	}

	// A metric that keeps it in ALARM records a transition into history.
	now := time.Now().UTC()
	if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("MyApp"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("Errors"),
			Value:      aws.Float64(0),
			Dimensions: []cwtypes.Dimension{{Name: aws.String("Service"), Value: aws.String("api")}},
			Timestamp:  aws.Time(now),
		}},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	out, err := client.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{
		AlarmName: aws.String("h1"),
	})
	if err != nil {
		t.Fatalf("DescribeAlarmHistory: %v", err)
	}

	if len(out.AlarmHistoryItems) == 0 {
		t.Fatalf("history empty, want at least one StateUpdate entry")
	}
	if out.AlarmHistoryItems[0].HistoryItemType != cwtypes.HistoryItemTypeStateUpdate {
		t.Fatalf("history type = %q, want StateUpdate", out.AlarmHistoryItems[0].HistoryItemType)
	}
	if aws.ToString(out.AlarmHistoryItems[0].AlarmName) != "h1" {
		t.Fatalf("history alarm = %q, want h1", aws.ToString(out.AlarmHistoryItems[0].AlarmName))
	}
}

// TestSDKDescribeAlarmsForMetric covers finding 7: DescribeAlarmsForMetric must
// be dispatched and filter by metric.
func TestSDKDescribeAlarmsForMetric(t *testing.T) {
	client, ctx := newCWClient(t)
	putAlarm(t, client, "m1")

	out, err := client.DescribeAlarmsForMetric(ctx, &awscw.DescribeAlarmsForMetricInput{
		Namespace:  aws.String("MyApp"),
		MetricName: aws.String("Errors"),
		Dimensions: []cwtypes.Dimension{{Name: aws.String("Service"), Value: aws.String("api")}},
	})
	if err != nil {
		t.Fatalf("DescribeAlarmsForMetric: %v", err)
	}

	if len(out.MetricAlarms) != 1 || aws.ToString(out.MetricAlarms[0].AlarmName) != "m1" {
		t.Fatalf("alarms = %+v, want only m1", out.MetricAlarms)
	}

	// A different metric returns nothing.
	none, err := client.DescribeAlarmsForMetric(ctx, &awscw.DescribeAlarmsForMetricInput{
		Namespace:  aws.String("MyApp"),
		MetricName: aws.String("Other"),
	})
	if err != nil {
		t.Fatalf("DescribeAlarmsForMetric(Other): %v", err)
	}
	if len(none.MetricAlarms) != 0 {
		t.Fatalf("alarms for Other = %+v, want none", none.MetricAlarms)
	}
}

// TestSDKEnableDisableAlarmActions covers finding 7: EnableAlarmActions /
// DisableAlarmActions must toggle ActionsEnabled.
func TestSDKEnableDisableAlarmActions(t *testing.T) {
	client, ctx := newCWClient(t)
	putAlarm(t, client, "act")

	if _, err := client.DisableAlarmActions(ctx, &awscw.DisableAlarmActionsInput{
		AlarmNames: []string{"act"},
	}); err != nil {
		t.Fatalf("DisableAlarmActions: %v", err)
	}

	out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"act"}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	if aws.ToBool(out.MetricAlarms[0].ActionsEnabled) {
		t.Fatalf("ActionsEnabled = true after Disable, want false")
	}

	if _, err := client.EnableAlarmActions(ctx, &awscw.EnableAlarmActionsInput{
		AlarmNames: []string{"act"},
	}); err != nil {
		t.Fatalf("EnableAlarmActions: %v", err)
	}

	out, err = client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"act"}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	if !aws.ToBool(out.MetricAlarms[0].ActionsEnabled) {
		t.Fatalf("ActionsEnabled = false after Enable, want true")
	}
}

// TestSDKAlarmTags covers finding 7: PutMetricAlarm Tags plus the
// Tag/Untag/ListTags operations must round-trip.
func TestSDKAlarmTags(t *testing.T) {
	client, ctx := newCWClient(t)

	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("tagged"),
		Namespace:          aws.String("MyApp"),
		MetricName:         aws.String("Errors"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(1),
		EvaluationPeriods:  aws.Int32(1),
		Period:             aws.Int32(60),
		Statistic:          cwtypes.StatisticAverage,
		Tags: []cwtypes.Tag{{
			Key:   aws.String("team"),
			Value: aws.String("payments"),
		}},
	}); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}

	da, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"tagged"}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	arn := aws.ToString(da.MetricAlarms[0].AlarmArn)
	if arn == "" {
		t.Fatalf("alarm has no ARN")
	}

	list, err := client.ListTagsForResource(ctx, &awscw.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}
	if !hasTag(list.Tags, "team", "payments") {
		t.Fatalf("tags = %+v, want team=payments (dropped on PutMetricAlarm)", list.Tags)
	}

	if _, err := client.TagResource(ctx, &awscw.TagResourceInput{
		ResourceARN: aws.String(arn),
		Tags:        []cwtypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	if _, err := client.UntagResource(ctx, &awscw.UntagResourceInput{
		ResourceARN: aws.String(arn),
		TagKeys:     []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	list, err = client.ListTagsForResource(ctx, &awscw.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}
	if hasTag(list.Tags, "team", "payments") {
		t.Fatalf("team tag still present after Untag: %+v", list.Tags)
	}
	if !hasTag(list.Tags, "env", "prod") {
		t.Fatalf("env tag missing after Tag: %+v", list.Tags)
	}
}

func hasTag(tags []cwtypes.Tag, key, value string) bool {
	for _, tag := range tags {
		if aws.ToString(tag.Key) == key && aws.ToString(tag.Value) == value {
			return true
		}
	}

	return false
}
