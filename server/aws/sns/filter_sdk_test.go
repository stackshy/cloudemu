package sns_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSNSAndSQS(t *testing.T) (*awssns.Client, *awssqs.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{SNS: cloud.SNS, SQS: cloud.SQS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	sns := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return sns, sqs
}

func snsQueue(t *testing.T, sqs *awssqs.Client, name string) (url, arn string) {
	t.Helper()

	ctx := context.Background()

	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name)})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	attrs, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}

	return aws.ToString(q.QueueUrl), attrs.Attributes["QueueArn"]
}

func drain(t *testing.T, sqs *awssqs.Client, url string) []string {
	t.Helper()

	out, err := sqs.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{
		QueueUrl:            aws.String(url),
		MaxNumberOfMessages: 10,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	bodies := make([]string, len(out.Messages))
	for i := range out.Messages {
		bodies[i] = aws.ToString(out.Messages[i].Body)
	}

	return bodies
}

func TestSDKSNSFilterPolicyGatesDelivery(t *testing.T) {
	sns, sqs := newSNSAndSQS(t)
	ctx := context.Background()

	url, arn := snsQueue(t, sqs, "filtered")

	topic, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("feed")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	sub, err := sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              topic.TopicArn,
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String(arn),
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if _, err := sns.SetSubscriptionAttributes(ctx, &awssns.SetSubscriptionAttributesInput{
		SubscriptionArn: sub.SubscriptionArn,
		AttributeName:   aws.String("FilterPolicy"),
		AttributeValue:  aws.String(`{"color":["red"]}`),
	}); err != nil {
		t.Fatalf("SetSubscriptionAttributes: %v", err)
	}

	publish := func(color string) {
		if _, err := sns.Publish(ctx, &awssns.PublishInput{
			TopicArn: topic.TopicArn,
			Message:  aws.String("hi"),
			MessageAttributes: map[string]snstypes.MessageAttributeValue{
				"color": {DataType: aws.String("String"), StringValue: aws.String(color)},
			},
		}); err != nil {
			t.Fatalf("Publish(%s): %v", color, err)
		}
	}

	publish("blue")

	if got := drain(t, sqs, url); len(got) != 0 {
		t.Fatalf("blue message delivered %d, want 0 (filtered out)", len(got))
	}

	publish("red")

	if got := drain(t, sqs, url); len(got) != 1 {
		t.Fatalf("red message delivered %d, want 1", len(got))
	}
}

func TestSDKSNSRawMessageDelivery(t *testing.T) {
	sns, sqs := newSNSAndSQS(t)
	ctx := context.Background()

	url, arn := snsQueue(t, sqs, "raw")

	topic, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("feed")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	sub, err := sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              topic.TopicArn,
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String(arn),
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if _, err := sns.SetSubscriptionAttributes(ctx, &awssns.SetSubscriptionAttributesInput{
		SubscriptionArn: sub.SubscriptionArn,
		AttributeName:   aws.String("RawMessageDelivery"),
		AttributeValue:  aws.String("true"),
	}); err != nil {
		t.Fatalf("SetSubscriptionAttributes: %v", err)
	}

	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn: topic.TopicArn,
		Message:  aws.String("hello-raw"),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := drain(t, sqs, url)
	if len(got) != 1 {
		t.Fatalf("delivered %d, want 1", len(got))
	}

	if got[0] != "hello-raw" {
		t.Fatalf("raw body = %q, want %q (no Notification envelope)", got[0], "hello-raw")
	}

	// Sanity: the raw body must not be the JSON envelope.
	var env map[string]any
	if json.Unmarshal([]byte(got[0]), &env) == nil && env["Type"] == "Notification" {
		t.Fatalf("raw delivery still wrapped in envelope: %s", got[0])
	}
}

// TestSDKSNSNotificationEnvelopeFields verifies the SNS->SQS Notification JSON:
// Subject is omitted when none was published, and the standard SignatureVersion,
// Signature, SigningCertURL, and UnsubscribeURL fields are always present.
func TestSDKSNSNotificationEnvelopeFields(t *testing.T) {
	sns, sqs := newSNSAndSQS(t)
	ctx := context.Background()

	url, arn := snsQueue(t, sqs, "envelope")

	topic, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("feed")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              topic.TopicArn,
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String(arn),
		ReturnSubscriptionArn: true,
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Publish WITHOUT a Subject.
	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn: topic.TopicArn,
		Message:  aws.String("hello world"),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := drain(t, sqs, url)
	if len(got) != 1 {
		t.Fatalf("delivered %d, want 1", len(got))
	}

	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got[0]), &env); err != nil {
		t.Fatalf("envelope not JSON: %v", err)
	}

	if _, ok := env["Subject"]; ok {
		t.Fatalf("Subject present with no subject published; AWS omits the key: %s", got[0])
	}

	for _, field := range []string{"SignatureVersion", "Signature", "SigningCertURL", "UnsubscribeURL"} {
		if _, ok := env[field]; !ok {
			t.Fatalf("envelope missing standard field %q: %s", field, got[0])
		}
	}

	// With a Subject, the key appears.
	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn: topic.TopicArn,
		Subject:  aws.String("greeting"),
		Message:  aws.String("hi"),
	}); err != nil {
		t.Fatalf("Publish(subject): %v", err)
	}

	got = drain(t, sqs, url)
	if len(got) != 1 {
		t.Fatalf("delivered %d, want 1", len(got))
	}

	var withSubject struct {
		Subject string `json:"Subject"`
	}
	if err := json.Unmarshal([]byte(got[0]), &withSubject); err != nil {
		t.Fatalf("envelope not JSON: %v", err)
	}

	if withSubject.Subject != "greeting" {
		t.Fatalf("Subject = %q, want %q", withSubject.Subject, "greeting")
	}
}
