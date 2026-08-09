package efs_test

import (
	"context"
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
