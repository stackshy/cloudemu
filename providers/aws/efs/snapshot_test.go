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
