package dynamodb

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

func TestUpdateTableSettings(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, m.CreateTable(ctx, driver.TableConfig{
		Name:                      "t",
		PartitionKey:              "pk",
		DeletionProtectionEnabled: true,
		TableClass:                "STANDARD_INFREQUENT_ACCESS",
	}))

	cfg, err := m.DescribeTable(ctx, "t")
	requireNoError(t, err)
	assertEqual(t, true, cfg.DeletionProtectionEnabled)
	assertEqual(t, "STANDARD_INFREQUENT_ACCESS", cfg.TableClass)

	// A change to one setting leaves the other untouched.
	off := false
	requireNoError(t, m.UpdateTableSettings(ctx, "t", "", &off))

	cfg, err = m.DescribeTable(ctx, "t")
	requireNoError(t, err)
	assertEqual(t, false, cfg.DeletionProtectionEnabled)
	assertEqual(t, "STANDARD_INFREQUENT_ACCESS", cfg.TableClass)

	requireNoError(t, m.UpdateTableSettings(ctx, "t", "STANDARD", nil))
	cfg, err = m.DescribeTable(ctx, "t")
	requireNoError(t, err)
	assertEqual(t, "STANDARD", cfg.TableClass)
	assertEqual(t, false, cfg.DeletionProtectionEnabled)
}

func TestUpdateTableSettingsMissingTable(t *testing.T) {
	m := newTestMock()
	err := m.UpdateTableSettings(context.Background(), "nope", "STANDARD", nil)
	if !cerrors.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestDeleteTableDeletionProtection(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, m.CreateTable(ctx, driver.TableConfig{
		Name:                      "t",
		PartitionKey:              "pk",
		DeletionProtectionEnabled: true,
	}))

	if err := m.DeleteTable(ctx, "t"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("expected InvalidArgument on protected delete, got %v", err)
	}

	on := false
	requireNoError(t, m.UpdateTableSettings(ctx, "t", "", &on))
	requireNoError(t, m.DeleteTable(ctx, "t"))
}
