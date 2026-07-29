package rds

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestEventSubscriptionLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	sub, err := m.CreateEventSubscription(ctx, rdsdriver.EventSubscriptionConfig{
		Name:            "sub",
		SnsTopicARN:     "arn:aws:sns:us-east-1:123456789012:rds-events",
		SourceType:      "db-instance",
		EventCategories: []string{"failure", "failover"},
		SourceIDs:       []string{"mydb"},
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("CreateEventSubscription: %v", err)
	}

	if sub.ARN == "" || sub.Status != "active" || !sub.Enabled {
		t.Fatalf("subscription fields wrong: %+v", sub)
	}

	if _, err := m.CreateEventSubscription(ctx, rdsdriver.EventSubscriptionConfig{Name: "sub", SnsTopicARN: "arn:x"}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate: want AlreadyExists, got %v", err)
	}

	if _, err := m.CreateEventSubscription(ctx, rdsdriver.EventSubscriptionConfig{Name: "s2"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing sns: want InvalidArgument, got %v", err)
	}

	disabled := false
	got, err := m.ModifyEventSubscription(ctx, "sub", rdsdriver.ModifyEventSubscriptionInput{
		Enabled:         &disabled,
		EventCategories: []string{"maintenance"},
	})
	if err != nil {
		t.Fatalf("ModifyEventSubscription: %v", err)
	}

	if got.Enabled || len(got.EventCategories) != 1 || got.EventCategories[0] != "maintenance" {
		t.Fatalf("modify not applied: %+v", got)
	}

	if _, err := m.DeleteEventSubscription(ctx, "sub"); err != nil {
		t.Fatalf("DeleteEventSubscription: %v", err)
	}

	if _, err := m.DescribeEventSubscriptions(ctx, []string{"sub"}); !cerrors.IsNotFound(err) {
		t.Fatalf("describe deleted: want NotFound, got %v", err)
	}
}

func TestDescribeEventsAndCategories(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	// No event timeline is retained, so events are always empty.
	events, err := m.DescribeEvents(ctx, "db-instance", "mydb", nil)
	if err != nil || len(events) != 0 {
		t.Fatalf("DescribeEvents: got %d events, err %v", len(events), err)
	}

	groups, err := m.DescribeEventCategories(ctx, "db-instance")
	if err != nil || len(groups) != 1 || len(groups[0].EventCategories) == 0 {
		t.Fatalf("DescribeEventCategories(db-instance): %+v err %v", groups, err)
	}

	if groups, _ := m.DescribeEventCategories(ctx, "no-such-type"); len(groups) != 0 {
		t.Fatalf("unknown source type: got %d groups, want 0", len(groups))
	}

	if groups, _ := m.DescribeEventCategories(ctx, ""); len(groups) == 0 {
		t.Fatal("all source types: expected a non-empty catalog")
	}
}
