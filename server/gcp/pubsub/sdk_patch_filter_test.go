package pubsub_test

import (
	"context"
	"encoding/base64"
	"testing"

	pubsubv1 "google.golang.org/api/pubsub/v1"
)

// TestSDKPubSubSubscriptionPatch guards subscriptions.patch: an in-place update
// of ackDeadlineSeconds via updateMask is reflected on a subsequent Get (real
// Terraform / SDK Update path; previously returned 405).
func TestSDKPubSubSubscriptionPatch(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/pt")
	mustSub(t, svc, "projects/demo/subscriptions/ps",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/pt", AckDeadlineSeconds: 10})

	_, err := svc.Projects.Subscriptions.Patch("projects/demo/subscriptions/ps",
		&pubsubv1.UpdateSubscriptionRequest{
			Subscription: &pubsubv1.Subscription{AckDeadlineSeconds: 30},
			UpdateMask:   "ackDeadlineSeconds",
		}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Subscriptions.Patch: %v", err)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/ps").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.AckDeadlineSeconds != 30 {
		t.Fatalf("ackDeadlineSeconds=%d, want 30", got.AckDeadlineSeconds)
	}
}

// TestSDKPubSubSubscriptionPatchMaskScope guards that only masked fields change:
// patching labels leaves ackDeadlineSeconds untouched.
func TestSDKPubSubSubscriptionPatchMaskScope(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/pms")
	mustSub(t, svc, "projects/demo/subscriptions/pms",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/pms", AckDeadlineSeconds: 25})

	if _, err := svc.Projects.Subscriptions.Patch("projects/demo/subscriptions/pms",
		&pubsubv1.UpdateSubscriptionRequest{
			Subscription: &pubsubv1.Subscription{Labels: map[string]string{"env": "prod"}, AckDeadlineSeconds: 99},
			UpdateMask:   "labels",
		}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/pms").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.AckDeadlineSeconds != 25 {
		t.Errorf("ackDeadlineSeconds=%d, want 25 (not in mask, must be unchanged)", got.AckDeadlineSeconds)
	}

	if got.Labels["env"] != "prod" {
		t.Errorf("labels[env]=%q, want prod", got.Labels["env"])
	}
}

// TestSDKPubSubSubscriptionPatchFilterImmutable guards that changing the
// immutable filter field is rejected.
func TestSDKPubSubSubscriptionPatchFilterImmutable(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/pf")
	mustSub(t, svc, "projects/demo/subscriptions/pf",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/pf", Filter: `attributes.a = "1"`})

	_, err := svc.Projects.Subscriptions.Patch("projects/demo/subscriptions/pf",
		&pubsubv1.UpdateSubscriptionRequest{
			Subscription: &pubsubv1.Subscription{Filter: `attributes.a = "2"`},
			UpdateMask:   "filter",
		}).Context(ctx).Do()
	if err == nil {
		t.Fatal("patching immutable filter returned nil error, want rejection")
	}
}

// TestSDKPubSubTopicPatch guards topics.patch: labels update is reflected on Get.
func TestSDKPubSubTopicPatch(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/tp")

	if _, err := svc.Projects.Topics.Patch("projects/demo/topics/tp",
		&pubsubv1.UpdateTopicRequest{
			Topic:      &pubsubv1.Topic{Labels: map[string]string{"team": "core"}},
			UpdateMask: "labels",
		}).Context(ctx).Do(); err != nil {
		t.Fatalf("Topics.Patch: %v", err)
	}

	got, err := svc.Projects.Topics.Get("projects/demo/topics/tp").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Labels["team"] != "core" {
		t.Fatalf("labels[team]=%q, want core", got.Labels["team"])
	}
}

// TestSDKPubSubFilterDelivery guards that a subscription filter is evaluated on
// delivery: only messages whose attributes match are pulled; non-matching ones
// are filtered out (auto-acked) and never delivered.
func TestSDKPubSubFilterDelivery(t *testing.T) {
	svc := newSDKService(t)

	mustTopic(t, svc, "projects/demo/topics/flt")
	mustSub(t, svc, "projects/demo/subscriptions/flt",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/flt", Filter: `attributes.color = "red"`})

	publish(t, svc, "projects/demo/topics/flt",
		&pubsubv1.PubsubMessage{
			Data:       base64.StdEncoding.EncodeToString([]byte("r")),
			Attributes: map[string]string{"color": "red"},
		},
		&pubsubv1.PubsubMessage{
			Data:       base64.StdEncoding.EncodeToString([]byte("b")),
			Attributes: map[string]string{"color": "blue"},
		},
	)

	resp := pull(t, svc, "projects/demo/subscriptions/flt", 10)
	if len(resp.ReceivedMessages) != 1 {
		t.Fatalf("filtered pull got %d messages, want 1 (only red)", len(resp.ReceivedMessages))
	}

	if got := resp.ReceivedMessages[0].Message.Attributes["color"]; got != "red" {
		t.Fatalf("delivered color=%q, want red", got)
	}
}

// TestSDKPubSubFilterPrefixAndBoolean guards hasPrefix + AND/OR/NOT combinations.
func TestSDKPubSubFilterPrefixAndBoolean(t *testing.T) {
	svc := newSDKService(t)

	mustTopic(t, svc, "projects/demo/topics/fltp")
	mustSub(t, svc, "projects/demo/subscriptions/fltp", &pubsubv1.Subscription{
		Topic:  "projects/demo/topics/fltp",
		Filter: `hasPrefix(attributes.name, "co") AND NOT attributes:skip`,
	})

	publish(t, svc, "projects/demo/topics/fltp",
		&pubsubv1.PubsubMessage{ // matches: name starts with "co", no skip attr
			Data:       base64.StdEncoding.EncodeToString([]byte("1")),
			Attributes: map[string]string{"name": "com"},
		},
		&pubsubv1.PubsubMessage{ // rejected: has skip attribute
			Data:       base64.StdEncoding.EncodeToString([]byte("2")),
			Attributes: map[string]string{"name": "core", "skip": "yes"},
		},
		&pubsubv1.PubsubMessage{ // rejected: wrong prefix
			Data:       base64.StdEncoding.EncodeToString([]byte("3")),
			Attributes: map[string]string{"name": "net"},
		},
	)

	resp := pull(t, svc, "projects/demo/subscriptions/fltp", 10)
	if len(resp.ReceivedMessages) != 1 {
		t.Fatalf("boolean-filter pull got %d, want 1", len(resp.ReceivedMessages))
	}

	if got := resp.ReceivedMessages[0].Message.Attributes["name"]; got != "com" {
		t.Fatalf("delivered name=%q, want com", got)
	}
}
