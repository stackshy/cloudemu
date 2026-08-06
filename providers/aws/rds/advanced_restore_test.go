package rds

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestCopyDBSnapshot(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db", Engine: "postgres", AllocatedStorage: 40}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "snap", InstanceID: "db"}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	cp, err := m.CopyDBSnapshot(ctx, "snap", "snap-copy", nil)
	if err != nil {
		t.Fatalf("CopyDBSnapshot: %v", err)
	}

	if cp.Engine != "postgres" || cp.AllocatedStorage != 40 || cp.InstanceID != "db" {
		t.Fatalf("copy did not clone source: %+v", cp)
	}

	if _, err := m.CopyDBSnapshot(ctx, "snap", "snap-copy", nil); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("copy onto existing: want AlreadyExists, got %v", err)
	}

	if _, err := m.CopyDBSnapshot(ctx, "ghost", "x", nil); !cerrors.IsNotFound(err) {
		t.Fatalf("copy missing source: want NotFound, got %v", err)
	}
}

func TestCopyDBClusterSnapshot(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "cl", Engine: "aurora-mysql"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{ID: "csnap", ClusterID: "cl"}); err != nil {
		t.Fatalf("CreateClusterSnapshot: %v", err)
	}

	cp, err := m.CopyDBClusterSnapshot(ctx, "csnap", "csnap-copy", nil)
	if err != nil {
		t.Fatalf("CopyDBClusterSnapshot: %v", err)
	}

	if cp.Engine != "aurora-mysql" || cp.ClusterID != "cl" {
		t.Fatalf("copy did not clone source: %+v", cp)
	}
}

func TestRestoreInstanceToPointInTime(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "src", Engine: "mysql", EngineVersion: "8.0", AllocatedStorage: 30, InstanceClass: "db.r5.large",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	restored, err := m.RestoreDBInstanceToPointInTime(ctx, rdsdriver.RestoreInstanceToPointInTimeInput{
		SourceInstanceID: "src", TargetInstanceID: "restored", UseLatestRestorableTime: true,
	})
	if err != nil {
		t.Fatalf("RestoreDBInstanceToPointInTime: %v", err)
	}

	if restored.Engine != "mysql" || restored.AllocatedStorage != 30 || restored.InstanceClass != "db.r5.large" {
		t.Fatalf("restore did not clone source spec: %+v", restored)
	}

	if _, err := m.RestoreDBInstanceToPointInTime(ctx, rdsdriver.RestoreInstanceToPointInTimeInput{
		SourceInstanceID: "ghost", TargetInstanceID: "x",
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("restore missing source: want NotFound, got %v", err)
	}

	if _, err := m.RestoreDBInstanceToPointInTime(ctx, rdsdriver.RestoreInstanceToPointInTimeInput{
		SourceInstanceID: "src", TargetInstanceID: "restored",
	}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("restore onto existing target: want AlreadyExists, got %v", err)
	}
}

func TestRestoreClusterToPointInTime(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "src", Engine: "aurora-postgresql"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	restored, err := m.RestoreDBClusterToPointInTime(ctx, rdsdriver.RestoreClusterToPointInTimeInput{
		SourceClusterID: "src", TargetClusterID: "restored",
	})
	if err != nil {
		t.Fatalf("RestoreDBClusterToPointInTime: %v", err)
	}

	if restored.Engine != "aurora-postgresql" || len(restored.Members) != 0 {
		t.Fatalf("restore wrong: %+v", restored)
	}
}
