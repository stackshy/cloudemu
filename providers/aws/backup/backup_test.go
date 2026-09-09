package backup_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/backup"
	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

func newMock() *backup.Mock {
	return backup.New(config.NewOptions())
}

func newMockAt(t time.Time) (*backup.Mock, *config.FakeClock) {
	clock := config.NewFakeClock(t)

	return backup.New(config.NewOptions(config.WithClock(clock))), clock
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustVault(t *testing.T, m *backup.Mock, name string) *driver.Vault {
	t.Helper()

	v, err := m.CreateBackupVault(context.Background(), &driver.CreateVaultInput{Name: name})
	requireNoError(t, err)

	return v
}

func samplePlan(name string) *driver.CreatePlanInput {
	days := int64(30)

	return &driver.CreatePlanInput{
		Body: driver.PlanBody{
			BackupPlanName: name,
			Rules: []driver.Rule{{
				RuleName:              "daily",
				TargetBackupVaultName: "v1",
				ScheduleExpression:    "cron(0 5 ? * * *)",
				Lifecycle:             &driver.Lifecycle{DeleteAfterDays: &days},
			}},
		},
	}
}

func TestVaultComputedFieldsStable(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created := mustVault(t, m, "v1")

	d1, err := m.DescribeBackupVault(ctx, "v1")
	requireNoError(t, err)

	d2, err := m.DescribeBackupVault(ctx, "v1")
	requireNoError(t, err)

	if created.Arn != d1.Arn || d1.Arn != d2.Arn {
		t.Fatalf("vault ARN drifted: %q / %q / %q", created.Arn, d1.Arn, d2.Arn)
	}

	if !d1.CreationDate.Equal(d2.CreationDate) {
		t.Fatalf("CreationDate drifted")
	}

	want := "arn:aws:backup:us-east-1:123456789012:backup-vault:v1"
	if d1.Arn != want {
		t.Fatalf("arn = %q, want %q", d1.Arn, want)
	}
}

func TestVaultDuplicateRejected(t *testing.T) {
	m := newMock()
	mustVault(t, m, "dup")

	_, err := m.CreateBackupVault(context.Background(), &driver.CreateVaultInput{Name: "dup"})
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("expected AlreadyExists, got %v", err)
	}
}

func TestVaultLockComplianceBecomesImmutable(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m, clock := newMockAt(base)
	ctx := context.Background()
	mustVault(t, m, "vault")

	days := int64(3)
	requireNoError(t, m.PutBackupVaultLockConfiguration(ctx, &driver.PutVaultLockInput{
		Name: "vault", MinRetentionDays: &days, ChangeableForDays: &days,
	}))

	d, err := m.DescribeBackupVault(ctx, "vault")
	requireNoError(t, err)

	if !d.Locked || d.LockDate == nil {
		t.Fatalf("COMPLIANCE lock must set Locked and a LockDate")
	}

	// Before the lock date the lock is still changeable / removable.
	if err := m.DeleteBackupVaultLockConfiguration(ctx, "vault"); err != nil {
		t.Fatalf("delete before lock date should succeed: %v", err)
	}

	// Re-lock and advance past the lock date: now immutable.
	requireNoError(t, m.PutBackupVaultLockConfiguration(ctx, &driver.PutVaultLockInput{
		Name: "vault", MinRetentionDays: &days, ChangeableForDays: &days,
	}))
	clock.Advance(4 * 24 * time.Hour)

	if err := m.DeleteBackupVaultLockConfiguration(ctx, "vault"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete after lock date must fail with FailedPrecondition, got %v", err)
	}

	if err := m.DeleteBackupVault(ctx, "vault"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete of an immutable-locked vault must fail, got %v", err)
	}
}

func TestVaultLockChangeableForDaysFloor(t *testing.T) {
	m := newMock()
	mustVault(t, m, "vault")

	two := int64(2)
	err := m.PutBackupVaultLockConfiguration(context.Background(), &driver.PutVaultLockInput{
		Name: "vault", ChangeableForDays: &two,
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("ChangeableForDays<3 must be rejected, got %v", err)
	}
}

func TestPlanVersioningAndRuleIDsStable(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	p, err := m.CreateBackupPlan(ctx, samplePlan("p1"))
	requireNoError(t, err)

	v1 := p.Current().VersionID
	ruleID := p.Current().Body.Rules[0].RuleID

	if ruleID == "" {
		t.Fatalf("rule id must be minted")
	}

	// A repeated read reports the same rule id (byte-stable).
	got, _, err := m.GetBackupPlan(ctx, p.BackupPlanID, "")
	requireNoError(t, err)

	if got.Current().Body.Rules[0].RuleID != ruleID {
		t.Fatalf("rule id drifted across reads")
	}

	upd, err := m.UpdateBackupPlan(ctx, &driver.UpdatePlanInput{BackupPlanID: p.BackupPlanID, Body: samplePlan("p1-v2").Body})
	requireNoError(t, err)

	if upd.Current().VersionID == v1 {
		t.Fatalf("update must bump VersionId")
	}

	_, oldVer, err := m.GetBackupPlan(ctx, p.BackupPlanID, v1)
	requireNoError(t, err)

	if oldVer.Body.BackupPlanName != "p1" {
		t.Fatalf("old version must resolve by id")
	}

	_, versions, _, err := m.ListBackupPlanVersions(ctx, p.BackupPlanID, driver.Page{})
	requireNoError(t, err)

	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
}

func TestDeletePlanGuardedBySelection(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	p, err := m.CreateBackupPlan(ctx, samplePlan("p1"))
	requireNoError(t, err)

	sel, err := m.CreateBackupSelection(ctx, &driver.CreateSelectionInput{
		BackupPlanID: p.BackupPlanID,
		Body:         driver.SelectionBody{SelectionName: "s1", IamRoleArn: "arn:aws:iam::123456789012:role/backup"},
	})
	requireNoError(t, err)

	if _, err := m.DeleteBackupPlan(ctx, p.BackupPlanID); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete of referenced plan must fail, got %v", err)
	}

	requireNoError(t, m.DeleteBackupSelection(ctx, p.BackupPlanID, sel.SelectionID))

	if _, err := m.DeleteBackupPlan(ctx, p.BackupPlanID); err != nil {
		t.Fatalf("delete after removing selection: %v", err)
	}
}

func TestTagsRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	v := mustVault(t, m, "v1")

	requireNoError(t, m.TagResource(ctx, v.Arn, map[string]string{"a": "1", "b": "2"}))

	tags, err := m.ListTags(ctx, v.Arn)
	requireNoError(t, err)

	if tags["a"] != "1" || tags["b"] != "2" {
		t.Fatalf("tags not stored: %v", tags)
	}

	requireNoError(t, m.UntagResource(ctx, v.Arn, []string{"a"}))

	tags, _ = m.ListTags(ctx, v.Arn)
	if _, ok := tags["a"]; ok {
		t.Fatalf("tag a should be removed")
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	v := mustVault(t, m, "v1")
	p, err := m.CreateBackupPlan(ctx, samplePlan("p1"))
	requireNoError(t, err)

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, data))

	rv, err := restored.DescribeBackupVault(ctx, "v1")
	requireNoError(t, err)

	if rv.Arn != v.Arn {
		t.Fatalf("restored vault arn drifted: %q vs %q", rv.Arn, v.Arn)
	}

	rp, _, err := restored.GetBackupPlan(ctx, p.BackupPlanID, "")
	requireNoError(t, err)

	if rp.BackupPlanArn != p.BackupPlanArn || rp.Current().VersionID != p.Current().VersionID {
		t.Fatalf("restored plan identity drifted")
	}
}
