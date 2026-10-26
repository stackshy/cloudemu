package efs_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/efs"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// TestSnapshotConcurrentWithLifecycle runs Snapshot while requests and Tick keep
// writing the per-file-system maps (mount targets settling and being reaped,
// tags changing). Snapshot marshals after it releases the file-system lock, so
// it must copy those maps first; under -race this fails if it does not.
func TestSnapshotConcurrentWithLifecycle(t *testing.T) {
	ctx := context.Background()
	m, clk := newAsyncMock(t)
	m.SetMonitoring(cloudwatch.New(config.NewOptions(config.WithClock(clk))))

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "tok"})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	clk.Advance(time.Minute)

	ap, err := m.CreateAccessPoint(ctx, driver.CreateAccessPointInput{FileSystemID: fs.FileSystemID})
	if err != nil {
		t.Fatalf("CreateAccessPoint: %v", err)
	}

	const rounds = 200

	var wg sync.WaitGroup

	wg.Add(3)

	go func() {
		defer wg.Done()

		for i := range rounds {
			mt, err := m.CreateMountTarget(ctx, driver.CreateMountTargetInput{
				FileSystemID: fs.FileSystemID, SubnetID: fmt.Sprintf("subnet-%d", i),
			})
			if err != nil {
				continue
			}

			_ = m.DeleteMountTarget(ctx, mt.MountTargetID)
			_ = m.TagResource(ctx, fs.FileSystemID, map[string]string{"k": fmt.Sprint(i)})
			_ = m.TagResource(ctx, ap.AccessPointID, map[string]string{"k": fmt.Sprint(i)})
			clk.Advance(time.Second)
		}
	}()

	go func() {
		defer wg.Done()

		for range rounds {
			m.Tick(clk.Now())
			_, _ = m.DescribeFileSystems(ctx, fs.FileSystemID, "")
		}
	}()

	go func() {
		defer wg.Done()

		for range rounds {
			if _, err := m.Snapshot(ctx, false); err != nil {
				t.Errorf("Snapshot: %v", err)

				return
			}
		}
	}()

	wg.Wait()
}

// TestCreateAccessPointRacesDeleteFileSystem checks the lifecycle check and the
// store in CreateAccessPoint are atomic: when DeleteFileSystem and
// CreateAccessPoint race, either the delete sees the access point
// (FileSystemInUse) or the create sees the file system "deleting", never both
// succeeding with the access point left on a file system that is then reaped.
func TestCreateAccessPointRacesDeleteFileSystem(t *testing.T) {
	t.Run("async", func(t *testing.T) {
		m, clk := newAsyncMock(t)
		raceAccessPointAgainstDelete(t, m, clk)
	})

	t.Run("sync", func(t *testing.T) {
		clk := config.NewFakeClock(time.Unix(1_700_000_000, 0))
		raceAccessPointAgainstDelete(t, efs.New(config.NewOptions(config.WithClock(clk))), clk)
	})
}

func raceAccessPointAgainstDelete(t *testing.T, m *efs.Mock, clk *config.FakeClock) {
	t.Helper()

	ctx := context.Background()

	for i := range 500 {
		fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: fmt.Sprintf("tok-%d", i)})
		if err != nil {
			t.Fatalf("CreateFileSystem: %v", err)
		}

		clk.Advance(time.Minute)

		var (
			wg           sync.WaitGroup
			apErr, delEr error
			ap           *driver.AccessPoint
		)

		wg.Add(2)

		go func() {
			defer wg.Done()

			ap, apErr = m.CreateAccessPoint(ctx, driver.CreateAccessPointInput{FileSystemID: fs.FileSystemID})
		}()

		go func() {
			defer wg.Done()

			delEr = m.DeleteFileSystem(ctx, fs.FileSystemID)
		}()

		wg.Wait()

		if apErr == nil && delEr == nil {
			t.Fatalf("round %d: CreateAccessPoint (%s) and DeleteFileSystem both succeeded", i, ap.AccessPointID)
		}
	}
}

// TestUpdateFileSystemProtectionRejectsReplicating checks REPLICATING, the state
// EFS sets on a replication destination, is not accepted from a caller.
func TestUpdateFileSystemProtectionRejectsReplicating(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "tok"})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	if _, err = m.UpdateFileSystemProtection(ctx, fs.FileSystemID, driver.ProtectionReplicating); !errors.IsInvalidArgument(err) {
		t.Fatalf("REPLICATING: want InvalidArgument (BadRequest), got %v", err)
	}

	desc, err := m.DescribeFileSystems(ctx, fs.FileSystemID, "")
	if err != nil || desc[0].Protection.ReplicationOverwriteProtection != driver.ProtectionEnabled {
		t.Fatalf("protection after rejected update = %+v, %v; want ENABLED", desc, err)
	}
}

// TestTickReportsReap checks Tick reports a reap as a state change (so serve
// persistence saves it) even when no metric is due or no CloudWatch is wired,
// and reports nothing once there is nothing left to do.
func TestTickReportsReap(t *testing.T) {
	ctx := context.Background()
	m, clk := newAsyncMock(t) // no monitoring wired: no metric is ever published

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "tok"})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	clk.Advance(time.Minute)
	m.Tick(clk.Now())

	if m.Tick(clk.Now()) {
		t.Fatal("idle Tick reported a change")
	}

	mt, err := m.CreateMountTarget(ctx, driver.CreateMountTargetInput{FileSystemID: fs.FileSystemID, SubnetID: "subnet-1"})
	if err != nil {
		t.Fatalf("CreateMountTarget: %v", err)
	}

	clk.Advance(time.Minute)
	m.Tick(clk.Now()) // drops the elapsed create window

	if err = m.DeleteMountTarget(ctx, mt.MountTargetID); err != nil {
		t.Fatalf("DeleteMountTarget: %v", err)
	}

	clk.Advance(time.Minute)

	if !m.Tick(clk.Now()) {
		t.Fatal("Tick that reaped a mount target reported no change")
	}

	if err = m.DeleteFileSystem(ctx, fs.FileSystemID); err != nil {
		t.Fatalf("DeleteFileSystem: %v", err)
	}

	clk.Advance(time.Minute)

	if !m.Tick(clk.Now()) {
		t.Fatal("Tick that reaped a file system reported no change")
	}

	if m.Tick(clk.Now()) {
		t.Fatal("Tick after everything was reaped reported a change")
	}
}

// TestRestoredEmptyFileSystemTakesMountTargets checks a file system snapshotted
// with no mount targets or access points (their maps are dropped by omitempty)
// can take new ones after Restore instead of panicking on a nil map.
func TestRestoredEmptyFileSystemTakesMountTargets(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	fs, err := src.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "tok"})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := efs.New(config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0)))))
	if err = dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if _, err = dst.CreateMountTarget(ctx, driver.CreateMountTargetInput{
		FileSystemID: fs.FileSystemID, SubnetID: "subnet-1",
	}); err != nil {
		t.Fatalf("CreateMountTarget after restore: %v", err)
	}

	if _, err = dst.CreateAccessPoint(ctx, driver.CreateAccessPointInput{FileSystemID: fs.FileSystemID}); err != nil {
		t.Fatalf("CreateAccessPoint after restore: %v", err)
	}
}
