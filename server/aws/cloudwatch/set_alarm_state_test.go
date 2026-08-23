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

// TestSDKSetAlarmState covers the cross-cutting bug: SetAlarmState was dispatched
// on the query/CLI path but not the SDK's rpc-v2-cbor path, so SDK clients got
// UnknownOperationException.
func TestSDKSetAlarmState(t *testing.T) {
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

	client := awscw.NewFromConfig(cfg, func(o *awscw.Options) { o.BaseEndpoint = aws.String(ts.URL) })
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

	if _, err := client.SetAlarmState(ctx, &awscw.SetAlarmStateInput{
		AlarmName:   aws.String("a1"),
		StateValue:  cwtypes.StateValueAlarm,
		StateReason: aws.String("test"),
	}); err != nil {
		t.Fatalf("SetAlarmState (cbor path): %v", err)
	}

	out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"a1"}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	if len(out.MetricAlarms) != 1 || out.MetricAlarms[0].StateValue != cwtypes.StateValueAlarm {
		t.Fatalf("alarm state = %+v, want ALARM", out.MetricAlarms)
	}
}
