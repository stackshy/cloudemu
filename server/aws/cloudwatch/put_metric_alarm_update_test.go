package cloudwatch_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/stackshy/cloudemu/v2/config"
	cwprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	cwserver "github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
)

func newCloudWatchClient(t *testing.T) *awscw.Client {
	t.Helper()

	h := cwserver.New(cwprovider.New(config.NewOptions()))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	return awscw.NewFromConfig(cfg, func(o *awscw.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

// TestSDKPutMetricAlarmUpdatePreservesState verifies the real AWS contract:
// "When you update an existing alarm, its state is left unchanged, but the
// update completely overwrites the previous configuration of the alarm."
// A re-issued PutMetricAlarm on an existing alarm must not reset StateValue or
// StateUpdatedTimestamp back to INSUFFICIENT_DATA.
func TestSDKPutMetricAlarmUpdatePreservesState(t *testing.T) {
	client := newCloudWatchClient(t)
	ctx := context.Background()

	put := func(threshold float64) {
		if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
			AlarmName:          aws.String("a1"),
			ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
			EvaluationPeriods:  aws.Int32(1),
			MetricName:         aws.String("m"),
			Namespace:          aws.String("ns"),
			Period:             aws.Int32(60),
			Threshold:          aws.Float64(threshold),
			Statistic:          cwtypes.StatisticAverage,
		}); err != nil {
			t.Fatalf("PutMetricAlarm: %v", err)
		}
	}

	put(1)

	if _, err := client.SetAlarmState(ctx, &awscw.SetAlarmStateInput{
		AlarmName:   aws.String("a1"),
		StateValue:  cwtypes.StateValueAlarm,
		StateReason: aws.String("test"),
	}); err != nil {
		t.Fatalf("SetAlarmState: %v", err)
	}

	before, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"a1"}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	if len(before.MetricAlarms) != 1 {
		t.Fatalf("want 1 alarm, got %d", len(before.MetricAlarms))
	}
	stampBefore := before.MetricAlarms[0].StateUpdatedTimestamp

	// Update the alarm's configuration (new threshold) via PutMetricAlarm.
	put(99)

	after, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"a1"}})
	if err != nil {
		t.Fatalf("DescribeAlarms after update: %v", err)
	}
	if len(after.MetricAlarms) != 1 {
		t.Fatalf("want 1 alarm after update, got %d", len(after.MetricAlarms))
	}
	got := after.MetricAlarms[0]

	if got.StateValue != cwtypes.StateValueAlarm {
		t.Fatalf("state after update = %s, want ALARM (state must be left unchanged)", got.StateValue)
	}
	if got.Threshold == nil || *got.Threshold != 99 {
		t.Fatalf("threshold after update = %v, want 99 (config must be overwritten)", got.Threshold)
	}
	if stampBefore == nil || got.StateUpdatedTimestamp == nil || !got.StateUpdatedTimestamp.Equal(*stampBefore) {
		t.Fatalf("StateUpdatedTimestamp changed on update: before=%v after=%v", stampBefore, got.StateUpdatedTimestamp)
	}
}

// TestSDKPutMetricAlarmUpdateIgnoresTags verifies that tags attached via
// TagResource survive a PutMetricAlarm update, and that tags supplied on an
// update are ignored (real AWS: "If you are using this operation to update an
// existing alarm, any tags you specify in this parameter are ignored.").
func TestSDKPutMetricAlarmUpdateIgnoresTags(t *testing.T) {
	client := newCloudWatchClient(t)
	ctx := context.Background()

	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("a1"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		EvaluationPeriods:  aws.Int32(1),
		MetricName:         aws.String("m"),
		Namespace:          aws.String("ns"),
		Period:             aws.Int32(60),
		Threshold:          aws.Float64(1),
		Statistic:          cwtypes.StatisticAverage,
	}); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}

	desc, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"a1"}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	arn := aws.ToString(desc.MetricAlarms[0].AlarmArn)

	if _, err := client.TagResource(ctx, &awscw.TagResourceInput{
		ResourceARN: aws.String(arn),
		Tags:        []cwtypes.Tag{{Key: aws.String("team"), Value: aws.String("payments")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	// Update the alarm, supplying a different tag set that must be ignored.
	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("a1"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		EvaluationPeriods:  aws.Int32(1),
		MetricName:         aws.String("m"),
		Namespace:          aws.String("ns"),
		Period:             aws.Int32(60),
		Threshold:          aws.Float64(2),
		Statistic:          cwtypes.StatisticAverage,
		Tags:               []cwtypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("PutMetricAlarm update: %v", err)
	}

	tags, err := client.ListTagsForResource(ctx, &awscw.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	got := map[string]string{}
	for _, tag := range tags.Tags {
		got[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	if got["team"] != "payments" {
		t.Fatalf("tag from TagResource lost after update: %v", got)
	}
	if _, ok := got["env"]; ok {
		t.Fatalf("tag supplied on update must be ignored, but present: %v", got)
	}
}
