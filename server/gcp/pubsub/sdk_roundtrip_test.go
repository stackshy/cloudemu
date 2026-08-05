package pubsub_test

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
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

// TestSDKPubSubSubscriptionMetadata guards the #321 fixes: a subscription may
// have a name distinct from its topic, and its ackDeadline + labels must
// round-trip on Get (not be hardcoded). Delete must also be effective.
func TestSDKPubSubSubscriptionMetadata(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Topics.Create("projects/demo/topics/events",
		&pubsubv1.Topic{}).Context(ctx).Do(); err != nil {
		t.Fatalf("Topic.Create: %v", err)
	}

	// Distinct subscription name (not "events").
	if _, err := svc.Projects.Subscriptions.Create("projects/demo/subscriptions/billing-sub",
		&pubsubv1.Subscription{
			Topic:              "projects/demo/topics/events",
			AckDeadlineSeconds: 45,
			Labels:             map[string]string{"team": "fin"},
		}).Context(ctx).Do(); err != nil {
		t.Fatalf("Subscription.Create (distinct name): %v", err)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/billing-sub").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Subscription.Get: %v", err)
	}

	if !strings.HasSuffix(got.Topic, "/topics/events") {
		t.Errorf("topic=%q want .../topics/events", got.Topic)
	}

	if got.AckDeadlineSeconds != 45 {
		t.Errorf("ackDeadlineSeconds=%d want 45", got.AckDeadlineSeconds)
	}

	if got.Labels["team"] != "fin" {
		t.Errorf("labels=%v want team=fin", got.Labels)
	}

	// List must return the real subscription (distinct name + metadata), not a
	// phantom one named after the topic queue.
	list, err := svc.Projects.Subscriptions.List("projects/demo").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Subscriptions.List: %v", err)
	}

	if len(list.Subscriptions) != 1 {
		t.Fatalf("List returned %d subs, want 1: %+v", len(list.Subscriptions), list.Subscriptions)
	}

	ls := list.Subscriptions[0]
	if !strings.HasSuffix(ls.Name, "/subscriptions/billing-sub") {
		t.Errorf("List sub name=%q want .../subscriptions/billing-sub", ls.Name)
	}

	if !strings.HasSuffix(ls.Topic, "/topics/events") || ls.AckDeadlineSeconds != 45 {
		t.Errorf("List sub metadata wrong: topic=%q ackDeadline=%d", ls.Topic, ls.AckDeadlineSeconds)
	}

	if _, err := svc.Projects.Subscriptions.Delete("projects/demo/subscriptions/billing-sub").Context(ctx).Do(); err != nil {
		t.Fatalf("Subscription.Delete: %v", err)
	}

	if _, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/billing-sub").Context(ctx).Do(); err == nil {
		t.Fatal("Get after Delete returned nil error, want NotFound")
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
