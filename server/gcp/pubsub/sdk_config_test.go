package pubsub_test

import (
	"context"
	"testing"

	pubsubv1 "google.golang.org/api/pubsub/v1"
)

// TestSDKPubSubTopicFieldsRoundTrip guards that extended topic fields
// (messageRetentionDuration, schemaSettings) round-trip through create and Get,
// rather than being silently dropped.
func TestSDKPubSubTopicFieldsRoundTrip(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	const name = "projects/demo/topics/cfg"

	created, err := svc.Projects.Topics.Create(name, &pubsubv1.Topic{
		MessageRetentionDuration: "604800s",
		SchemaSettings: &pubsubv1.SchemaSettings{
			Schema:   "projects/demo/schemas/s1",
			Encoding: "JSON",
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.MessageRetentionDuration != "604800s" {
		t.Errorf("create messageRetentionDuration=%q, want 604800s", created.MessageRetentionDuration)
	}

	got, err := svc.Projects.Topics.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.MessageRetentionDuration != "604800s" {
		t.Errorf("get messageRetentionDuration=%q, want 604800s", got.MessageRetentionDuration)
	}

	if got.SchemaSettings == nil || got.SchemaSettings.Schema != "projects/demo/schemas/s1" {
		t.Fatalf("get schemaSettings=%+v, want schema projects/demo/schemas/s1", got.SchemaSettings)
	}

	if got.SchemaSettings.Encoding != "JSON" {
		t.Errorf("get schemaSettings.Encoding=%q, want JSON", got.SchemaSettings.Encoding)
	}
}

// TestSDKPubSubTopicPatchUpdateMask guards that topics.patch honors updateMask:
// only the masked field is updated; unmasked fields are left unchanged.
func TestSDKPubSubTopicPatchUpdateMask(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	const name = "projects/demo/topics/patchcfg"

	if _, err := svc.Projects.Topics.Create(name, &pubsubv1.Topic{
		MessageRetentionDuration: "3600s",
		Labels:                   map[string]string{"team": "core"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Projects.Topics.Patch(name, &pubsubv1.UpdateTopicRequest{
		Topic: &pubsubv1.Topic{
			MessageRetentionDuration: "7200s",
			Labels:                   map[string]string{"team": "changed"},
		},
		UpdateMask: "messageRetentionDuration",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.Topics.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.MessageRetentionDuration != "7200s" {
		t.Errorf("messageRetentionDuration=%q, want 7200s (masked, updated)", got.MessageRetentionDuration)
	}

	if got.Labels["team"] != "core" {
		t.Errorf("labels[team]=%q, want core (not in mask, must be unchanged)", got.Labels["team"])
	}
}

// TestSDKPubSubExactlyOnceRoundTrip guards that enableExactlyOnceDelivery
// round-trips through subscription create and patch.
func TestSDKPubSubExactlyOnceRoundTrip(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/eos")
	mustSub(t, svc, "projects/demo/subscriptions/eos", &pubsubv1.Subscription{
		Topic:                     "projects/demo/topics/eos",
		EnableExactlyOnceDelivery: true,
	})

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/eos").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !got.EnableExactlyOnceDelivery {
		t.Fatal("create: enableExactlyOnceDelivery=false, want true")
	}

	if _, err := svc.Projects.Subscriptions.Patch("projects/demo/subscriptions/eos",
		&pubsubv1.UpdateSubscriptionRequest{
			Subscription: &pubsubv1.Subscription{EnableExactlyOnceDelivery: false, ForceSendFields: []string{"EnableExactlyOnceDelivery"}},
			UpdateMask:   "enableExactlyOnceDelivery",
		}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err = svc.Projects.Subscriptions.Get("projects/demo/subscriptions/eos").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got.EnableExactlyOnceDelivery {
		t.Error("patch: enableExactlyOnceDelivery=true, want false after masked patch")
	}
}

// TestSDKPubSubDetach guards subscriptions:detach: it returns 200 and marks the
// subscription detached.
func TestSDKPubSubDetach(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/dtch")
	mustSub(t, svc, "projects/demo/subscriptions/dtch",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/dtch"})

	if _, err := svc.Projects.Subscriptions.Detach("projects/demo/subscriptions/dtch").Context(ctx).Do(); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/dtch").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !got.Detached {
		t.Fatal("detached=false, want true after Detach")
	}
}

// TestSDKPubSubCreateSubEmptyTopic guards that a subscription create without a
// topic is rejected with 400 INVALID_ARGUMENT.
func TestSDKPubSubCreateSubEmptyTopic(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Subscriptions.Create("projects/demo/subscriptions/notopic",
		&pubsubv1.Subscription{}).Context(ctx).Do(); err == nil {
		t.Fatal("create with empty topic returned nil error, want 400 INVALID_ARGUMENT")
	}
}

// TestSDKPubSubAckDeadlineRange guards that ackDeadlineSeconds outside the valid
// 10..600 range is rejected on create, and an in-range value is accepted.
func TestSDKPubSubAckDeadlineRange(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/adl")

	for _, bad := range []int64{5, 5000} {
		_, err := svc.Projects.Subscriptions.Create("projects/demo/subscriptions/adl-bad",
			&pubsubv1.Subscription{Topic: "projects/demo/topics/adl", AckDeadlineSeconds: bad}).
			Context(ctx).Do()
		if err == nil {
			t.Fatalf("create with ackDeadlineSeconds=%d returned nil error, want 400", bad)
		}
	}

	if _, err := svc.Projects.Subscriptions.Create("projects/demo/subscriptions/adl-ok",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/adl", AckDeadlineSeconds: 60}).
		Context(ctx).Do(); err != nil {
		t.Fatalf("create with ackDeadlineSeconds=60: %v", err)
	}
}
