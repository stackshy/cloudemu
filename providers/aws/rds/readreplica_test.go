package rds

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestReadReplicaLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "primary", Engine: "mysql", EngineVersion: "8.0", AllocatedStorage: 50,
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	replica, err := m.CreateDBInstanceReadReplica(ctx, rdsdriver.ReadReplicaConfig{
		ID: "replica-1", SourceInstanceID: "primary",
	})
	if err != nil {
		t.Fatalf("CreateDBInstanceReadReplica: %v", err)
	}

	if replica.ReadReplicaSource != "primary" {
		t.Errorf("replica source = %q, want primary", replica.ReadReplicaSource)
	}

	// The replica inherits the source's engine and storage.
	if replica.Engine != "mysql" || replica.AllocatedStorage != 50 {
		t.Errorf("replica did not inherit source spec: %+v", replica)
	}

	// The source tracks the replica.
	src, _ := m.DescribeInstances(ctx, []string{"primary"})
	if len(src) != 1 || len(src[0].ReadReplicaTargets) != 1 || src[0].ReadReplicaTargets[0] != "replica-1" {
		t.Fatalf("source targets wrong: %+v", src)
	}

	// Promote detaches the replica.
	promoted, err := m.PromoteReadReplica(ctx, "replica-1")
	if err != nil {
		t.Fatalf("PromoteReadReplica: %v", err)
	}

	if promoted.ReadReplicaSource != "" {
		t.Errorf("promoted replica still has source %q", promoted.ReadReplicaSource)
	}

	src, _ = m.DescribeInstances(ctx, []string{"primary"})
	if len(src[0].ReadReplicaTargets) != 0 {
		t.Errorf("source still lists replica after promote: %+v", src[0].ReadReplicaTargets)
	}
}

func TestReadReplicaErrors(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateDBInstanceReadReplica(ctx, rdsdriver.ReadReplicaConfig{
		ID: "r", SourceInstanceID: "ghost",
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("replica from missing source: want NotFound, got %v", err)
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "standalone", Engine: "mysql"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.PromoteReadReplica(ctx, "standalone"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("promote non-replica: want FailedPrecondition, got %v", err)
	}

	if _, err := m.PromoteReadReplica(ctx, "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("promote missing: want NotFound, got %v", err)
	}
}
