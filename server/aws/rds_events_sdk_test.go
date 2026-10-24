package aws_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestE2ERDSEventSubscriptionDeliversToSNSToSQS drives the RDS event pipeline
// the way a real user does: an RDS event subscription on an SNS topic that an
// SQS queue subscribes to must deliver the RDS lifecycle notification to the
// queue, and DescribeEvents must report the same event.
func TestE2ERDSEventSubscriptionDeliversToSNSToSQS(t *testing.T) {
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		RDS: provider.RDS,
		SNS: provider.SNS,
		SQS: provider.SQS,
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg := sdkConfig(t, ts.URL)
	ctx := context.Background()

	rdsClient := awsrds.NewFromConfig(cfg)
	snsClient := sns.NewFromConfig(cfg)
	sqsClient := sqs.NewFromConfig(cfg)

	topic, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String("rds-events")})
	require.NoError(t, err)

	queue, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("rds-events-q")})
	require.NoError(t, err)

	attrs, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       queue.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	require.NoError(t, err)

	_, err = snsClient.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn:   topic.TopicArn,
		Protocol:   aws.String("sqs"),
		Endpoint:   aws.String(attrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]),
		Attributes: map[string]string{"RawMessageDelivery": "true"},
	})
	require.NoError(t, err)

	_, err = rdsClient.CreateEventSubscription(ctx, &awsrds.CreateEventSubscriptionInput{
		SubscriptionName: aws.String("db-lifecycle"),
		SnsTopicArn:      topic.TopicArn,
		SourceType:       aws.String("db-instance"),
		EventCategories:  []string{"creation", "deletion"},
	})
	require.NoError(t, err)

	_, err = rdsClient.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("evdb"),
		Engine:               aws.String("postgres"),
		EngineVersion:        aws.String("15.4"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		MasterUsername:       aws.String("admin"),
		MasterUserPassword:   aws.String("password123"),
		AllocatedStorage:     aws.Int32(20),
	})
	require.NoError(t, err)

	msgs, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl: queue.QueueUrl, MaxNumberOfMessages: 10,
	})
	require.NoError(t, err)
	require.Len(t, msgs.Messages, 1, "creation event should reach the queue")

	var body map[string]string
	require.NoError(t, json.Unmarshal([]byte(aws.ToString(msgs.Messages[0].Body)), &body))
	assert.Equal(t, "db-instance", body["Event Source"])
	assert.Equal(t, "evdb", body["Source ID"])
	assert.Equal(t, "DB instance created", body["Event Message"])
	assert.Equal(t, "http://docs.amazonwebservices.com/AmazonRDS/latest/UserGuide/USER_Events.html#RDS-EVENT-0005", body["Event ID"])

	events, err := rdsClient.DescribeEvents(ctx, &awsrds.DescribeEventsInput{
		SourceType:       rdstypes.SourceTypeDbInstance,
		SourceIdentifier: aws.String("evdb"),
	})
	require.NoError(t, err)
	require.Len(t, events.Events, 1)

	ev := events.Events[0]
	assert.Equal(t, "DB instance created", aws.ToString(ev.Message))
	assert.Equal(t, []string{"creation"}, ev.EventCategories)
	assert.Equal(t, rdstypes.SourceTypeDbInstance, ev.SourceType)
	assert.Equal(t, "arn:aws:rds:us-east-1:123456789012:db:evdb", aws.ToString(ev.SourceArn))
	assert.NotNil(t, ev.Date)

	// The instance's default parameter group follows its 15.4 version family.
	inst, err := rdsClient.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{DBInstanceIdentifier: aws.String("evdb")})
	require.NoError(t, err)
	require.Len(t, inst.DBInstances[0].DBParameterGroups, 1)
	assert.Equal(t, "default.postgres15", aws.ToString(inst.DBInstances[0].DBParameterGroups[0].DBParameterGroupName))

	// A disabled subscription stops delivery, while DescribeEvents still logs.
	_, err = rdsClient.ModifyEventSubscription(ctx, &awsrds.ModifyEventSubscriptionInput{
		SubscriptionName: aws.String("db-lifecycle"), Enabled: aws.Bool(false),
	})
	require.NoError(t, err)

	_, err = rdsClient.DeleteDBInstance(ctx, &awsrds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String("evdb"), SkipFinalSnapshot: aws.Bool(true),
	})
	require.NoError(t, err)

	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl: queue.QueueUrl, ReceiptHandle: msgs.Messages[0].ReceiptHandle,
	})
	require.NoError(t, err)

	again, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: queue.QueueUrl, MaxNumberOfMessages: 10})
	require.NoError(t, err)
	assert.Empty(t, again.Messages, "disabled subscription must not deliver")

	deletions, err := rdsClient.DescribeEvents(ctx, &awsrds.DescribeEventsInput{EventCategories: []string{"deletion"}})
	require.NoError(t, err)
	require.Len(t, deletions.Events, 1)
	assert.Equal(t, "DB instance deleted", aws.ToString(deletions.Events[0].Message))
}
