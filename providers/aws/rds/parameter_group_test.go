package rds

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestDBParameterGroupLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	pg, err := m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{
		Name:        "pg-1",
		Family:      "mysql8.0",
		Description: "app params",
	})
	if err != nil {
		t.Fatalf("CreateDBParameterGroup: %v", err)
	}

	if pg.ARN == "" {
		t.Error("expected ARN to be set")
	}

	// Duplicate is rejected.
	if _, err := m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: "pg-1", Family: "mysql8.0"}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create: want AlreadyExists, got %v", err)
	}

	// Missing family is rejected.
	if _, err := m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: "pg-x"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing family: want InvalidArgument, got %v", err)
	}

	// Modify merges parameters, describe returns them.
	if _, err := m.ModifyDBParameterGroup(ctx, "pg-1", []rdsdriver.Parameter{
		{Name: "max_connections", Value: "200"},
		{Name: "slow_query_log", Value: "1"},
	}); err != nil {
		t.Fatalf("ModifyDBParameterGroup: %v", err)
	}

	params, err := m.DescribeDBParameters(ctx, "pg-1")
	if err != nil {
		t.Fatalf("DescribeDBParameters: %v", err)
	}

	// Describe returns the full engine-default set with the two modifications
	// overlaid as user-sourced (real RDS never returns an empty list).
	if len(params) < 10 {
		t.Fatalf("got %d params, want the full engine-default set", len(params))
	}
	if mc := findParam(params, "max_connections"); mc == nil || mc.Value != "200" || mc.Source != "user" {
		t.Fatalf("max_connections = %+v, want value 200 source user", mc)
	}
	if sq := findParam(params, "slow_query_log"); sq == nil || sq.Value != "1" || sq.Source != "user" {
		t.Fatalf("slow_query_log = %+v, want value 1 source user", sq)
	}
	if cs := findParam(params, "character_set_server"); cs == nil || cs.Source != "engine-default" {
		t.Fatalf("unmodified character_set_server = %+v, want engine-default", cs)
	}

	// Resetting a named parameter reverts it to engine-default; the other stays.
	if _, err := m.ResetDBParameterGroup(ctx, "pg-1", []string{"slow_query_log"}, false); err != nil {
		t.Fatalf("ResetDBParameterGroup: %v", err)
	}

	params, _ = m.DescribeDBParameters(ctx, "pg-1")
	if sq := findParam(params, "slow_query_log"); sq == nil || sq.Source != "engine-default" {
		t.Fatalf("slow_query_log after reset = %+v, want engine-default", sq)
	}
	if mc := findParam(params, "max_connections"); mc == nil || mc.Source != "user" {
		t.Fatalf("max_connections after resetting a different param = %+v, want still user", mc)
	}

	// Reset all: every parameter reverts to engine-default.
	if _, err := m.ResetDBParameterGroup(ctx, "pg-1", nil, true); err != nil {
		t.Fatalf("ResetDBParameterGroup all: %v", err)
	}

	params, _ = m.DescribeDBParameters(ctx, "pg-1")
	for i := range params {
		if params[i].Source == "user" {
			t.Fatalf("after reset all, %s is still user-sourced", params[i].Name)
		}
	}

	// Delete, then describe-missing is NotFound.
	if err := m.DeleteDBParameterGroup(ctx, "pg-1"); err != nil {
		t.Fatalf("DeleteDBParameterGroup: %v", err)
	}

	if _, err := m.DescribeDBParameterGroups(ctx, []string{"pg-1"}); !cerrors.IsNotFound(err) {
		t.Fatalf("describe deleted: want NotFound, got %v", err)
	}
}

func TestDBParameterGroupApplyMethodRoundTrips(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: "pg", Family: "mysql8.0"}); err != nil {
		t.Fatalf("CreateDBParameterGroup: %v", err)
	}

	if _, err := m.ModifyDBParameterGroup(ctx, "pg", []rdsdriver.Parameter{
		{Name: "max_connections", Value: "200", ApplyMethod: "immediate"},
	}); err != nil {
		t.Fatalf("ModifyDBParameterGroup: %v", err)
	}

	params, err := m.DescribeDBParameters(ctx, "pg")
	if err != nil {
		t.Fatalf("DescribeDBParameters: %v", err)
	}

	mc := findParam(params, "max_connections")
	if mc == nil || mc.ApplyMethod != "immediate" || mc.Source != "user" {
		t.Fatalf("ApplyMethod/source not preserved for max_connections: %+v", mc)
	}
}

// findParam returns the parameter with the given name, or nil.
func findParam(params []rdsdriver.Parameter, name string) *rdsdriver.Parameter {
	for i := range params {
		if params[i].Name == name {
			return &params[i]
		}
	}

	return nil
}

func TestDBParameterGroupCopy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: "src", Family: "postgres15"}); err != nil {
		t.Fatalf("create src: %v", err)
	}

	if _, err := m.ModifyDBParameterGroup(ctx, "src", []rdsdriver.Parameter{{Name: "work_mem", Value: "64MB"}}); err != nil {
		t.Fatalf("modify src: %v", err)
	}

	cp, err := m.CopyDBParameterGroup(ctx, "src", "dst", "copied")
	if err != nil {
		t.Fatalf("CopyDBParameterGroup: %v", err)
	}

	if cp.Family != "postgres15" || cp.Description != "copied" {
		t.Errorf("copy metadata wrong: %+v", cp)
	}

	// The copy carries the source's user modification (over the engine defaults).
	if params, _ := m.DescribeDBParameters(ctx, "dst"); findParam(params, "work_mem") == nil ||
		findParam(params, "work_mem").Value != "64MB" || findParam(params, "work_mem").Source != "user" {
		t.Fatalf("copy did not carry the work_mem modification: %+v", params)
	}

	// Copying onto an existing target is rejected.
	if _, err := m.CopyDBParameterGroup(ctx, "src", "dst", ""); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("copy onto existing: want AlreadyExists, got %v", err)
	}

	// Copying a missing source is NotFound.
	if _, err := m.CopyDBParameterGroup(ctx, "ghost", "dst2", ""); !cerrors.IsNotFound(err) {
		t.Fatalf("copy missing source: want NotFound, got %v", err)
	}
}

func TestParameterGroupDeleteGuards(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: "pg", Family: "mysql8.0"}); err != nil {
		t.Fatalf("CreateDBParameterGroup: %v", err)
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db", Engine: "mysql", DBParameterGroupName: "pg"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// In use by an instance → refused.
	if err := m.DeleteDBParameterGroup(ctx, "pg"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete in-use group: want FailedPrecondition, got %v", err)
	}

	// A default group is protected.
	if err := m.DeleteDBParameterGroup(ctx, "default.mysql8.0"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete default group: want FailedPrecondition, got %v", err)
	}

	// Once the instance is gone, the group deletes cleanly.
	if err := m.DeleteInstance(ctx, "db"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if err := m.DeleteDBParameterGroup(ctx, "pg"); err != nil {
		t.Fatalf("delete after detach: %v", err)
	}
}

func TestModifyReleasesParameterGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	for _, n := range []string{"pg1", "pg2"} {
		if _, err := m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: n, Family: "mysql8.0"}); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db", Engine: "mysql", DBParameterGroupName: "pg1"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := m.DeleteDBParameterGroup(ctx, "pg1"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete attached pg1: want FailedPrecondition, got %v", err)
	}

	// Re-point the instance to pg2; pg1 is now released.
	if _, err := m.ModifyInstance(ctx, "db", rdsdriver.ModifyInstanceInput{DBParameterGroupName: "pg2"}); err != nil {
		t.Fatalf("ModifyInstance: %v", err)
	}

	if err := m.DeleteDBParameterGroup(ctx, "pg1"); err != nil {
		t.Fatalf("delete released pg1: %v", err)
	}

	if err := m.DeleteDBParameterGroup(ctx, "pg2"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete now-attached pg2: want FailedPrecondition, got %v", err)
	}
}

func TestModifyReleasesClusterParameterGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	for _, n := range []string{"cpg1", "cpg2"} {
		if _, err := m.CreateDBClusterParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: n, Family: "aurora-mysql8.0"}); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "cl", Engine: "aurora-mysql", DBClusterParameterGroupName: "cpg1"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if err := m.DeleteDBClusterParameterGroup(ctx, "cpg1"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete attached cpg1: want FailedPrecondition, got %v", err)
	}

	if _, err := m.ModifyCluster(ctx, "cl", rdsdriver.ModifyInstanceInput{DBClusterParameterGroupName: "cpg2"}); err != nil {
		t.Fatalf("ModifyCluster: %v", err)
	}

	if err := m.DeleteDBClusterParameterGroup(ctx, "cpg1"); err != nil {
		t.Fatalf("delete released cpg1: %v", err)
	}
}

func TestCreateParameterGroupRejectsReservedName(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: "default.mysql8.0", Family: "mysql8.0"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("reserved DB param group name: want InvalidArgument, got %v", err)
	}

	if _, err := m.CreateDBClusterParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: "default.aurora-mysql8.0", Family: "aurora-mysql8.0"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("reserved cluster param group name: want InvalidArgument, got %v", err)
	}
}

func TestClusterParameterGroupDeleteGuard(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateDBClusterParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: "cpg", Family: "aurora-mysql8.0"}); err != nil {
		t.Fatalf("CreateDBClusterParameterGroup: %v", err)
	}

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "cl", Engine: "aurora-mysql", DBClusterParameterGroupName: "cpg"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if err := m.DeleteDBClusterParameterGroup(ctx, "cpg"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete in-use cluster group: want FailedPrecondition, got %v", err)
	}
}

func TestDBClusterParameterGroupLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateDBClusterParameterGroup(ctx, rdsdriver.ParameterGroupConfig{
		Name:   "cpg-1",
		Family: "aurora-mysql8.0",
	}); err != nil {
		t.Fatalf("CreateDBClusterParameterGroup: %v", err)
	}

	if _, err := m.ModifyDBClusterParameterGroup(ctx, "cpg-1", []rdsdriver.Parameter{
		{Name: "character_set_server", Value: "utf8mb4"},
	}); err != nil {
		t.Fatalf("ModifyDBClusterParameterGroup: %v", err)
	}

	// Aurora-mysql defaults are returned with the modification overlaid.
	if params, _ := m.DescribeDBClusterParameters(ctx, "cpg-1"); findParam(params, "character_set_server") == nil ||
		findParam(params, "character_set_server").Value != "utf8mb4" ||
		findParam(params, "character_set_server").Source != "user" {
		t.Fatalf("cluster params missing the modification: got %d", len(params))
	}

	groups, err := m.DescribeDBClusterParameterGroups(ctx, nil)
	if err != nil || len(groups) != 1 {
		t.Fatalf("DescribeDBClusterParameterGroups: got %d groups, err %v", len(groups), err)
	}

	if err := m.DeleteDBClusterParameterGroup(ctx, "cpg-1"); err != nil {
		t.Fatalf("DeleteDBClusterParameterGroup: %v", err)
	}

	if _, err := m.DescribeDBClusterParameters(ctx, "cpg-1"); !cerrors.IsNotFound(err) {
		t.Fatalf("describe deleted cluster group: want NotFound, got %v", err)
	}
}
