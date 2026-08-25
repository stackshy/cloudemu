package pubsub_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	pubsubv1 "google.golang.org/api/pubsub/v1"
)

func mustTopic(t *testing.T, svc *pubsubv1.Service, name string) {
	t.Helper()

	if _, err := svc.Projects.Topics.Create(name, &pubsubv1.Topic{}).Context(context.Background()).Do(); err != nil {
		t.Fatalf("Topic.Create(%s): %v", name, err)
	}
}

func mustSub(t *testing.T, svc *pubsubv1.Service, name string, sub *pubsubv1.Subscription) {
	t.Helper()

	if _, err := svc.Projects.Subscriptions.Create(name, sub).Context(context.Background()).Do(); err != nil {
		t.Fatalf("Subscription.Create(%s): %v", name, err)
	}
}

func publish(t *testing.T, svc *pubsubv1.Service, topic string, msgs ...*pubsubv1.PubsubMessage) {
	t.Helper()

	if _, err := svc.Projects.Topics.Publish(topic,
		&pubsubv1.PublishRequest{Messages: msgs}).Context(context.Background()).Do(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func pull(t *testing.T, svc *pubsubv1.Service, sub string, maxMsgs int64) *pubsubv1.PullResponse {
	t.Helper()

	resp, err := svc.Projects.Subscriptions.Pull(sub,
		&pubsubv1.PullRequest{MaxMessages: maxMsgs}).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("Pull(%s): %v", sub, err)
	}

	return resp
}

// TestSDKPubSubFanOut guards the BLOCKER: two subscriptions on one topic each
// receive an independent copy of every published message.
func TestSDKPubSubFanOut(t *testing.T) {
	svc := newSDKService(t)

	mustTopic(t, svc, "projects/demo/topics/fo")
	mustSub(t, svc, "projects/demo/subscriptions/foa", &pubsubv1.Subscription{Topic: "projects/demo/topics/fo"})
	mustSub(t, svc, "projects/demo/subscriptions/fob", &pubsubv1.Subscription{Topic: "projects/demo/topics/fo"})

	publish(t, svc, "projects/demo/topics/fo",
		&pubsubv1.PubsubMessage{Data: base64.StdEncoding.EncodeToString([]byte("m1"))})

	a := pull(t, svc, "projects/demo/subscriptions/foa", 10)
	b := pull(t, svc, "projects/demo/subscriptions/fob", 10)

	if len(a.ReceivedMessages) != 1 {
		t.Fatalf("foa got %d messages, want 1", len(a.ReceivedMessages))
	}

	if len(b.ReceivedMessages) != 1 {
		t.Fatalf("fob got %d messages, want 1 (fan-out: second sub must not be starved)", len(b.ReceivedMessages))
	}
}

// TestSDKPubSubModifyAckDeadlineRedelivery guards modifyAckDeadline: setting the
// deadline to 0 nacks the message and it is redelivered on the next pull.
func TestSDKPubSubModifyAckDeadlineRedelivery(t *testing.T) {
	svc := newSDKService(t)

	mustTopic(t, svc, "projects/demo/topics/mad")
	mustSub(t, svc, "projects/demo/subscriptions/mad", &pubsubv1.Subscription{Topic: "projects/demo/topics/mad"})
	publish(t, svc, "projects/demo/topics/mad",
		&pubsubv1.PubsubMessage{Data: base64.StdEncoding.EncodeToString([]byte("x"))})

	first := pull(t, svc, "projects/demo/subscriptions/mad", 1)
	if len(first.ReceivedMessages) != 1 {
		t.Fatalf("first pull got %d, want 1", len(first.ReceivedMessages))
	}

	ackID := first.ReceivedMessages[0].AckId
	if _, err := svc.Projects.Subscriptions.ModifyAckDeadline("projects/demo/subscriptions/mad",
		&pubsubv1.ModifyAckDeadlineRequest{AckIds: []string{ackID}, AckDeadlineSeconds: 0}).
		Context(context.Background()).Do(); err != nil {
		t.Fatalf("ModifyAckDeadline: %v", err)
	}

	second := pull(t, svc, "projects/demo/subscriptions/mad", 1)
	if len(second.ReceivedMessages) != 1 {
		t.Fatalf("after nack, redelivery pull got %d, want 1", len(second.ReceivedMessages))
	}
}

// TestSDKPubSubExtendedSubscriptionFields guards that the extended subscription
// config round-trips on Get instead of being silently dropped.
func TestSDKPubSubExtendedSubscriptionFields(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/ext")
	mustTopic(t, svc, "projects/demo/topics/ext-dlq")

	want := &pubsubv1.Subscription{
		Topic:                    "projects/demo/topics/ext",
		AckDeadlineSeconds:       30,
		RetainAckedMessages:      true,
		MessageRetentionDuration: "600s",
		EnableMessageOrdering:    true,
		Filter:                   `attributes.type = "order"`,
		PushConfig:               &pubsubv1.PushConfig{PushEndpoint: "https://example.com/push"},
		ExpirationPolicy:         &pubsubv1.ExpirationPolicy{Ttl: "86400s"},
		DeadLetterPolicy: &pubsubv1.DeadLetterPolicy{
			DeadLetterTopic: "projects/demo/topics/ext-dlq", MaxDeliveryAttempts: 5,
		},
		RetryPolicy: &pubsubv1.RetryPolicy{MinimumBackoff: "10s", MaximumBackoff: "600s"},
	}
	mustSub(t, svc, "projects/demo/subscriptions/ext", want)

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/ext").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	switch {
	case got.PushConfig == nil || got.PushConfig.PushEndpoint != "https://example.com/push":
		t.Errorf("pushConfig not round-tripped: %+v", got.PushConfig)
	case !got.RetainAckedMessages:
		t.Errorf("retainAckedMessages dropped")
	case got.MessageRetentionDuration != "600s":
		t.Errorf("messageRetentionDuration=%q want 600s", got.MessageRetentionDuration)
	case !got.EnableMessageOrdering:
		t.Errorf("enableMessageOrdering dropped")
	case got.Filter != `attributes.type = "order"`:
		t.Errorf("filter=%q", got.Filter)
	case got.ExpirationPolicy == nil || got.ExpirationPolicy.Ttl != "86400s":
		t.Errorf("expirationPolicy not round-tripped: %+v", got.ExpirationPolicy)
	case got.DeadLetterPolicy == nil || got.DeadLetterPolicy.MaxDeliveryAttempts != 5:
		t.Errorf("deadLetterPolicy not round-tripped: %+v", got.DeadLetterPolicy)
	case got.RetryPolicy == nil || got.RetryPolicy.MinimumBackoff != "10s":
		t.Errorf("retryPolicy not round-tripped: %+v", got.RetryPolicy)
	}
}

// TestSDKPubSubDuplicateSubscription409 guards that a duplicate Create is
// rejected rather than silently overwriting.
func TestSDKPubSubDuplicateSubscription409(t *testing.T) {
	svc := newSDKService(t)

	mustTopic(t, svc, "projects/demo/topics/ds")
	mustSub(t, svc, "projects/demo/subscriptions/ds",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/ds", AckDeadlineSeconds: 20})

	_, err := svc.Projects.Subscriptions.Create("projects/demo/subscriptions/ds",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/ds", AckDeadlineSeconds: 99}).
		Context(context.Background()).Do()
	if err == nil {
		t.Fatal("duplicate Subscription.Create returned nil error, want 409 ALREADY_EXISTS")
	}
}

// TestSDKPubSubOrderingKeyAndPublishTime guards that the received message
// carries the publish-time orderingKey and a publishTime stamped at publish
// (not at pull).
func TestSDKPubSubOrderingKeyAndPublishTime(t *testing.T) {
	svc := newSDKService(t)

	mustTopic(t, svc, "projects/demo/topics/ord")
	mustSub(t, svc, "projects/demo/subscriptions/ord", &pubsubv1.Subscription{Topic: "projects/demo/topics/ord"})

	publish(t, svc, "projects/demo/topics/ord", &pubsubv1.PubsubMessage{
		Data:        base64.StdEncoding.EncodeToString([]byte("y")),
		OrderingKey: "k1",
	})

	// Delay so a pull-time publishTime would be measurably later than publish.
	time.Sleep(60 * time.Millisecond)

	resp := pull(t, svc, "projects/demo/subscriptions/ord", 1)
	if len(resp.ReceivedMessages) != 1 {
		t.Fatalf("pull got %d, want 1", len(resp.ReceivedMessages))
	}

	msg := resp.ReceivedMessages[0].Message
	if msg.OrderingKey != "k1" {
		t.Errorf("orderingKey=%q want k1", msg.OrderingKey)
	}

	pt, err := time.Parse(time.RFC3339Nano, msg.PublishTime)
	if err != nil {
		t.Fatalf("publishTime %q not RFC3339: %v", msg.PublishTime, err)
	}

	if age := time.Since(pt); age < 40*time.Millisecond {
		t.Errorf("publishTime age %v too small — stamped at pull, not publish", age)
	}
}
