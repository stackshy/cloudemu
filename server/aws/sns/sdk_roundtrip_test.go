package sns_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

func newSDKClient(t *testing.T) *awssns.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		SNS: cloud.SNS,
		// EC2 also wired so we exercise the dispatch precedence: a request
		// for SNS must claim the body before EC2 sees it.
		EC2: cloud.EC2,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awssns.NewFromConfig(cfg, func(o *awssns.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKSNSTopicLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name: aws.String("my-topic"),
		Tags: []snstypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	topicArn := aws.ToString(created.TopicArn)
	if topicArn == "" {
		t.Fatal("CreateTopic returned empty TopicArn")
	}

	// CreateTopic is idempotent: a second create with the same name returns
	// the same ARN rather than an error.
	again, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("my-topic")})
	if err != nil {
		t.Fatalf("CreateTopic (idempotent): %v", err)
	}

	if aws.ToString(again.TopicArn) != topicArn {
		t.Fatalf("idempotent CreateTopic ARN = %q, want %q", aws.ToString(again.TopicArn), topicArn)
	}

	attrs, err := client.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{
		TopicArn: aws.String(topicArn),
	})
	if err != nil {
		t.Fatalf("GetTopicAttributes: %v", err)
	}

	if attrs.Attributes["TopicArn"] != topicArn {
		t.Fatalf("attribute TopicArn = %q, want %q", attrs.Attributes["TopicArn"], topicArn)
	}

	sub, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: aws.String(topicArn),
		Protocol: aws.String("email"),
		Endpoint: aws.String("ops@example.com"),
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	subArn := aws.ToString(sub.SubscriptionArn)
	if subArn == "" {
		t.Fatal("Subscribe returned empty SubscriptionArn")
	}

	pub, err := client.Publish(ctx, &awssns.PublishInput{
		TopicArn: aws.String(topicArn),
		Subject:  aws.String("hello"),
		Message:  aws.String("world"),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if aws.ToString(pub.MessageId) == "" {
		t.Fatal("Publish returned empty MessageId")
	}

	byTopic, err := client.ListSubscriptionsByTopic(ctx, &awssns.ListSubscriptionsByTopicInput{
		TopicArn: aws.String(topicArn),
	})
	if err != nil {
		t.Fatalf("ListSubscriptionsByTopic: %v", err)
	}

	if len(byTopic.Subscriptions) != 1 {
		t.Fatalf("got %d subscriptions, want 1", len(byTopic.Subscriptions))
	}

	if aws.ToString(byTopic.Subscriptions[0].Protocol) != "email" ||
		aws.ToString(byTopic.Subscriptions[0].Endpoint) != "ops@example.com" {
		t.Fatalf("subscription = %+v, want email/ops@example.com", byTopic.Subscriptions[0])
	}

	list, err := client.ListTopics(ctx, &awssns.ListTopicsInput{})
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}

	if len(list.Topics) != 1 || aws.ToString(list.Topics[0].TopicArn) != topicArn {
		t.Fatalf("ListTopics = %+v, want one topic %q", list.Topics, topicArn)
	}

	allSubs, err := client.ListSubscriptions(ctx, &awssns.ListSubscriptionsInput{})
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}

	if len(allSubs.Subscriptions) != 1 {
		t.Fatalf("ListSubscriptions got %d, want 1", len(allSubs.Subscriptions))
	}

	if _, err := client.Unsubscribe(ctx, &awssns.UnsubscribeInput{
		SubscriptionArn: aws.String(subArn),
	}); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	afterUnsub, err := client.ListSubscriptionsByTopic(ctx, &awssns.ListSubscriptionsByTopicInput{
		TopicArn: aws.String(topicArn),
	})
	if err != nil {
		t.Fatalf("ListSubscriptionsByTopic (after unsubscribe): %v", err)
	}

	if len(afterUnsub.Subscriptions) != 0 {
		t.Fatalf("got %d subscriptions after unsubscribe, want 0", len(afterUnsub.Subscriptions))
	}

	if _, err := client.DeleteTopic(ctx, &awssns.DeleteTopicInput{
		TopicArn: aws.String(topicArn),
	}); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
}

func TestSDKSNSErrors(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:000000000000:missing"),
	})
	if err == nil {
		t.Fatal("GetTopicAttributes(missing): expected error, got nil")
	}

	var nfe *snstypes.NotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("GetTopicAttributes(missing): got %v, want NotFoundException", err)
	}

	if _, err := client.Publish(ctx, &awssns.PublishInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:000000000000:missing"),
		Message:  aws.String("x"),
	}); !errors.As(err, &nfe) {
		t.Fatalf("Publish(missing topic): got %v, want NotFoundException", err)
	}
}
