package sns_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	s3provider "github.com/stackshy/cloudemu/v2/providers/aws/s3"
	snsprovider "github.com/stackshy/cloudemu/v2/providers/aws/sns"
	sndriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// recordingInvoker captures SNS -> Lambda deliveries.
type recordingInvoker struct {
	arns     []string
	payloads []string
}

func (i *recordingInvoker) InvokeExternal(_ context.Context, functionARN string, payload []byte) error {
	i.arns = append(i.arns, functionARN)
	i.payloads = append(i.payloads, string(payload))

	return nil
}

// snsRecord decodes the first Records[].Sns envelope of an SNS -> Lambda payload.
func snsRecord(t *testing.T, payload string) map[string]any {
	t.Helper()

	var event struct {
		Records []struct {
			EventSource          string         `json:"EventSource"`
			EventVersion         string         `json:"EventVersion"`
			EventSubscriptionArn string         `json:"EventSubscriptionArn"`
			Sns                  map[string]any `json:"Sns"`
		} `json:"Records"`
	}

	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("payload is not an SNS Records event: %v (%s)", err, payload)
	}

	if len(event.Records) != 1 {
		t.Fatalf("expected exactly 1 record, got %d", len(event.Records))
	}

	r := event.Records[0]
	if r.EventSource != "aws:sns" || r.EventVersion != "1.0" {
		t.Fatalf("unexpected record framing: %+v", r)
	}

	if r.EventSubscriptionArn == "" {
		t.Fatalf("record missing EventSubscriptionArn: %+v", r)
	}

	return r.Sns
}

// TestSNSToLambdaDelivery verifies that publishing to a topic with a
// lambda-protocol subscription invokes the function with the SNS Records event,
// and that Message/MessageAttributes/Subject survive the hop.
func TestSNSToLambdaDelivery(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	invoker := &recordingInvoker{}
	sns := snsprovider.New(opts)
	sns.SetLambdaInvoker(invoker)

	topic, err := sns.CreateTopic(ctx, sndriver.TopicConfig{Name: "feed"})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	const fnARN = "arn:aws:lambda:us-east-1:000000000000:function:notify"

	if _, err := sns.Subscribe(ctx, sndriver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "lambda", Endpoint: fnARN,
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if _, err := sns.Publish(ctx, sndriver.PublishInput{
		TopicID: topic.Name, Message: "hello", Subject: "hi",
		Attributes: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(invoker.arns) != 1 || invoker.arns[0] != fnARN {
		t.Fatalf("expected 1 invoke of %s, got %v", fnARN, invoker.arns)
	}

	envelope := snsRecord(t, invoker.payloads[0])
	if envelope["Type"] != "Notification" || envelope["Message"] != "hello" ||
		envelope["TopicArn"] != topic.ResourceID || envelope["Subject"] != "hi" {
		t.Fatalf("unexpected Sns envelope: %+v", envelope)
	}

	attrs, ok := envelope["MessageAttributes"].(map[string]any)
	if !ok {
		t.Fatalf("Sns envelope missing MessageAttributes: %+v", envelope)
	}

	env, ok := attrs["env"].(map[string]any)
	if !ok || env["Type"] != "String" || env["Value"] != "prod" {
		t.Fatalf("unexpected MessageAttributes: %+v", attrs)
	}
}

// TestSNSToLambdaFilterPolicy verifies a filter policy on a lambda-protocol
// subscription gates delivery exactly like the SQS path.
func TestSNSToLambdaFilterPolicy(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	invoker := &recordingInvoker{}
	sns := snsprovider.New(opts)
	sns.SetLambdaInvoker(invoker)

	topic, err := sns.CreateTopic(ctx, sndriver.TopicConfig{Name: "filtered"})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	const fnARN = "arn:aws:lambda:us-east-1:000000000000:function:filtered"

	if _, err := sns.Subscribe(ctx, sndriver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "lambda", Endpoint: fnARN,
		Attributes: map[string]string{"FilterPolicy": `{"env":["prod"]}`},
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Non-matching publish is filtered out.
	if _, err := sns.Publish(ctx, sndriver.PublishInput{
		TopicID: topic.Name, Message: "dev-msg", Attributes: map[string]string{"env": "dev"},
	}); err != nil {
		t.Fatalf("Publish dev: %v", err)
	}

	if len(invoker.arns) != 0 {
		t.Fatalf("expected filtered publish to be dropped, got %v", invoker.arns)
	}

	// Matching publish is delivered.
	if _, err := sns.Publish(ctx, sndriver.PublishInput{
		TopicID: topic.Name, Message: "prod-msg", Attributes: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("Publish prod: %v", err)
	}

	if len(invoker.arns) != 1 {
		t.Fatalf("expected exactly the matching publish delivered, got %v", invoker.arns)
	}

	if got := snsRecord(t, invoker.payloads[0])["Message"]; got != "prod-msg" {
		t.Fatalf("delivered the wrong message: %v", got)
	}
}

// TestS3ToSNSToLambdaTransitive verifies the transitive S3 -> SNS -> Lambda
// chain: an object upload delivers to the topic, whose lambda subscriber is
// invoked with the S3 event nested in the SNS Records envelope.
func TestS3ToSNSToLambdaTransitive(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	invoker := &recordingInvoker{}
	sns := snsprovider.New(opts)
	sns.SetLambdaInvoker(invoker)

	s3 := s3provider.New(opts)
	s3.SetSNSPublisher(sns)

	topic, err := sns.CreateTopic(ctx, sndriver.TopicConfig{Name: "s3-topic"})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	const fnARN = "arn:aws:lambda:us-east-1:000000000000:function:s3fn"

	if _, err := sns.Subscribe(ctx, sndriver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "lambda", Endpoint: fnARN,
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := s3.CreateBucket(ctx, "uploads"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if err := s3.PutBucketNotification(ctx, "uploads", []s3provider.BucketNotification{{
		ID: "toSNS", Target: s3provider.NotifyTopic, ARN: topic.ResourceID,
		Events: []string{"s3:ObjectCreated:*"},
	}}); err != nil {
		t.Fatalf("PutBucketNotification: %v", err)
	}

	if err := s3.PutObject(ctx, "uploads", "photo.jpg", []byte("data"), "image/jpeg", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	if len(invoker.arns) != 1 || invoker.arns[0] != fnARN {
		t.Fatalf("expected S3 -> SNS -> Lambda to invoke %s once, got %v", fnARN, invoker.arns)
	}

	// The SNS envelope's Message carries the S3 event JSON.
	msg, _ := snsRecord(t, invoker.payloads[0])["Message"].(string)
	if msg == "" || !json.Valid([]byte(msg)) {
		t.Fatalf("expected the S3 event JSON in the SNS Message, got %q", msg)
	}
}

// TestSNSPublishNilLambdaInvoker verifies Publish with a lambda subscription but
// no wired invoker does not panic and delivers nothing.
func TestSNSPublishNilLambdaInvoker(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	sns := snsprovider.New(opts)

	topic, err := sns.CreateTopic(ctx, sndriver.TopicConfig{Name: "nolambda"})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := sns.Subscribe(ctx, sndriver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "lambda",
		Endpoint: "arn:aws:lambda:us-east-1:000000000000:function:x",
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if _, err := sns.Publish(ctx, sndriver.PublishInput{TopicID: topic.Name, Message: "hi"}); err != nil {
		t.Fatalf("Publish must not fail with a nil invoker: %v", err)
	}
}
