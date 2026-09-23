package redshift

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const testTopicARN = "arn:aws:sns:us-east-1:123456789012:events"

func TestEventSubscriptionSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	sub, err := src.CreateEventSubscription(ctx, EventSubscriptionConfig{
		Name: "sub-1", SnsTopicARN: testTopicARN, EventCategories: []string{"management"},
		Severity: "ERROR", Tags: map[string]string{"env": "dev"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := dst.DescribeEventSubscriptions(ctx, "sub-1")
	if err != nil || len(got) != 1 || got[0].Severity != "ERROR" || got[0].ARN != sub.ARN {
		t.Fatalf("restored = %+v, err %v", got, err)
	}

	tags, _ := dst.DescribeTags(ctx, sub.ARN)
	if tags["env"] != "dev" {
		t.Fatalf("restored tags = %v", tags)
	}
}

func TestEventSubscriptionValidation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	cases := []struct {
		name string
		cfg  EventSubscriptionConfig
		code cerrors.Code
	}{
		{"bad name", EventSubscriptionConfig{Name: "1bad", SnsTopicARN: testTopicARN}, cerrors.InvalidArgument},
		{"double hyphen", EventSubscriptionConfig{Name: "a--b", SnsTopicARN: testTopicARN}, cerrors.InvalidArgument},
		{"bad topic", EventSubscriptionConfig{Name: "a", SnsTopicARN: "not-an-arn"}, cerrors.InvalidArgument},
		{"bad severity", EventSubscriptionConfig{Name: "a", SnsTopicARN: testTopicARN, Severity: "WARN"}, cerrors.NotFound},
		{"bad category", EventSubscriptionConfig{Name: "a", SnsTopicARN: testTopicARN, EventCategories: []string{"x"}}, cerrors.NotFound},
		{"ids without type", EventSubscriptionConfig{Name: "a", SnsTopicARN: testTopicARN, SourceIDs: []string{"c"}}, cerrors.InvalidArgument},
		{"missing cluster", EventSubscriptionConfig{
			Name: "a", SnsTopicARN: testTopicARN, SourceType: "cluster", SourceIDs: []string{"nope"},
		}, cerrors.NotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.CreateEventSubscription(ctx, tc.cfg)
			if cerrors.GetCode(err) != tc.code {
				t.Fatalf("err = %v, want code %v", err, tc.code)
			}
		})
	}
}
