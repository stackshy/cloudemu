package rds

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestOptionGroupLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	og, err := m.CreateOptionGroup(ctx, rdsdriver.OptionGroupConfig{
		Name:               "og-1",
		EngineName:         "mysql",
		MajorEngineVersion: "8.0",
		Description:        "app options",
	})
	if err != nil {
		t.Fatalf("CreateOptionGroup: %v", err)
	}

	if og.ARN == "" {
		t.Error("expected ARN")
	}

	if _, err := m.CreateOptionGroup(ctx, rdsdriver.OptionGroupConfig{Name: "og-1", EngineName: "mysql"}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate: want AlreadyExists, got %v", err)
	}

	if _, err := m.CreateOptionGroup(ctx, rdsdriver.OptionGroupConfig{Name: "og-x"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing engine: want InvalidArgument, got %v", err)
	}

	// Include two options, then remove one.
	got, err := m.ModifyOptionGroup(ctx, "og-1", []rdsdriver.Option{
		{Name: "MARIADB_AUDIT_PLUGIN"},
		{Name: "MEMCACHED", Port: 11211},
	}, nil)
	if err != nil {
		t.Fatalf("ModifyOptionGroup include: %v", err)
	}

	if len(got.Options) != 2 {
		t.Fatalf("got %d options, want 2", len(got.Options))
	}

	got, err = m.ModifyOptionGroup(ctx, "og-1", nil, []string{"MEMCACHED"})
	if err != nil {
		t.Fatalf("ModifyOptionGroup remove: %v", err)
	}

	if len(got.Options) != 1 || got.Options[0].Name != "MARIADB_AUDIT_PLUGIN" {
		t.Fatalf("after remove: %+v", got.Options)
	}

	// Filter describe by engine.
	if groups, _ := m.DescribeOptionGroups(ctx, nil, "postgres"); len(groups) != 0 {
		t.Fatalf("engine filter: got %d, want 0", len(groups))
	}

	if groups, _ := m.DescribeOptionGroups(ctx, nil, "mysql"); len(groups) != 1 {
		t.Fatalf("engine filter: got %d, want 1", len(groups))
	}

	if err := m.DeleteOptionGroup(ctx, "og-1"); err != nil {
		t.Fatalf("DeleteOptionGroup: %v", err)
	}

	if _, err := m.DescribeOptionGroups(ctx, []string{"og-1"}, ""); !cerrors.IsNotFound(err) {
		t.Fatalf("describe deleted: want NotFound, got %v", err)
	}
}

func TestDeleteOptionGroupGuards(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateOptionGroup(ctx, rdsdriver.OptionGroupConfig{Name: "og", EngineName: "mysql"}); err != nil {
		t.Fatalf("CreateOptionGroup: %v", err)
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db", Engine: "mysql", OptionGroupName: "og"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := m.DeleteOptionGroup(ctx, "og"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete in-use option group: want FailedPrecondition, got %v", err)
	}

	if err := m.DeleteOptionGroup(ctx, "default:mysql-8-0"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete default option group: want FailedPrecondition, got %v", err)
	}
}

func TestModifyReleasesOptionGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	for _, n := range []string{"og1", "og2"} {
		if _, err := m.CreateOptionGroup(ctx, rdsdriver.OptionGroupConfig{Name: n, EngineName: "mysql"}); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db", Engine: "mysql", OptionGroupName: "og1"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := m.DeleteOptionGroup(ctx, "og1"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete attached og1: want FailedPrecondition, got %v", err)
	}

	if _, err := m.ModifyInstance(ctx, "db", rdsdriver.ModifyInstanceInput{OptionGroupName: "og2"}); err != nil {
		t.Fatalf("ModifyInstance: %v", err)
	}

	if err := m.DeleteOptionGroup(ctx, "og1"); err != nil {
		t.Fatalf("delete released og1: %v", err)
	}
}

func TestCreateOptionGroupRejectsReservedName(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateOptionGroup(ctx, rdsdriver.OptionGroupConfig{Name: "default:mysql-8-0", EngineName: "mysql"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("reserved option group name: want InvalidArgument, got %v", err)
	}
}

func TestOptionGroupCopyAndOptions(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateOptionGroup(ctx, rdsdriver.OptionGroupConfig{Name: "src", EngineName: "oracle-ee", MajorEngineVersion: "19"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := m.ModifyOptionGroup(ctx, "src", []rdsdriver.Option{{Name: "APEX"}}, nil); err != nil {
		t.Fatalf("modify: %v", err)
	}

	cp, err := m.CopyOptionGroup(ctx, "src", "dst", "")
	if err != nil {
		t.Fatalf("CopyOptionGroup: %v", err)
	}

	if cp.EngineName != "oracle-ee" || len(cp.Options) != 1 {
		t.Fatalf("copy wrong: %+v", cp)
	}

	// DescribeOptionGroupOptions returns the representative catalog for the engine.
	opts, err := m.DescribeOptionGroupOptions(ctx, "oracle-ee", "19")
	if err != nil {
		t.Fatalf("DescribeOptionGroupOptions: %v", err)
	}

	if len(opts) == 0 {
		t.Fatal("expected a non-empty option catalog for oracle-ee")
	}

	if _, err := m.DescribeOptionGroupOptions(ctx, "", ""); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing engine: want InvalidArgument, got %v", err)
	}
}
