// notification_config_sdk_test.go — real aws-sdk-go-v2 tests for bucket event
// notification configuration. Queue (SQS), Topic (SNS), and Lambda function
// destinations, plus an S3Key prefix/suffix filter, must round-trip through
// PutBucketNotificationConfiguration / GetBucketNotificationConfiguration
// (previously Topic/Lambda configs and filters were silently dropped).
package s3_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestS3BucketNotificationConfigRoundTrip(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "notif-roundtrip"
	suiteCreateBucket(t, client, bucket)

	const (
		queueARN  = "arn:aws:sqs:us-east-1:000000000000:s3-events"
		topicARN  = "arn:aws:sns:us-east-1:000000000000:s3-topic"
		lambdaARN = "arn:aws:lambda:us-east-1:000000000000:function:s3-fn"
	)

	_, err := client.PutBucketNotificationConfiguration(ctx, &s3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String(bucket),
		NotificationConfiguration: &types.NotificationConfiguration{
			QueueConfigurations: []types.QueueConfiguration{{
				Id:       aws.String("q1"),
				QueueArn: aws.String(queueARN),
				Events:   []types.Event{types.EventS3ObjectCreatedPut},
				Filter: &types.NotificationConfigurationFilter{
					Key: &types.S3KeyFilter{FilterRules: []types.FilterRule{
						{Name: types.FilterRuleNamePrefix, Value: aws.String("images/")},
						{Name: types.FilterRuleNameSuffix, Value: aws.String(".jpg")},
					}},
				},
			}},
			TopicConfigurations: []types.TopicConfiguration{{
				Id:       aws.String("t1"),
				TopicArn: aws.String(topicARN),
				Events:   []types.Event{types.EventS3ObjectCreated},
			}},
			LambdaFunctionConfigurations: []types.LambdaFunctionConfiguration{{
				Id:                aws.String("l1"),
				LambdaFunctionArn: aws.String(lambdaARN),
				Events:            []types.Event{types.EventS3ObjectRemoved},
			}},
		},
	})
	require.NoError(t, err)

	got, err := client.GetBucketNotificationConfiguration(ctx, &s3.GetBucketNotificationConfigurationInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// Queue configuration with its S3Key filter.
	require.Len(t, got.QueueConfigurations, 1)
	q := got.QueueConfigurations[0]
	assert.Equal(t, "q1", aws.ToString(q.Id))
	assert.Equal(t, queueARN, aws.ToString(q.QueueArn))
	assert.Equal(t, []types.Event{types.EventS3ObjectCreatedPut}, q.Events)
	require.NotNil(t, q.Filter)
	require.NotNil(t, q.Filter.Key)
	require.Len(t, q.Filter.Key.FilterRules, 2)
	assert.Equal(t, types.FilterRuleNamePrefix, q.Filter.Key.FilterRules[0].Name)
	assert.Equal(t, "images/", aws.ToString(q.Filter.Key.FilterRules[0].Value))
	assert.Equal(t, types.FilterRuleNameSuffix, q.Filter.Key.FilterRules[1].Name)
	assert.Equal(t, ".jpg", aws.ToString(q.Filter.Key.FilterRules[1].Value))

	// Topic (SNS) configuration — previously dropped.
	require.Len(t, got.TopicConfigurations, 1)
	assert.Equal(t, "t1", aws.ToString(got.TopicConfigurations[0].Id))
	assert.Equal(t, topicARN, aws.ToString(got.TopicConfigurations[0].TopicArn))
	assert.Equal(t, []types.Event{types.EventS3ObjectCreated}, got.TopicConfigurations[0].Events)

	// Lambda function configuration — previously dropped.
	require.Len(t, got.LambdaFunctionConfigurations, 1)
	assert.Equal(t, "l1", aws.ToString(got.LambdaFunctionConfigurations[0].Id))
	assert.Equal(t, lambdaARN, aws.ToString(got.LambdaFunctionConfigurations[0].LambdaFunctionArn))
	assert.Equal(t, []types.Event{types.EventS3ObjectRemoved}, got.LambdaFunctionConfigurations[0].Events)
}
