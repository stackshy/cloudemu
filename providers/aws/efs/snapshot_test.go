package efs_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// TestSnapshotRoundTripEFS proves a snapshot/restore round-trip preserves the
// EFS mock's state under the original identities: a file system (promoted from
// the unexported fsData), its creation-token index, and the account resource-id
// preference survive restore into a fresh mock, and a re-snapshot is
// byte-identical (proving every captured store round-trips).
func TestSnapshotRoundTripEFS(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	fs, err := src.CreateFileSystem(ctx, driver.CreateFileSystemInput{
		CreationToken: "tok-1",
		Encrypted:     true,
		Tags:          map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("create file system: %v", err)
	}

	if _, err := src.PutAccountPreferences(ctx, "LONG_ID"); err != nil {
		t.Fatalf("put account preferences: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := dst.DescribeFileSystems(ctx, fs.FileSystemID, "")
	if err != nil {
		t.Fatalf("describe restored file system: %v", err)
	}

	if len(got) != 1 || got[0].CreationToken != "tok-1" || !got[0].Encrypted {
		t.Fatalf("restored file system = %+v, want token tok-1 / encrypted", got)
	}

	// Token index preserved: recreating with the same creation token is rejected
	// as a duplicate, proving the tokenIndex store survived the restore.
	if _, err := dst.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "tok-1"}); err == nil {
		t.Fatalf("token index lost: recreate with tok-1 should be rejected as a duplicate")
	}

	pref, err := dst.DescribeAccountPreferences(ctx)
	if err != nil {
		t.Fatalf("describe restored account preferences: %v", err)
	}

	if pref != "LONG_ID" {
		t.Fatalf("restored account preference = %q, want LONG_ID", pref)
	}

	raw2, err := dst.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("re-snapshot: %v", err)
	}

	if !bytes.Equal(raw, raw2) {
		t.Fatalf("re-snapshot not byte-identical to original")
	}
}

// TestSnapshotRoundTripEFSPreservesIPCounters verifies that the mount-target IP
// allocator's counters survive a snapshot/restore, so a mount target created
// after restore never reuses an IP already handed out before the snapshot
// (which would otherwise produce a duplicate IP once the in-memory counter
// resets to zero on a fresh mock).
func TestSnapshotRoundTripEFSPreservesIPCounters(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	fs, err := src.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "ip-snap"})
	if err != nil {
		t.Fatalf("create file system: %v", err)
	}

	mt1, err := src.CreateMountTarget(ctx, driver.CreateMountTargetInput{
		FileSystemID: fs.FileSystemID, SubnetID: "subnet-snap-1",
	})
	if err != nil {
		t.Fatalf("create mount target: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	mt2, err := dst.CreateMountTarget(ctx, driver.CreateMountTargetInput{
		FileSystemID: fs.FileSystemID, SubnetID: "subnet-snap-2",
	})
	if err != nil {
		t.Fatalf("create mount target after restore: %v", err)
	}

	if mt1.IPAddress == mt2.IPAddress {
		t.Fatalf("mount target created after restore reused IP %q handed out before the snapshot", mt1.IPAddress)
	}
}
