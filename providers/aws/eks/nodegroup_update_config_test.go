package eks

import (
	"context"
	"testing"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// mustCluster creates an ACTIVE cluster the nodegroup tests can attach to.
func mustCluster(t *testing.T, m *Mock, name string) {
	t.Helper()

	if _, err := m.CreateCluster(context.Background(), eksdriver.ClusterConfig{
		Name:    name,
		RoleArn: "arn:aws:iam::123456789012:role/eks-cluster",
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
}

// TestCreateNodegroupUpdateConfigDefault verifies a nodegroup created without an
// updateConfig reports maxUnavailable=1 — the real EKS default — so Terraform's
// update_config block does not drift after create.
func TestCreateNodegroupUpdateConfigDefault(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "c1")

	created, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"})
	requireNoError(t, err)
	assertEqual(t, 1, created.UpdateConfig.MaxUnavailable)
	assertEqual(t, 0, created.UpdateConfig.MaxUnavailablePercentage)

	got, err := m.DescribeNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)
	assertEqual(t, 1, got.UpdateConfig.MaxUnavailable)
}

// TestCreateNodegroupUpdateConfigExplicit verifies an explicit updateConfig
// round-trips, and that the percentage variant leaves maxUnavailable unset.
func TestCreateNodegroupUpdateConfigExplicit(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "c1")

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName:   "c1",
		NodegroupName: "count",
		UpdateConfig:  eksdriver.NodegroupUpdateConfig{MaxUnavailable: 3},
	})
	requireNoError(t, err)

	got, err := m.DescribeNodegroup(ctx, "c1", "count")
	requireNoError(t, err)
	assertEqual(t, 3, got.UpdateConfig.MaxUnavailable)
	assertEqual(t, 0, got.UpdateConfig.MaxUnavailablePercentage)

	_, err = m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName:   "c1",
		NodegroupName: "pct",
		UpdateConfig:  eksdriver.NodegroupUpdateConfig{MaxUnavailablePercentage: 25},
	})
	requireNoError(t, err)

	pct, err := m.DescribeNodegroup(ctx, "c1", "pct")
	requireNoError(t, err)
	assertEqual(t, 0, pct.UpdateConfig.MaxUnavailable)
	assertEqual(t, 25, pct.UpdateConfig.MaxUnavailablePercentage)
}

// TestCreateNodegroupUpdateConfigMutualExclusion verifies setting both knobs is
// rejected, matching real EKS (InvalidParameterException).
func TestCreateNodegroupUpdateConfigMutualExclusion(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "c1")

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName:   "c1",
		NodegroupName: "bad",
		UpdateConfig:  eksdriver.NodegroupUpdateConfig{MaxUnavailable: 1, MaxUnavailablePercentage: 50},
	})
	assertError(t, err, true)
}

// TestUpdateNodegroupConfigUpdateConfig verifies UpdateNodegroupConfig applies a
// new updateConfig and leaves it in place when the request omits it.
func TestUpdateNodegroupConfigUpdateConfig(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "c1")

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"})
	requireNoError(t, err)

	uc := eksdriver.NodegroupUpdateConfig{MaxUnavailable: 4}
	if _, err = m.UpdateNodegroupConfig(ctx, "c1", "ng1",
		eksdriver.NodegroupConfigUpdate{UpdateConfig: &uc}); err != nil {
		t.Fatalf("UpdateNodegroupConfig: %v", err)
	}

	got, err := m.DescribeNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)
	assertEqual(t, 4, got.UpdateConfig.MaxUnavailable)

	// A later config update that omits updateConfig must leave it untouched.
	if _, err = m.UpdateNodegroupConfig(ctx, "c1", "ng1",
		eksdriver.NodegroupConfigUpdate{AddOrUpdateLabels: map[string]string{"k": "v"}}); err != nil {
		t.Fatalf("UpdateNodegroupConfig (labels): %v", err)
	}

	got, err = m.DescribeNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)
	assertEqual(t, 4, got.UpdateConfig.MaxUnavailable)
}
