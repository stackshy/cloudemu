package ec2

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

func TestCreatePlacementGroupDuplicate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := driver.PlacementGroupConfig{Name: "pg", Strategy: "cluster"}
	if _, err := m.CreatePlacementGroup(ctx, cfg); err != nil {
		t.Fatalf("CreatePlacementGroup: %v", err)
	}

	_, err := m.CreatePlacementGroup(ctx, cfg)
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate CreatePlacementGroup err = %v, want AlreadyExists", err)
	}
}

func TestCreatePlacementGroupInvalidStrategy(t *testing.T) {
	m := newTestMock()

	_, err := m.CreatePlacementGroup(context.Background(),
		driver.PlacementGroupConfig{Name: "pg", Strategy: "bogus"})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("invalid strategy err = %v, want InvalidArgument", err)
	}
}

func TestDescribePlacementGroupsUnknownNameNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.DescribePlacementGroups(context.Background(), []string{"missing"}, nil)
	if !cerrors.IsNotFound(err) {
		t.Fatalf("unknown name err = %v, want NotFound", err)
	}
}

func TestDeletePlacementGroupUnknownNotFound(t *testing.T) {
	m := newTestMock()

	err := m.DeletePlacementGroup(context.Background(), "missing")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("delete unknown err = %v, want NotFound", err)
	}
}

func TestSnapshotCreateVolumePermissionAddRemove(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10, AvailabilityZone: "us-east-1a"})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	snap, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: vol.ID})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if err := m.ModifySnapshotAttribute(ctx, driver.ModifySnapshotAttributeInput{
		SnapshotID: snap.ID, OperationType: "add", Groups: []string{"all"},
	}); err != nil {
		t.Fatalf("ModifySnapshotAttribute add: %v", err)
	}

	perms, err := m.DescribeSnapshotVolumePermissions(ctx, snap.ID)
	if err != nil {
		t.Fatalf("DescribeSnapshotVolumePermissions: %v", err)
	}
	if len(perms) != 1 || perms[0].Group != "all" {
		t.Fatalf("perms after add = %+v, want one group=all", perms)
	}

	if err := m.ModifySnapshotAttribute(ctx, driver.ModifySnapshotAttributeInput{
		SnapshotID: snap.ID, OperationType: "remove", Groups: []string{"all"},
	}); err != nil {
		t.Fatalf("ModifySnapshotAttribute remove: %v", err)
	}

	perms, err = m.DescribeSnapshotVolumePermissions(ctx, snap.ID)
	if err != nil {
		t.Fatalf("DescribeSnapshotVolumePermissions: %v", err)
	}
	if len(perms) != 0 {
		t.Fatalf("perms after remove = %+v, want empty", perms)
	}
}

func TestModifyInstanceMetadataOptionsLeavesUnsetUnchanged(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "ami-1"}, 1)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	id := insts[0].ID

	// Only change HttpTokens; the hop limit default (1) must survive.
	opts, err := m.ModifyInstanceMetadataOptions(ctx, id, driver.MetadataOptions{HTTPTokens: "required"})
	if err != nil {
		t.Fatalf("ModifyInstanceMetadataOptions: %v", err)
	}
	if opts.HTTPTokens != "required" {
		t.Errorf("HTTPTokens = %q, want required", opts.HTTPTokens)
	}
	if opts.HTTPPutResponseHopLimit != 1 {
		t.Errorf("HTTPPutResponseHopLimit = %d, want 1 (unchanged default)", opts.HTTPPutResponseHopLimit)
	}
}
