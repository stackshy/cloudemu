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

	if len(params) != 2 {
		t.Fatalf("got %d params, want 2", len(params))
	}

	if params[0].Source != "user" {
		t.Errorf("param source = %q, want user", params[0].Source)
	}

	// Reset a named parameter.
	if _, err := m.ResetDBParameterGroup(ctx, "pg-1", []string{"slow_query_log"}, false); err != nil {
		t.Fatalf("ResetDBParameterGroup: %v", err)
	}

	if params, _ := m.DescribeDBParameters(ctx, "pg-1"); len(params) != 1 {
		t.Fatalf("after reset one: got %d params, want 1", len(params))
	}

	// Reset all.
	if _, err := m.ResetDBParameterGroup(ctx, "pg-1", nil, true); err != nil {
		t.Fatalf("ResetDBParameterGroup all: %v", err)
	}

	if params, _ := m.DescribeDBParameters(ctx, "pg-1"); len(params) != 0 {
		t.Fatalf("after reset all: got %d params, want 0", len(params))
	}

	// Delete, then describe-missing is NotFound.
	if err := m.DeleteDBParameterGroup(ctx, "pg-1"); err != nil {
		t.Fatalf("DeleteDBParameterGroup: %v", err)
	}

	if _, err := m.DescribeDBParameterGroups(ctx, []string{"pg-1"}); !cerrors.IsNotFound(err) {
		t.Fatalf("describe deleted: want NotFound, got %v", err)
	}
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

	// The copy carries the source's parameters.
	if params, _ := m.DescribeDBParameters(ctx, "dst"); len(params) != 1 || params[0].Name != "work_mem" {
		t.Fatalf("copy did not carry params: %+v", params)
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

	if params, _ := m.DescribeDBClusterParameters(ctx, "cpg-1"); len(params) != 1 {
		t.Fatalf("got %d cluster params, want 1", len(params))
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
