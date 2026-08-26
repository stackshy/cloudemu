package efs_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/efs"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

func newMock(t *testing.T) *efs.Mock {
	t.Helper()

	return efs.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Unix(0, 0))),
		config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"),
	))
}

func TestCreateRequiresToken(t *testing.T) {
	m := newMock(t)

	if _, err := m.CreateFileSystem(context.Background(), driver.CreateFileSystemInput{}); !errors.IsInvalidArgument(err) {
		t.Fatalf("empty token should be InvalidArgument, got %v", err)
	}
}

func TestCreateDuplicateToken(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if _, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "t"}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "t"}); !errors.IsAlreadyExists(err) {
		t.Fatalf("dup token should be AlreadyExists, got %v", err)
	}
}

func TestProvisionedRequiresThroughput(t *testing.T) {
	m := newMock(t)

	_, err := m.CreateFileSystem(context.Background(), driver.CreateFileSystemInput{
		CreationToken: "t", ThroughputMode: driver.ThroughputProvisioned,
	})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("provisioned without mibps should be InvalidArgument, got %v", err)
	}
}

func TestDefaultsAndPolicy(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "t"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if fs.PerformanceMode != driver.PerformanceGeneralPurpose || fs.ThroughputMode != driver.ThroughputBursting {
		t.Fatalf("unexpected defaults: %+v", fs)
	}

	// Policy round-trip.
	if _, err := m.PutFileSystemPolicy(ctx, fs.FileSystemID, "{}", false); err != nil {
		t.Fatalf("put policy: %v", err)
	}

	got, err := m.DescribeFileSystemPolicy(ctx, fs.FileSystemID)
	if err != nil || got != "{}" {
		t.Fatalf("describe policy = %q, %v", got, err)
	}

	// Missing policy after delete.
	if err := m.DeleteFileSystemPolicy(ctx, fs.FileSystemID); err != nil {
		t.Fatalf("delete policy: %v", err)
	}

	if _, err := m.DescribeFileSystemPolicy(ctx, fs.FileSystemID); !errors.IsNotFound(err) {
		t.Fatalf("policy after delete should be NotFound, got %v", err)
	}
}

// TestCreateAccessPointClientTokenConcurrent stresses the ClientToken
// idempotency contract under contention: many concurrent CreateAccessPoint calls
// with the same token on one file system must all return the SAME access point
// (exactly one created), and a racing loser must never see AccessPointNotFound.
// Run with `go test -race -count=200 ./providers/aws/efs/...` to catch the TOCTOU
// window between claiming the token and publishing the access point.
func TestCreateAccessPointClientTokenConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "ct-fs"})
	if err != nil {
		t.Fatalf("create fs: %v", err)
	}

	const workers = 40

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		ids  = make(map[string]struct{})
		errs []error
	)

	wg.Add(workers)

	for range workers {
		go func() {
			defer wg.Done()

			ap, apErr := m.CreateAccessPoint(ctx, driver.CreateAccessPointInput{
				FileSystemID: fs.FileSystemID, ClientToken: "ct-1",
			})

			mu.Lock()
			defer mu.Unlock()

			if apErr != nil {
				errs = append(errs, apErr)
				return
			}

			ids[ap.AccessPointID] = struct{}{}
		}()
	}

	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("racing same-token creates returned %d error(s); first: %v", len(errs), errs[0])
	}

	if len(ids) != 1 {
		t.Fatalf("same ClientToken produced %d distinct access-point ids, want 1: %v", len(ids), ids)
	}

	aps, err := m.DescribeAccessPoints(ctx, fs.FileSystemID, "")
	if err != nil {
		t.Fatalf("describe access points: %v", err)
	}

	if len(aps) != 1 {
		t.Fatalf("want exactly 1 access point after idempotent races, got %d", len(aps))
	}
}
