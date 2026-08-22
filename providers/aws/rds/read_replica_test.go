package rds

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestReadReplicaSharesEngineBackedSourceEndpoint proves a read replica of an
// engine-backed source points at the SOURCE's reachable host:port (so a client
// reading from the replica reaches the real database that holds the source's
// data) without provisioning a separate empty database.
func TestReadReplicaSharesEngineBackedSourceEndpoint(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	src, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "src", Engine: "postgres", MasterUsername: "admin", MasterUserPassword: "pw",
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if src.Endpoint != "127.0.0.1" || src.Port != 55432 {
		t.Fatalf("source not engine-backed: %s:%d", src.Endpoint, src.Port)
	}

	replica, err := m.CreateDBInstanceReadReplica(ctx, rdsdriver.ReadReplicaConfig{
		ID: "rep", SourceInstanceID: "src",
	})
	if err != nil {
		t.Fatalf("CreateDBInstanceReadReplica: %v", err)
	}

	if replica.Endpoint != src.Endpoint || replica.Port != src.Port {
		t.Fatalf("replica endpoint not shared with engine-backed source: got %s:%d, want %s:%d",
			replica.Endpoint, replica.Port, src.Endpoint, src.Port)
	}

	if replica.ReadReplicaSource != "src" {
		t.Fatalf("replica source linkage not set: %q", replica.ReadReplicaSource)
	}

	// A replica shares the source's data — it must not provision its own database.
	for _, p := range eng.provisioned {
		if p.InstanceID == "rep" {
			t.Fatalf("replica must not provision a separate database: %+v", p)
		}
	}
}

// TestReadReplicaSyntheticSourceKeepsSyntheticEndpoint proves that without a
// real engine the replica keeps its own synthetic endpoint rather than aliasing
// the source's.
func TestReadReplicaSyntheticSourceKeepsSyntheticEndpoint(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	src, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "src", Engine: "postgres"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	replica, err := m.CreateDBInstanceReadReplica(ctx, rdsdriver.ReadReplicaConfig{
		ID: "rep", SourceInstanceID: "src",
	})
	if err != nil {
		t.Fatalf("CreateDBInstanceReadReplica: %v", err)
	}

	if replica.Endpoint == "" || replica.Endpoint == src.Endpoint {
		t.Fatalf("synthetic replica endpoint should differ from the source, got %q", replica.Endpoint)
	}
}

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

func TestDeleteInstanceBlockedByReplica(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "primary", Engine: "mysql"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CreateDBInstanceReadReplica(ctx, rdsdriver.ReadReplicaConfig{ID: "rep", SourceInstanceID: "primary"}); err != nil {
		t.Fatalf("CreateDBInstanceReadReplica: %v", err)
	}

	// Deleting a source that still has replicas is refused.
	if err := m.DeleteInstance(ctx, "primary"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete source with replicas: want FailedPrecondition, got %v", err)
	}

	// Deleting the replica strips it from the source's target list.
	if err := m.DeleteInstance(ctx, "rep"); err != nil {
		t.Fatalf("DeleteInstance(replica): %v", err)
	}

	src, _ := m.DescribeInstances(ctx, []string{"primary"})
	if len(src[0].ReadReplicaTargets) != 0 {
		t.Fatalf("source still lists deleted replica: %v", src[0].ReadReplicaTargets)
	}

	// Now the source deletes cleanly.
	if err := m.DeleteInstance(ctx, "primary"); err != nil {
		t.Fatalf("DeleteInstance(primary) after replica gone: %v", err)
	}
}

// Describe must return independent copies of the instance slice fields, across
// both the list-all and named-lookup branches (LOW copy-on-read consistency).
func TestDescribeInstancesReturnsIndependentCopies(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "db", Engine: "mysql", VPCSecurityGroups: []string{"sg-1", "sg-2"},
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	all, _ := m.DescribeInstances(ctx, nil)
	all[0].VPCSecurityGroups[0] = "MUTATED"

	named, _ := m.DescribeInstances(ctx, []string{"db"})
	named[0].VPCSecurityGroups[0] = "MUTATED-TOO"

	again, _ := m.DescribeInstances(ctx, []string{"db"})
	if again[0].VPCSecurityGroups[0] != "sg-1" {
		t.Fatalf("returned VPCSecurityGroups aliased the store: %v", again[0].VPCSecurityGroups)
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
