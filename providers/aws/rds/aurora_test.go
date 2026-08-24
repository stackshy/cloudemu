package rds

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestClusterEndpointLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "cl", Engine: "aurora-mysql"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	ep, err := m.CreateDBClusterEndpoint(ctx, rdsdriver.ClusterEndpointConfig{
		EndpointID: "reader-ep", ClusterID: "cl", EndpointType: "READER",
	})
	if err != nil {
		t.Fatalf("CreateDBClusterEndpoint: %v", err)
	}

	if ep.EndpointType != "CUSTOM" || ep.CustomEndpointType != "READER" || ep.Endpoint == "" {
		t.Fatalf("endpoint fields wrong: %+v", ep)
	}

	if _, err := m.CreateDBClusterEndpoint(ctx, rdsdriver.ClusterEndpointConfig{EndpointID: "x", ClusterID: "ghost"}); !cerrors.IsNotFound(err) {
		t.Fatalf("endpoint on missing cluster: want NotFound, got %v", err)
	}

	// Describe returns the built-in WRITER + READER endpoints plus the custom one.
	byCluster, _ := m.DescribeDBClusterEndpoints(ctx, "cl", "")

	types := map[string]int{}
	for i := range byCluster {
		types[byCluster[i].EndpointType]++
	}

	if types["WRITER"] != 1 || types["READER"] != 1 || types["CUSTOM"] != 1 {
		t.Fatalf("describe by cluster types = %v, want one each of WRITER/READER/CUSTOM", types)
	}

	if _, err := m.ModifyDBClusterEndpoint(ctx, "reader-ep", rdsdriver.ModifyClusterEndpointInput{EndpointType: "ANY"}); err != nil {
		t.Fatalf("ModifyDBClusterEndpoint: %v", err)
	}

	got, _ := m.DescribeDBClusterEndpoints(ctx, "", "reader-ep")
	if got[0].CustomEndpointType != "ANY" {
		t.Fatalf("modify not applied: %+v", got[0])
	}

	if _, err := m.DeleteDBClusterEndpoint(ctx, "reader-ep"); err != nil {
		t.Fatalf("DeleteDBClusterEndpoint: %v", err)
	}

	if _, err := m.DescribeDBClusterEndpoints(ctx, "", "reader-ep"); !cerrors.IsNotFound(err) {
		t.Fatalf("describe deleted: want NotFound, got %v", err)
	}
}

func TestFailoverDBCluster(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "cl", Engine: "aurora-mysql"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	for _, id := range []string{"writer", "reader"} {
		if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: id, Engine: "aurora-mysql", ClusterID: "cl"}); err != nil {
			t.Fatalf("CreateInstance %s: %v", id, err)
		}
	}

	// Fail over to the reader; it must become the writer (Members[0]).
	got, err := m.FailoverDBCluster(ctx, "cl", "reader")
	if err != nil {
		t.Fatalf("FailoverDBCluster: %v", err)
	}

	if len(got.Members) != 2 || got.Members[0] != "reader" {
		t.Fatalf("failover did not promote target: %+v", got.Members)
	}

	if _, err := m.FailoverDBCluster(ctx, "cl", "ghost"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("failover to non-member: want InvalidArgument, got %v", err)
	}

	if _, err := m.FailoverDBCluster(ctx, "ghost", ""); !cerrors.IsNotFound(err) {
		t.Fatalf("failover missing cluster: want NotFound, got %v", err)
	}
}

func TestGlobalClusterLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	src, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "primary", Engine: "aurora-postgresql", EngineVersion: "15.4"})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	gc, err := m.CreateGlobalCluster(ctx, rdsdriver.GlobalClusterConfig{
		ID: "global-1", SourceDBClusterID: "primary",
	})
	if err != nil {
		t.Fatalf("CreateGlobalCluster: %v", err)
	}

	if gc.Engine != "aurora-postgresql" || len(gc.Members) != 1 || !gc.Members[0].IsWriter {
		t.Fatalf("global cluster did not adopt source: %+v", gc)
	}

	if gc.Members[0].DBClusterARN != src.ARN {
		t.Fatalf("member ARN = %q, want %q", gc.Members[0].DBClusterARN, src.ARN)
	}

	if _, err := m.CreateGlobalCluster(ctx, rdsdriver.GlobalClusterConfig{ID: "global-1"}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate: want AlreadyExists, got %v", err)
	}

	// Deleting a global cluster that still has members is refused.
	if _, err := m.DeleteGlobalCluster(ctx, "global-1"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete global cluster with members: want FailedPrecondition, got %v", err)
	}

	// Rename + bump engine version.
	renamed, err := m.ModifyGlobalCluster(ctx, "global-1", "global-2", "15.5")
	if err != nil {
		t.Fatalf("ModifyGlobalCluster: %v", err)
	}

	if renamed.ID != "global-2" || renamed.EngineVersion != "15.5" {
		t.Fatalf("modify not applied: %+v", renamed)
	}

	// Remove the member cluster.
	removed, err := m.RemoveFromGlobalCluster(ctx, "global-2", src.ARN)
	if err != nil {
		t.Fatalf("RemoveFromGlobalCluster: %v", err)
	}

	if len(removed.Members) != 0 {
		t.Fatalf("member not removed: %+v", removed.Members)
	}

	if _, err := m.DeleteGlobalCluster(ctx, "global-2"); err != nil {
		t.Fatalf("DeleteGlobalCluster: %v", err)
	}

	if _, err := m.DescribeGlobalClusters(ctx, []string{"global-2"}); !cerrors.IsNotFound(err) {
		t.Fatalf("describe deleted: want NotFound, got %v", err)
	}
}
