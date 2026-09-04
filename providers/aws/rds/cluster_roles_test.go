package rds

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestAddRemoveRoleToDBCluster(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "cl", Engine: "aurora-mysql"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	const s3Role = "arn:aws:iam::123456789012:role/s3-import"

	if err := m.AddRoleToDBCluster(ctx, "cl", s3Role, "s3Import"); err != nil {
		t.Fatalf("AddRoleToDBCluster: %v", err)
	}

	got, err := m.DescribeClusters(ctx, []string{"cl"})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(got[0].AssociatedRoles) != 1 {
		t.Fatalf("AssociatedRoles = %+v, want one", got[0].AssociatedRoles)
	}

	role := got[0].AssociatedRoles[0]
	if role.RoleARN != s3Role || role.FeatureName != "s3Import" || role.Status != clusterRoleStatusActive {
		t.Fatalf("role = %+v, want {%s s3Import ACTIVE}", role, s3Role)
	}

	// A second role coexists (keyed by ARN).
	const lambdaRole = "arn:aws:iam::123456789012:role/lambda"
	if err := m.AddRoleToDBCluster(ctx, "cl", lambdaRole, "Lambda"); err != nil {
		t.Fatalf("AddRoleToDBCluster second: %v", err)
	}

	if err := m.RemoveRoleFromDBCluster(ctx, "cl", s3Role, ""); err != nil {
		t.Fatalf("RemoveRoleFromDBCluster: %v", err)
	}

	got, _ = m.DescribeClusters(ctx, []string{"cl"})
	if len(got[0].AssociatedRoles) != 1 || got[0].AssociatedRoles[0].RoleARN != lambdaRole {
		t.Fatalf("after remove AssociatedRoles = %+v, want only lambda", got[0].AssociatedRoles)
	}
}

func TestAddRoleToDBClusterGuards(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "cl", Engine: "aurora-mysql"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	const role = "arn:aws:iam::123456789012:role/r"

	// Missing RoleArn.
	if err := m.AddRoleToDBCluster(ctx, "cl", "", "s3Import"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("empty RoleArn: want InvalidArgument, got %v", err)
	}

	// Unknown cluster.
	if err := m.AddRoleToDBCluster(ctx, "ghost", role, ""); !cerrors.IsNotFound(err) {
		t.Fatalf("add on missing cluster: want NotFound, got %v", err)
	}

	if err := m.AddRoleToDBCluster(ctx, "cl", role, ""); err != nil {
		t.Fatalf("AddRoleToDBCluster: %v", err)
	}

	// Duplicate association.
	if err := m.AddRoleToDBCluster(ctx, "cl", role, ""); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate role: want AlreadyExists, got %v", err)
	}

	// Remove a role that is not associated.
	if err := m.RemoveRoleFromDBCluster(ctx, "cl", "arn:aws:iam::123456789012:role/other", ""); !cerrors.IsNotFound(err) {
		t.Fatalf("remove unassociated role: want NotFound, got %v", err)
	}

	// Remove on unknown cluster.
	if err := m.RemoveRoleFromDBCluster(ctx, "ghost", role, ""); !cerrors.IsNotFound(err) {
		t.Fatalf("remove on missing cluster: want NotFound, got %v", err)
	}
}

// TestDescribeClustersRolesCloned proves DescribeClusters hands back an
// independent copy of the AssociatedRoles slice: mutating a returned value must
// not leak into a subsequent Describe.
func TestDescribeClustersRolesCloned(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "cl", Engine: "aurora-mysql"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if err := m.AddRoleToDBCluster(ctx, "cl", "arn:aws:iam::123456789012:role/r", "s3Import"); err != nil {
		t.Fatalf("AddRoleToDBCluster: %v", err)
	}

	first, _ := m.DescribeClusters(ctx, []string{"cl"})
	first[0].AssociatedRoles[0].Status = "TAMPERED"

	second, _ := m.DescribeClusters(ctx, []string{"cl"})
	if second[0].AssociatedRoles[0].Status != clusterRoleStatusActive {
		t.Fatalf("store leaked mutation: status = %q", second[0].AssociatedRoles[0].Status)
	}
}
