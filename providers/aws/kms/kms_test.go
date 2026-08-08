package kms_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/kms"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

func newMock(t *testing.T) *kms.Mock {
	t.Helper()

	return kms.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Unix(0, 0))),
		config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"),
	))
}

func TestCreateAndDescribeKey(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{Description: "app key"})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	if k.KeyState != driver.StateEnabled || !k.Enabled {
		t.Fatalf("new key should be enabled, got state=%s enabled=%v", k.KeyState, k.Enabled)
	}

	if k.KeySpec != driver.SpecSymmetricDefault || k.KeyUsage != driver.UsageEncryptDecrypt {
		t.Fatalf("unexpected defaults: spec=%s usage=%s", k.KeySpec, k.KeyUsage)
	}

	// Describe by ID and by ARN must both resolve.
	if _, err := m.DescribeKey(ctx, k.KeyID); err != nil {
		t.Fatalf("DescribeKey by id: %v", err)
	}

	if _, err := m.DescribeKey(ctx, k.ARN); err != nil {
		t.Fatalf("DescribeKey by arn: %v", err)
	}
}

func TestAliasResolutionAndLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	k, _ := m.CreateKey(ctx, driver.CreateKeyInput{})

	if err := m.CreateAlias(ctx, "alias/app", k.KeyID); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}

	// Describe by alias resolves to the same key.
	got, err := m.DescribeKey(ctx, "alias/app")
	if err != nil {
		t.Fatalf("DescribeKey by alias: %v", err)
	}

	if got.KeyID != k.KeyID {
		t.Fatalf("alias resolved to %s, want %s", got.KeyID, k.KeyID)
	}

	// Reserved prefix rejected.
	if err := m.CreateAlias(ctx, "alias/aws/reserved", k.KeyID); !errors.IsInvalidArgument(err) {
		t.Fatalf("alias/aws/ should be rejected, got %v", err)
	}

	// Duplicate rejected.
	if err := m.CreateAlias(ctx, "alias/app", k.KeyID); !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate alias should fail, got %v", err)
	}
}

func TestScheduleAndCancelDeletion(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	k, _ := m.CreateKey(ctx, driver.CreateKeyInput{})

	md, err := m.ScheduleKeyDeletion(ctx, k.KeyID, 7)
	if err != nil {
		t.Fatalf("ScheduleKeyDeletion: %v", err)
	}

	if md.KeyState != driver.StatePendingDeletion || md.DeletionDate.IsZero() {
		t.Fatalf("expected pending deletion with a date, got %+v", md)
	}

	// Out-of-range window rejected.
	if _, err := m.ScheduleKeyDeletion(ctx, k.KeyID, 3); !errors.IsInvalidArgument(err) {
		t.Fatalf("window 3 should be rejected, got %v", err)
	}

	back, err := m.CancelKeyDeletion(ctx, k.KeyID)
	if err != nil {
		t.Fatalf("CancelKeyDeletion: %v", err)
	}

	if back.KeyState != driver.StateDisabled {
		t.Fatalf("cancelled key should be Disabled, got %s", back.KeyState)
	}
}

func TestTags(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	k, _ := m.CreateKey(ctx, driver.CreateKeyInput{Tags: map[string]string{"env": "prod"}})

	if err := m.TagResource(ctx, k.KeyID, map[string]string{"team": "platform"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := m.ListResourceTags(ctx, k.KeyID)
	if err != nil {
		t.Fatalf("ListResourceTags: %v", err)
	}

	if tags["env"] != "prod" || tags["team"] != "platform" {
		t.Fatalf("unexpected tags: %+v", tags)
	}

	if err := m.UntagResource(ctx, k.KeyID, []string{"env"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, _ = m.ListResourceTags(ctx, k.KeyID)
	if _, ok := tags["env"]; ok {
		t.Fatalf("env tag should be removed: %+v", tags)
	}
}
