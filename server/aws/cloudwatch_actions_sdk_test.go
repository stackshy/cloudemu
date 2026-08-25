package aws_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// sdkConfig builds an aws.Config pointed at ts with static test credentials.
func sdkConfig(t *testing.T, url string) aws.Config {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	require.NoError(t, err)

	cfg.BaseEndpoint = aws.String(url)

	return cfg
}

// TestE2ECloudWatchAlarmActionFiresSNSToSQS is the real-user integration for the
// alarm-action delivery finding: a metric alarm with an SNS AlarmAction must
// publish to the topic on its OK->ALARM transition, and — with an sqs-protocol
// subscription — the notification must land in the subscribed queue.
func TestE2ECloudWatchAlarmActionFiresSNSToSQS(t *testing.T) {
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		CloudWatch: provider.CloudWatch,
		SNS:        provider.SNS,
		SQS:        provider.SQS,
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg := sdkConfig(t, ts.URL)
	ctx := context.Background()

	cw := cloudwatch.NewFromConfig(cfg)
	snsClient := sns.NewFromConfig(cfg)
	sqsClient := sqs.NewFromConfig(cfg)

	topic, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String("alarm-topic")})
	require.NoError(t, err)
	topicARN := aws.ToString(topic.TopicArn)

	queue, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("alarm-queue")})
	require.NoError(t, err)
	queueURL := aws.ToString(queue.QueueUrl)

	attrs, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       queue.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	require.NoError(t, err)
	queueARN := attrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
	require.NotEmpty(t, queueARN)

	_, err = snsClient.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: aws.String(topicARN),
		Protocol: aws.String("sqs"),
		Endpoint: aws.String(queueARN),
	})
	require.NoError(t, err)

	_, err = cw.PutMetricAlarm(ctx, &cloudwatch.PutMetricAlarmInput{
		AlarmName:          aws.String("app-errors"),
		Namespace:          aws.String("Custom/App"),
		MetricName:         aws.String("AppErrors"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(10),
		EvaluationPeriods:  aws.Int32(1),
		Period:             aws.Int32(60),
		Statistic:          cwtypes.StatisticSum,
		AlarmActions:       []string{topicARN},
	})
	require.NoError(t, err)

	// A breaching datapoint drives the alarm INSUFFICIENT_DATA -> ALARM, which
	// must fire the AlarmAction.
	_, err = cw.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
		Namespace: aws.String("Custom/App"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("AppErrors"),
			Value:      aws.Float64(99),
		}},
	})
	require.NoError(t, err)

	// The alarm must be in ALARM (sanity), and the SNS notification must have
	// been delivered to the subscribed SQS queue.
	desc, err := cw.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{AlarmNames: []string{"app-errors"}})
	require.NoError(t, err)
	require.Len(t, desc.MetricAlarms, 1)
	assert.Equal(t, cwtypes.StateValueAlarm, desc.MetricAlarms[0].StateValue)

	msgs, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL),
		MaxNumberOfMessages: 10,
	})
	require.NoError(t, err)
	require.Len(t, msgs.Messages, 1, "alarm AlarmAction should deliver one SNS notification to the queue")
	assert.Contains(t, aws.ToString(msgs.Messages[0].Body), "app-errors")
}

// TestE2ECloudWatchLogsMetricFilterIncrementsMetric is the real-user integration
// for the metric-filter finding: PutMetricFilter must be accepted at the wire
// layer, and log events matching the pattern (published via PutLogEvents) must
// increment the configured custom metric.
func TestE2ECloudWatchLogsMetricFilterIncrementsMetric(t *testing.T) {
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		CloudWatchLogs: provider.CloudWatchLogs,
		CloudWatch:     provider.CloudWatch,
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg := sdkConfig(t, ts.URL)
	ctx := context.Background()

	logs := cwl.NewFromConfig(cfg)
	cw := cloudwatch.NewFromConfig(cfg)

	const (
		group  = "/app/api"
		stream = "instance-1"
	)

	_, err := logs.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)})
	require.NoError(t, err)

	_, err = logs.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
		LogGroupName:  aws.String(group),
		LogStreamName: aws.String(stream),
	})
	require.NoError(t, err)

	_, err = logs.PutMetricFilter(ctx, &cwl.PutMetricFilterInput{
		LogGroupName:  aws.String(group),
		FilterName:    aws.String("errors"),
		FilterPattern: aws.String("ERROR"),
		MetricTransformations: []cwltypes.MetricTransformation{{
			MetricName:      aws.String("ErrorCount"),
			MetricNamespace: aws.String("MyApp/Errors"),
			MetricValue:     aws.String("1"),
		}},
	})
	require.NoError(t, err)

	descMF, err := logs.DescribeMetricFilters(ctx, &cwl.DescribeMetricFiltersInput{
		LogGroupName: aws.String(group),
	})
	require.NoError(t, err)
	require.Len(t, descMF.MetricFilters, 1)
	assert.Equal(t, "errors", aws.ToString(descMF.MetricFilters[0].FilterName))
	require.Len(t, descMF.MetricFilters[0].MetricTransformations, 1)
	assert.Equal(t, "ErrorCount", aws.ToString(descMF.MetricFilters[0].MetricTransformations[0].MetricName))

	nowMillis := time.Now().UnixMilli()
	_, err = logs.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName:  aws.String(group),
		LogStreamName: aws.String(stream),
		LogEvents: []cwltypes.InputLogEvent{
			{Timestamp: aws.Int64(nowMillis), Message: aws.String("INFO started")},
			{Timestamp: aws.Int64(nowMillis + 1), Message: aws.String("ERROR db timeout")},
			{Timestamp: aws.Int64(nowMillis + 2), Message: aws.String("ERROR retry failed")},
		},
	})
	require.NoError(t, err)

	now := time.Now().UTC()
	stats, err := cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace:  aws.String("MyApp/Errors"),
		MetricName: aws.String("ErrorCount"),
		StartTime:  aws.Time(now.Add(-1 * time.Hour)),
		EndTime:    aws.Time(now.Add(1 * time.Hour)),
		Period:     aws.Int32(60),
		Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
	})
	require.NoError(t, err)
	require.Len(t, stats.Datapoints, 1, "the two ERROR lines should publish one datapoint to MyApp/Errors ErrorCount")
	require.NotNil(t, stats.Datapoints[0].Sum)
	assert.InDelta(t, 2.0, *stats.Datapoints[0].Sum, 0.001)
}
