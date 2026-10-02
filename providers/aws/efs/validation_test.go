package efs_test

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func TestCreateFileSystemModeValidation(t *testing.T) {
	tests := []struct {
		name string
		in   driver.CreateFileSystemInput
	}{
		{"bogus performance", driver.CreateFileSystemInput{PerformanceMode: "bogusMode"}},
		{"maxIO one zone", driver.CreateFileSystemInput{PerformanceMode: driver.PerformanceMaxIO, AvailabilityZoneName: "us-east-1a"}},
		{"maxIO elastic", driver.CreateFileSystemInput{PerformanceMode: driver.PerformanceMaxIO, ThroughputMode: driver.ThroughputElastic}},
		{"bogus throughput", driver.CreateFileSystemInput{ThroughputMode: "bogus"}},
		{"provisioned too high", driver.CreateFileSystemInput{
			ThroughputMode: driver.ThroughputProvisioned, ProvisionedThroughputInMibps: 999999,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMock(t)
			tt.in.CreationToken = "t"

			if _, err := m.CreateFileSystem(context.Background(), tt.in); !errors.IsInvalidArgument(err) {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}

			// A rejected create must not claim the token.
			if _, err := m.CreateFileSystem(context.Background(), driver.CreateFileSystemInput{CreationToken: "t"}); err != nil {
				t.Fatalf("retry with same token: %v", err)
			}
		})
	}
}

func TestCreateFileSystemValidModes(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	valid := []driver.CreateFileSystemInput{
		{CreationToken: "a", PerformanceMode: driver.PerformanceMaxIO},
		{CreationToken: "b", ThroughputMode: driver.ThroughputElastic},
		{CreationToken: "c", ThroughputMode: driver.ThroughputProvisioned, ProvisionedThroughputInMibps: 3414},
	}

	for _, in := range valid {
		if _, err := m.CreateFileSystem(ctx, in); err != nil {
			t.Fatalf("create %s: %v", in.CreationToken, err)
		}
	}
}

func TestUpdateFileSystemValidation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "t"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = m.UpdateFileSystem(ctx, driver.UpdateFileSystemInput{
		FileSystemID: fs.FileSystemID, ThroughputMode: driver.ThroughputProvisioned,
	})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("provisioned without value err = %v, want InvalidArgument", err)
	}

	_, err = m.UpdateFileSystem(ctx, driver.UpdateFileSystemInput{FileSystemID: fs.FileSystemID, ThroughputMode: "bogus"})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("bogus mode err = %v, want InvalidArgument", err)
	}

	got, err := m.DescribeFileSystems(ctx, fs.FileSystemID, "")
	if err != nil || got[0].ThroughputMode != driver.ThroughputBursting {
		t.Fatalf("file system changed after rejected update: %+v, %v", got, err)
	}

	if _, err = m.UpdateFileSystem(ctx, driver.UpdateFileSystemInput{
		FileSystemID: fs.FileSystemID, ThroughputMode: driver.ThroughputProvisioned, ProvisionedThroughputInMibps: 100,
	}); err != nil {
		t.Fatalf("valid update: %v", err)
	}

	if _, err = m.UpdateFileSystem(ctx, driver.UpdateFileSystemInput{FileSystemID: fs.FileSystemID}); err != nil {
		t.Fatalf("no-op update on provisioned: %v", err)
	}
}

type fakeSubnets map[string]netdriver.SubnetInfo

func (f fakeSubnets) DescribeSubnets(_ context.Context, ids []string) ([]netdriver.SubnetInfo, error) {
	out := make([]netdriver.SubnetInfo, 0, len(ids))
	for _, id := range ids {
		if s, ok := f[id]; ok {
			out = append(out, s)
		}
	}

	return out, nil
}

func TestOneZoneMountTargetZone(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	m.SetSubnetResolver(fakeSubnets{
		"subnet-a": {ID: "subnet-a", VPCID: "vpc-1", AvailabilityZone: "us-east-1a"},
		"subnet-b": {ID: "subnet-b", VPCID: "vpc-1", AvailabilityZone: "us-east-1b"},
	})

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "t", AvailabilityZoneName: "us-east-1b"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = m.CreateMountTarget(ctx, driver.CreateMountTargetInput{FileSystemID: fs.FileSystemID, SubnetID: "subnet-a"})

	var re *driver.ResourceError
	if !errors.IsInvalidArgument(err) || !stderrors.As(err, &re) || re.Kind != driver.KindAvailabilityZone {
		t.Fatalf("wrong zone err = %v, want AvailabilityZone InvalidArgument", err)
	}

	if _, err = m.CreateMountTarget(ctx, driver.CreateMountTargetInput{FileSystemID: fs.FileSystemID, SubnetID: "subnet-b"}); err != nil {
		t.Fatalf("same zone: %v", err)
	}
}

func TestOneZoneMountTargetUnresolvedSubnetUsesFileSystemZone(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "t", AvailabilityZoneName: "us-east-1c"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mt, err := m.CreateMountTarget(ctx, driver.CreateMountTargetInput{FileSystemID: fs.FileSystemID, SubnetID: "subnet-x"})
	if err != nil {
		t.Fatalf("create mount target: %v", err)
	}

	if mt.AvailabilityZoneName != "us-east-1c" {
		t.Fatalf("AvailabilityZoneName = %q, want us-east-1c", mt.AvailabilityZoneName)
	}
}
