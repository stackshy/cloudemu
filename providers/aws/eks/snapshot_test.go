package eks

import (
	"context"
	"testing"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// TestSnapshotRoundTripEKS proves a snapshot/restore round-trip preserves the
// clusters, nodegroups and access entries under their original keys.
func TestSnapshotRoundTripEKS(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.30"}); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := src.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"}); err != nil {
		t.Fatalf("create nodegroup: %v", err)
	}

	if _, err := src.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name: "c2", AccessConfig: eksdriver.AccessConfigRequest{AuthenticationMode: "API"},
	}); err != nil {
		t.Fatalf("create api cluster: %v", err)
	}

	if _, err := src.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{ClusterName: "c2", PrincipalArn: testRoleArn}); err != nil {
		t.Fatalf("create access entry: %v", err)
	}

	if _, err := src.AssociateAccessPolicy(ctx, "c2", testRoleArn, testAdminPolicy,
		eksdriver.AccessScope{Type: eksdriver.AccessScopeCluster}); err != nil {
		t.Fatalf("associate: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	names, err := dst.ListClusters(ctx)
	if err != nil || len(names) != 2 {
		t.Fatalf("restored clusters = %+v, err %v", names, err)
	}

	if _, err := dst.DescribeCluster(ctx, "c1"); err != nil {
		t.Fatalf("describe restored cluster: %v", err)
	}

	policies, err := dst.ListAssociatedAccessPolicies(ctx, "c2", testRoleArn)
	if err != nil || len(policies) != 1 || policies[0].PolicyArn != testAdminPolicy {
		t.Fatalf("restored access entry policies = %+v, err %v", policies, err)
	}
}
