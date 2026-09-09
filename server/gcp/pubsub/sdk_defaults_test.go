package pubsub_test

import (
	"context"
	"testing"

	pubsubv1 "google.golang.org/api/pubsub/v1"
)

// TestSDKPubSubSubscriptionCreateDefaults guards the #1 Terraform-drift source:
// a subscription created with only the required Topic must read back GCP's
// server-assigned defaults (ackDeadlineSeconds=10, messageRetentionDuration=
// 604800s/7d, expirationPolicy.ttl=2678400s/31d, state=ACTIVE). If any default
// is missing, `sub.Config()` and every Terraform plan diverge from real GCP.
func TestSDKPubSubSubscriptionCreateDefaults(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/defs")

	created, err := svc.Projects.Subscriptions.Create("projects/demo/subscriptions/defs",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/defs"}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	assertSubDefaults(t, "create", created)

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/defs").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertSubDefaults(t, "get", got)
}

func assertSubDefaults(t *testing.T, stage string, s *pubsubv1.Subscription) {
	t.Helper()

	if s.AckDeadlineSeconds != 10 {
		t.Errorf("%s: ackDeadlineSeconds=%d, want 10", stage, s.AckDeadlineSeconds)
	}

	if s.MessageRetentionDuration != "604800s" {
		t.Errorf("%s: messageRetentionDuration=%q, want 604800s", stage, s.MessageRetentionDuration)
	}

	if s.ExpirationPolicy == nil || s.ExpirationPolicy.Ttl != "2678400s" {
		t.Errorf("%s: expirationPolicy=%+v, want ttl 2678400s", stage, s.ExpirationPolicy)
	}

	if s.State != "ACTIVE" {
		t.Errorf("%s: state=%q, want ACTIVE", stage, s.State)
	}
}

// TestSDKPubSubExplicitNeverExpire guards that an explicitly-set empty
// expirationPolicy ({} = never expire) is preserved verbatim and NOT clobbered
// by the 31-day default.
func TestSDKPubSubExplicitNeverExpire(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/never")

	created, err := svc.Projects.Subscriptions.Create("projects/demo/subscriptions/never",
		&pubsubv1.Subscription{
			Topic:            "projects/demo/topics/never",
			ExpirationPolicy: &pubsubv1.ExpirationPolicy{ForceSendFields: []string{"Ttl"}},
		}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.ExpirationPolicy == nil || created.ExpirationPolicy.Ttl != "" {
		t.Errorf("create: expirationPolicy=%+v, want empty ttl (never expire)", created.ExpirationPolicy)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/never").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ExpirationPolicy == nil || got.ExpirationPolicy.Ttl != "" {
		t.Errorf("get: expirationPolicy=%+v, want empty ttl (never expire)", got.ExpirationPolicy)
	}
}

// TestSDKPubSubExplicitRetentionPreserved guards that an explicit
// messageRetentionDuration is preserved, not overwritten by the 7-day default.
func TestSDKPubSubExplicitRetentionPreserved(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/ret")

	created, err := svc.Projects.Subscriptions.Create("projects/demo/subscriptions/ret",
		&pubsubv1.Subscription{
			Topic:                    "projects/demo/topics/ret",
			MessageRetentionDuration: "3600s",
		}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.MessageRetentionDuration != "3600s" {
		t.Errorf("create: messageRetentionDuration=%q, want 3600s", created.MessageRetentionDuration)
	}
}
