package aws_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestE2ECloudWatchSetAlarmStateFiresSNSToSQS covers F2 end-to-end: forcing an
// alarm to ALARM with SetAlarmState must invoke the alarm's SNS AlarmAction —
// the documented "test my alarm wiring" workflow — delivering to the subscribed
// queue and recording the transition in DescribeAlarmHistory.
func TestE2ECloudWatchSetAlarmStateFiresSNSToSQS(t *testing.T) {
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
		AlarmName:          aws.String("wired"),
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

	// Forcing ALARM must fire the AlarmAction, exactly like an evaluated transition.
	_, err = cw.SetAlarmState(ctx, &cloudwatch.SetAlarmStateInput{
		AlarmName:   aws.String("wired"),
		StateValue:  cwtypes.StateValueAlarm,
		StateReason: aws.String("forced for wiring test"),
	})
	require.NoError(t, err)

	hist, err := cw.DescribeAlarmHistory(ctx, &cloudwatch.DescribeAlarmHistoryInput{
		AlarmName: aws.String("wired"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, hist.AlarmHistoryItems, "SetAlarmState should record a history entry")
	assert.Equal(t, cwtypes.HistoryItemTypeStateUpdate, hist.AlarmHistoryItems[0].HistoryItemType)

	msgs, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL),
		MaxNumberOfMessages: 10,
	})
	require.NoError(t, err)
	require.Len(t, msgs.Messages, 1, "SetAlarmState AlarmAction should deliver one SNS notification to the queue")
	assert.Contains(t, aws.ToString(msgs.Messages[0].Body), "wired")
}
