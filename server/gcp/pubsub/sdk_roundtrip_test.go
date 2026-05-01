package pubsub_test

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
	"google.golang.org/api/option"
	pubsubv1 "google.golang.org/api/pubsub/v1"
)

func newSDKService(t *testing.T) *pubsubv1.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{PubSub: cloud.PubSub})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := pubsubv1.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return svc
}

func TestSDKPubSubTopicLifecycle(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	created, err := svc.Projects.Topics.Create(
		"projects/demo/topics/sdk-topic", &pubsubv1.Topic{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !strings.HasSuffix(created.Name, "/topics/sdk-topic") {
		t.Fatalf("created.Name = %q, want suffix /topics/sdk-topic", created.Name)
	}

	got, err := svc.Projects.Topics.Get("projects/demo/topics/sdk-topic").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name != created.Name {
		t.Fatalf("Get returned %q, want %q", got.Name, created.Name)
	}

	if _, err := svc.Projects.Topics.Delete("projects/demo/topics/sdk-topic").Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestSDKPubSubPublishPullAck(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	const topicName = "projects/demo/topics/loop"
	const subName = "projects/demo/subscriptions/loop"

	if _, err := svc.Projects.Topics.Create(topicName, &pubsubv1.Topic{}).Context(ctx).Do(); err != nil {
		t.Fatalf("Topic.Create: %v", err)
	}

	if _, err := svc.Projects.Subscriptions.Create(subName, &pubsubv1.Subscription{
		Topic: topicName,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Subscription.Create: %v", err)
	}

	if _, err := svc.Projects.Topics.Publish(topicName, &pubsubv1.PublishRequest{
		Messages: []*pubsubv1.PubsubMessage{
			{Data: base64.StdEncoding.EncodeToString([]byte("hello"))},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	pull, err := svc.Projects.Subscriptions.Pull(subName, &pubsubv1.PullRequest{
		MaxMessages: 1,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}

	if len(pull.ReceivedMessages) != 1 {
		t.Fatalf("got %d messages, want 1", len(pull.ReceivedMessages))
	}

	body, _ := base64.StdEncoding.DecodeString(pull.ReceivedMessages[0].Message.Data)
	if string(body) != "hello" {
		t.Fatalf("body = %q, want hello", body)
	}

	if _, err := svc.Projects.Subscriptions.Acknowledge(subName, &pubsubv1.AcknowledgeRequest{
		AckIds: []string{pull.ReceivedMessages[0].AckId},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
}

func TestSDKPubSubPublishToMissingTopic(t *testing.T) {
	svc := newSDKService(t)

	_, err := svc.Projects.Topics.Publish("projects/demo/topics/nope",
		&pubsubv1.PublishRequest{Messages: []*pubsubv1.PubsubMessage{
			{Data: base64.StdEncoding.EncodeToString([]byte("x"))},
		}}).Context(context.Background()).Do()

	if err == nil {
		t.Fatal("Publish to missing topic returned nil error, want NotFound")
	}
}
