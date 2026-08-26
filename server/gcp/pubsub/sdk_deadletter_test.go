package pubsub_test

import (
	"context"
	"encoding/base64"
	"testing"

	pubsubv1 "google.golang.org/api/pubsub/v1"
)

func nack(t *testing.T, svc *pubsubv1.Service, sub, ackID string) {
	t.Helper()

	if _, err := svc.Projects.Subscriptions.ModifyAckDeadline(sub,
		&pubsubv1.ModifyAckDeadlineRequest{AckIds: []string{ackID}, AckDeadlineSeconds: 0}).
		Context(context.Background()).Do(); err != nil {
		t.Fatalf("ModifyAckDeadline(nack): %v", err)
	}
}

// TestSDKPubSubDeadLetterRouting guards deadLetterPolicy enforcement: after the
// message exceeds maxDeliveryAttempts nacks on the source subscription, it is
// forwarded to the dead-letter topic (a subscription there receives it) and
// stops redelivering on the source (no infinite redelivery).
func TestSDKPubSubDeadLetterRouting(t *testing.T) {
	svc := newSDKService(t)

	mustTopic(t, svc, "projects/demo/topics/dl-src")
	mustTopic(t, svc, "projects/demo/topics/dl-dlq")

	mustSub(t, svc, "projects/demo/subscriptions/dl-src-sub", &pubsubv1.Subscription{
		Topic: "projects/demo/topics/dl-src",
		DeadLetterPolicy: &pubsubv1.DeadLetterPolicy{
			DeadLetterTopic:     "projects/demo/topics/dl-dlq",
			MaxDeliveryAttempts: 5,
		},
	})
	mustSub(t, svc, "projects/demo/subscriptions/dl-dlq-sub",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/dl-dlq"})

	publish(t, svc, "projects/demo/topics/dl-src",
		&pubsubv1.PubsubMessage{Data: base64.StdEncoding.EncodeToString([]byte("poison"))})

	// Pull + nack until the source stops delivering (message dead-lettered).
	routed := false

	for i := 0; i < 10; i++ {
		resp := pull(t, svc, "projects/demo/subscriptions/dl-src-sub", 1)
		if len(resp.ReceivedMessages) == 0 {
			routed = true
			break
		}

		nack(t, svc, "projects/demo/subscriptions/dl-src-sub", resp.ReceivedMessages[0].AckId)
	}

	if !routed {
		t.Fatal("source kept redelivering past maxDeliveryAttempts — message never dead-lettered")
	}

	dlq := pull(t, svc, "projects/demo/subscriptions/dl-dlq-sub", 1)
	if len(dlq.ReceivedMessages) != 1 {
		t.Fatalf("dead-letter subscription got %d messages, want 1", len(dlq.ReceivedMessages))
	}
}

// TestSDKPubSubDeleteTopicDetachesSub guards that deleting a topic detaches its
// subscriptions: their topic becomes "_deleted-topic_" (real Pub/Sub behavior).
func TestSDKPubSubDeleteTopicDetachesSub(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/det")
	mustSub(t, svc, "projects/demo/subscriptions/det-sub",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/det"})

	if _, err := svc.Projects.Topics.Delete("projects/demo/topics/det").Context(ctx).Do(); err != nil {
		t.Fatalf("Topics.Delete: %v", err)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/det-sub").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get subscription: %v", err)
	}

	if got.Topic != "_deleted-topic_" {
		t.Fatalf("subscription topic=%q, want _deleted-topic_", got.Topic)
	}
}
