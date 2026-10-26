package efs_test

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/efs"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

func newAsyncMock(t *testing.T) (*efs.Mock, *config.FakeClock) {
	t.Helper()

	clk := config.NewFakeClock(time.Unix(1_700_000_000, 0))

	return efs.New(config.NewOptions(
		config.WithClock(clk),
		config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"),
		config.WithAsyncSettle(),
	)), clk
}

// isIncorrectFSState reports whether err is the IncorrectFileSystemLifeCycleState
// condition (FailedPrecondition tagged with the file-system-state kind).
func isIncorrectFSState(err error) bool {
	var re *driver.ResourceError

	return errors.IsFailedPrecondition(err) && stderrors.As(err, &re) && re.Kind == driver.KindFileSystemState
}

func fsState(t *testing.T, m *efs.Mock, id string) string {
	t.Helper()

	got, err := m.DescribeFileSystems(context.Background(), id, "")
	if err != nil {
		t.Fatalf("DescribeFileSystems(%s): %v", id, err)
	}

	return got[0].LifeCycleState
}

func mtState(t *testing.T, m *efs.Mock, id string) string {
	t.Helper()

	got, err := m.DescribeMountTargets(context.Background(), "", id, "")
	if err != nil {
		t.Fatalf("DescribeMountTargets(%s): %v", id, err)
	}

	return got[0].LifeCycleState
}

// TestAsyncFileSystemLifecycle pins the real EFS lifecycle under AsyncSettle:
// CreateFileSystem returns "creating" and only becomes "available" later;
// mount targets can't be created until then (IncorrectFileSystemLifeCycleState);
// DeleteFileSystem returns while the file system is "deleting" and it then
// disappears (FileSystemNotFound).
func TestAsyncFileSystemLifecycle(t *testing.T) {
	ctx := context.Background()
	m, clk := newAsyncMock(t)

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "tok"})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	if fs.LifeCycleState != driver.StateCreating {
		t.Fatalf("create response state = %q, want creating", fs.LifeCycleState)
	}

	if got := fsState(t, m, fs.FileSystemID); got != driver.StateCreating {
		t.Fatalf("describe right after create = %q, want creating", got)
	}

	_, err = m.CreateMountTarget(ctx, driver.CreateMountTargetInput{FileSystemID: fs.FileSystemID, SubnetID: "subnet-1"})
	if !isIncorrectFSState(err) {
		t.Fatalf("CreateMountTarget on creating fs: want IncorrectFileSystemLifeCycleState, got %v", err)
	}

	if _, err = m.UpdateFileSystemProtection(ctx, fs.FileSystemID, "DISABLED"); !isIncorrectFSState(err) {
		t.Fatalf("UpdateFileSystemProtection on creating fs: want IncorrectFileSystemLifeCycleState, got %v", err)
	}

	clk.Advance(time.Minute)

	if got := fsState(t, m, fs.FileSystemID); got != driver.StateAvailable {
		t.Fatalf("after settle = %q, want available", got)
	}

	mt, err := m.CreateMountTarget(ctx, driver.CreateMountTargetInput{FileSystemID: fs.FileSystemID, SubnetID: "subnet-1"})
	if err != nil {
		t.Fatalf("CreateMountTarget: %v", err)
	}

	if mt.LifeCycleState != driver.StateCreating {
		t.Fatalf("mount target create state = %q, want creating", mt.LifeCycleState)
	}

	clk.Advance(time.Minute)

	if got := mtState(t, m, mt.MountTargetID); got != driver.StateAvailable {
		t.Fatalf("mount target after settle = %q, want available", got)
	}

	if err = m.DeleteMountTarget(ctx, mt.MountTargetID); err != nil {
		t.Fatalf("DeleteMountTarget: %v", err)
	}

	if got := mtState(t, m, mt.MountTargetID); got != driver.StateDeleting {
		t.Fatalf("mount target after delete = %q, want deleting", got)
	}

	// Still present while deleting, so the file system is still in use.
	if err = m.DeleteFileSystem(ctx, fs.FileSystemID); !errors.IsFailedPrecondition(err) || isIncorrectFSState(err) {
		t.Fatalf("DeleteFileSystem with deleting mount target: want FileSystemInUse, got %v", err)
	}

	clk.Advance(time.Minute)

	if _, err = m.DescribeMountTargets(ctx, "", mt.MountTargetID, ""); !errors.IsNotFound(err) {
		t.Fatalf("mount target after delete settle: want NotFound, got %v", err)
	}

	if got := fsState(t, m, fs.FileSystemID); got != driver.StateAvailable {
		t.Fatalf("fs state = %q, want available", got)
	}

	if err = m.DeleteFileSystem(ctx, fs.FileSystemID); err != nil {
		t.Fatalf("DeleteFileSystem: %v", err)
	}

	if got := fsState(t, m, fs.FileSystemID); got != driver.StateDeleting {
		t.Fatalf("fs after delete = %q, want deleting", got)
	}

	if _, err = m.PutBackupPolicy(ctx, fs.FileSystemID, driver.BackupEnabled); !isIncorrectFSState(err) {
		t.Fatalf("PutBackupPolicy on deleting fs: want IncorrectFileSystemLifeCycleState, got %v", err)
	}

	clk.Advance(time.Minute)

	if _, err = m.DescribeFileSystems(ctx, fs.FileSystemID, ""); !errors.IsNotFound(err) {
		t.Fatalf("fs after delete settle: want NotFound, got %v", err)
	}

	all, err := m.DescribeFileSystems(ctx, "", "")
	if err != nil || len(all) != 0 {
		t.Fatalf("describe all after delete = %v, %v; want empty", all, err)
	}

	// The creation token is released once the file system is gone.
	if _, err = m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "tok"}); err != nil {
		t.Fatalf("re-create with released token: %v", err)
	}
}

// TestSyncDefaultIsImmediate pins the historical default (AsyncSettle off):
// everything is available immediately and deletes are instant.
func TestSyncDefaultIsImmediate(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "tok"})
	if err != nil || fs.LifeCycleState != driver.StateAvailable {
		t.Fatalf("create = %+v, %v; want available", fs, err)
	}

	mt, err := m.CreateMountTarget(ctx, driver.CreateMountTargetInput{FileSystemID: fs.FileSystemID, SubnetID: "subnet-1"})
	if err != nil || mt.LifeCycleState != driver.StateAvailable {
		t.Fatalf("create mt = %+v, %v; want available", mt, err)
	}

	if err = m.DeleteMountTarget(ctx, mt.MountTargetID); err != nil {
		t.Fatalf("DeleteMountTarget: %v", err)
	}

	if _, err = m.DescribeMountTargets(ctx, "", mt.MountTargetID, ""); !errors.IsNotFound(err) {
		t.Fatalf("mount target after sync delete: want NotFound, got %v", err)
	}

	if err = m.DeleteFileSystem(ctx, fs.FileSystemID); err != nil {
		t.Fatalf("DeleteFileSystem: %v", err)
	}

	if _, err = m.DescribeFileSystems(ctx, fs.FileSystemID, ""); !errors.IsNotFound(err) {
		t.Fatalf("fs after sync delete: want NotFound, got %v", err)
	}
}

func TestUpdateFileSystemProtection(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "tok"})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	if fs.Protection.ReplicationOverwriteProtection != driver.ProtectionEnabled {
		t.Fatalf("default protection = %q, want ENABLED", fs.Protection.ReplicationOverwriteProtection)
	}

	got, err := m.UpdateFileSystemProtection(ctx, fs.FileSystemID, driver.ProtectionDisabled)
	if err != nil || got.ReplicationOverwriteProtection != driver.ProtectionDisabled {
		t.Fatalf("UpdateFileSystemProtection = %+v, %v; want DISABLED", got, err)
	}

	desc, err := m.DescribeFileSystems(ctx, fs.FileSystemID, "")
	if err != nil || desc[0].Protection.ReplicationOverwriteProtection != driver.ProtectionDisabled {
		t.Fatalf("describe protection = %+v, %v; want DISABLED", desc, err)
	}

	// Omitting the value leaves the current setting unchanged.
	if got, err = m.UpdateFileSystemProtection(ctx, fs.FileSystemID, ""); err != nil ||
		got.ReplicationOverwriteProtection != driver.ProtectionDisabled {
		t.Fatalf("empty update = %+v, %v; want unchanged DISABLED", got, err)
	}

	if _, err = m.UpdateFileSystemProtection(ctx, fs.FileSystemID, "BOGUS"); !errors.IsInvalidArgument(err) {
		t.Fatalf("invalid value: want InvalidArgument, got %v", err)
	}

	if _, err = m.UpdateFileSystemProtection(ctx, "fs-0123456789abcdef0", driver.ProtectionEnabled); !errors.IsNotFound(err) {
		t.Fatalf("unknown fs: want NotFound, got %v", err)
	}
}

// TestLifecycleSurvivesSnapshot checks a deleting file system and mount target
// keep their transient state (and later vanish) across Snapshot/Restore.
func TestLifecycleSurvivesSnapshot(t *testing.T) {
	ctx := context.Background()
	m, clk := newAsyncMock(t)

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "tok"})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	clk.Advance(time.Minute)

	mt, err := m.CreateMountTarget(ctx, driver.CreateMountTargetInput{FileSystemID: fs.FileSystemID, SubnetID: "subnet-1"})
	if err != nil {
		t.Fatalf("CreateMountTarget: %v", err)
	}

	if err = m.DeleteMountTarget(ctx, mt.MountTargetID); err != nil {
		t.Fatalf("DeleteMountTarget: %v", err)
	}

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := efs.New(config.NewOptions(config.WithClock(clk), config.WithAsyncSettle()))
	if err = restored.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := mtState(t, restored, mt.MountTargetID); got != driver.StateDeleting {
		t.Fatalf("restored mount target = %q, want deleting", got)
	}

	clk.Advance(time.Minute)

	if _, err = restored.DescribeMountTargets(ctx, "", mt.MountTargetID, ""); !errors.IsNotFound(err) {
		t.Fatalf("restored mount target after settle: want NotFound, got %v", err)
	}
}
