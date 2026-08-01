package rds

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// Real RDS refuses to delete a subnet group anything is still placed in. A
// teardown that got a success here would delete the group out from under a
// live instance and only discover the ordering mistake later, somewhere else.

func newRDSWithSubnetGroup(t *testing.T) *Mock {
	t.Helper()

	m := New(config.NewOptions())

	if _, err := m.CreateDBSubnetGroup(context.Background(), rdsdriver.SubnetGroupConfig{
		Name:      "test-group",
		SubnetIDs: []string{"subnet-1", "subnet-2"},
	}); err != nil {
		t.Fatalf("CreateDBSubnetGroup: %v", err)
	}

	return m
}

func TestDeleteDBSubnetGroup_RefusesWhileInstanceUsesIt(t *testing.T) {
	t.Parallel()

	m := newRDSWithSubnetGroup(t)
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:              "db-1",
		Engine:          "postgres",
		InstanceClass:   "db.t3.micro",
		SubnetGroupName: "test-group",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	err := m.DeleteDBSubnetGroup(ctx, "test-group")
	if err == nil {
		t.Fatal("got nil error deleting an in-use subnet group, want a refusal")
	}

	if !strings.Contains(err.Error(), "InvalidDBSubnetGroupStateFault") {
		t.Errorf("error = %q, want it to name InvalidDBSubnetGroupStateFault", err)
	}

	groups, err := m.DescribeDBSubnetGroups(ctx, nil)
	if err != nil {
		t.Fatalf("DescribeDBSubnetGroups: %v", err)
	}

	if len(groups) != 1 {
		t.Errorf("got %d subnet groups, want 1 — the refusal still deleted it", len(groups))
	}
}

func TestDeleteDBSubnetGroup_SucceedsOnceEmpty(t *testing.T) {
	t.Parallel()

	m := newRDSWithSubnetGroup(t)
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:              "db-1",
		Engine:          "postgres",
		InstanceClass:   "db.t3.micro",
		SubnetGroupName: "test-group",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := m.DeleteInstance(ctx, "db-1"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if err := m.DeleteDBSubnetGroup(ctx, "test-group"); err != nil {
		t.Errorf("DeleteDBSubnetGroup after the instance was removed: %v", err)
	}
}

// TestDeleteDBSubnetGroup_UnrelatedInstanceDoesNotBlock guards against the
// guard being too broad — an instance in a different group is not a reason to
// refuse.
func TestDeleteDBSubnetGroup_UnrelatedInstanceDoesNotBlock(t *testing.T) {
	t.Parallel()

	m := newRDSWithSubnetGroup(t)
	ctx := context.Background()

	if _, err := m.CreateDBSubnetGroup(ctx, rdsdriver.SubnetGroupConfig{
		Name:      "other-group",
		SubnetIDs: []string{"subnet-3"},
	}); err != nil {
		t.Fatalf("CreateDBSubnetGroup: %v", err)
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:              "db-1",
		Engine:          "postgres",
		InstanceClass:   "db.t3.micro",
		SubnetGroupName: "other-group",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := m.DeleteDBSubnetGroup(ctx, "test-group"); err != nil {
		t.Errorf("DeleteDBSubnetGroup for an unused group: %v", err)
	}
}

func TestDeleteDBSubnetGroup_UnknownIsNotFound(t *testing.T) {
	t.Parallel()

	m := New(config.NewOptions())

	err := m.DeleteDBSubnetGroup(context.Background(), "nope")
	if err == nil {
		t.Fatal("got nil error for an unknown subnet group")
	}

	if !strings.Contains(err.Error(), "DBSubnetGroupNotFoundFault") {
		t.Errorf("error = %q, want it to name DBSubnetGroupNotFoundFault", err)
	}
}
