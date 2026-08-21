package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const (
	notificationService = "notification"
	snsTopicName        = "compat-topic"
	snsProtocol         = "email"
	snsEndpoint         = "ops@example.com"
	snsDisplayName      = "Compat Topic"
)

// TestSNSCompat drives a real aws-sdk-go-v2 SNS client against CloudEmu's
// in-process wire server and records one compat result per portable
// notification op (provider AWS = SNS).
func TestSNSCompat(t *testing.T) {
	cloud := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{SNS: cloud.SNS})

	client := awssns.NewFromConfig(sess.Config(), func(o *awssns.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})

	ctx := context.Background()

	var topicArn, subArn string

	sess.Op(notificationService, "CreateTopic", func() error {
		out, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{
			Name: aws.String(snsTopicName),
		})
		if err != nil {
			return err
		}

		topicArn = aws.ToString(out.TopicArn)

		return nil
	})

	sess.Op(notificationService, "GetTopic", func() error {
		_, err := client.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{
			TopicArn: aws.String(topicArn),
		})

		return err
	})

	sess.Op(notificationService, "UpdateTopic", func() error {
		_, err := client.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
			TopicArn:       aws.String(topicArn),
			AttributeName:  aws.String("DisplayName"),
			AttributeValue: aws.String(snsDisplayName),
		})

		return err
	})

	sess.Op(notificationService, "ListTopics", func() error {
		_, err := client.ListTopics(ctx, &awssns.ListTopicsInput{})

		return err
	})

	sess.Op(notificationService, "Subscribe", func() error {
		out, err := client.Subscribe(ctx, &awssns.SubscribeInput{
			TopicArn: aws.String(topicArn),
			Protocol: aws.String(snsProtocol),
			Endpoint: aws.String(snsEndpoint),
		})
		if err != nil {
			return err
		}

		subArn = aws.ToString(out.SubscriptionArn)

		return nil
	})

	sess.Op(notificationService, "Publish", func() error {
		_, err := client.Publish(ctx, &awssns.PublishInput{
			TopicArn: aws.String(topicArn),
			Subject:  aws.String("hello"),
			Message:  aws.String("world"),
		})

		return err
	})

	sess.Op(notificationService, "ListSubscriptions", func() error {
		_, err := client.ListSubscriptions(ctx, &awssns.ListSubscriptionsInput{})

		return err
	})

	sess.Op(notificationService, "Unsubscribe", func() error {
		_, err := client.Unsubscribe(ctx, &awssns.UnsubscribeInput{
			SubscriptionArn: aws.String(subArn),
		})

		return err
	})

	sess.Op(notificationService, "DeleteTopic", func() error {
		_, err := client.DeleteTopic(ctx, &awssns.DeleteTopicInput{
			TopicArn: aws.String(topicArn),
		})

		return err
	})
}
