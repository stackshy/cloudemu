package managedcassandra

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mcdriver "github.com/stackshy/cloudemu/v2/services/managedcassandra/driver"
)

func newTestMock() *Mock {
	opts := config.NewOptions(config.WithRegion("eastus"), config.WithAccountID("sub-123"))

	return New(opts)
}

func mustCluster(t *testing.T, m *Mock, rg, name string) {
	t.Helper()

	if _, err := m.CreateOrUpdateCluster(context.Background(), mcdriver.CreateClusterConfig{
		Name: name, ResourceGroup: rg, Location: "eastus", DelegatedManagementSubnetID: "/subnets/sn",
	}); err != nil {
		t.Fatalf("CreateOrUpdateCluster %s: %v", name, err)
	}
}

func TestClusterLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	c, err := m.CreateOrUpdateCluster(ctx, mcdriver.CreateClusterConfig{
		Name: "cass", ResourceGroup: "rg1", Location: "eastus", RepairEnabled: true,
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateCluster: %v", err)
	}

	if c.ProvisioningState != mcdriver.ProvisioningSucceeded || c.CassandraVersion != defaultCassandraVersion {
		t.Fatalf("cluster defaults wrong: %+v", c)
	}

	// PATCH.
	repair := false
	upd, err := m.UpdateCluster(ctx, "rg1", "cass", mcdriver.ClusterPatch{
		RepairEnabled: &repair, Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("UpdateCluster: %v", err)
	}

	if upd.RepairEnabled || upd.Tags["env"] != "prod" {
		t.Fatalf("patch not applied: %+v", upd)
	}

	// List by RG + subscription.
	byRG, _ := m.ListClustersByResourceGroup(ctx, "rg1")
	if len(byRG) != 1 {
		t.Fatalf("list by rg: got %d, want 1", len(byRG))
	}

	bySub, _ := m.ListClustersBySubscription(ctx)
	if len(bySub) != 1 {
		t.Fatalf("list by sub: got %d, want 1", len(bySub))
	}

	if err := m.DeleteCluster(ctx, "rg1", "cass"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, err := m.GetCluster(ctx, "rg1", "cass"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete: got %v, want NotFound", err)
	}
}

func TestDataCenterParentLinkage(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "cass")

	// A datacenter under a missing cluster is rejected as a missing parent.
	if _, err := m.CreateOrUpdateDataCenter(ctx, mcdriver.CreateDataCenterConfig{
		ClusterName: "ghost", ResourceGroup: "rg1", Name: "dc1",
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("dc under missing cluster: got %v, want NotFound", err)
	}

	dc, err := m.CreateOrUpdateDataCenter(ctx, mcdriver.CreateDataCenterConfig{
		ClusterName: "cass", ResourceGroup: "rg1", Name: "dc1", DataCenterLocation: "eastus", NodeCount: 3,
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateDataCenter: %v", err)
	}

	if dc.NodeCount != 3 || len(dc.SeedNodes) != 3 || dc.SKU != defaultDataCenterSKU {
		t.Fatalf("datacenter defaults wrong: %+v", dc)
	}

	// The parent cluster's seed nodes now include the datacenter's.
	c, _ := m.GetCluster(ctx, "rg1", "cass")
	if len(c.SeedNodes) != 3 {
		t.Fatalf("cluster seed nodes not derived: %+v", c.SeedNodes)
	}

	// Deleting the cluster cascade-deletes the datacenter.
	if err := m.DeleteCluster(ctx, "rg1", "cass"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, err := m.GetDataCenter(ctx, "rg1", "cass", "dc1"); !cerrors.IsNotFound(err) {
		t.Fatalf("datacenter survived cluster delete: %v", err)
	}
}

func TestDataCenterUpdateAndList(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "cass")

	if _, err := m.CreateOrUpdateDataCenter(ctx, mcdriver.CreateDataCenterConfig{
		ClusterName: "cass", ResourceGroup: "rg1", Name: "dc1", NodeCount: 3,
	}); err != nil {
		t.Fatalf("create dc: %v", err)
	}

	nodes := 6
	upd, err := m.UpdateDataCenter(ctx, "rg1", "cass", "dc1", mcdriver.DataCenterPatch{NodeCount: &nodes})
	if err != nil {
		t.Fatalf("UpdateDataCenter: %v", err)
	}

	if upd.NodeCount != 6 || len(upd.SeedNodes) != 6 {
		t.Fatalf("dc scale-up not applied: %+v", upd)
	}

	list, _ := m.ListDataCenters(ctx, "rg1", "cass")
	if len(list) != 1 {
		t.Fatalf("list datacenters: got %d, want 1", len(list))
	}
}

func TestDeallocateStartAndStatus(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "cass")

	if _, err := m.CreateOrUpdateDataCenter(ctx, mcdriver.CreateDataCenterConfig{
		ClusterName: "cass", ResourceGroup: "rg1", Name: "dc1", NodeCount: 3,
	}); err != nil {
		t.Fatalf("create dc: %v", err)
	}

	if _, err := m.DeallocateCluster(ctx, "rg1", "cass"); err != nil {
		t.Fatalf("DeallocateCluster: %v", err)
	}

	dc, _ := m.GetDataCenter(ctx, "rg1", "cass", "dc1")
	if !dc.Deallocated {
		t.Fatal("datacenter not deallocated with cluster")
	}

	status, err := m.ClusterStatus(ctx, "rg1", "cass")
	if err != nil {
		t.Fatalf("ClusterStatus: %v", err)
	}

	if len(status.Nodes) != 3 || status.Nodes[0].State != "STOPPED" {
		t.Fatalf("status after deallocate wrong: %+v", status.Nodes)
	}

	if _, err := m.StartCluster(ctx, "rg1", "cass"); err != nil {
		t.Fatalf("StartCluster: %v", err)
	}

	status, _ = m.ClusterStatus(ctx, "rg1", "cass")
	if status.Nodes[0].State != "NORMAL" {
		t.Fatalf("status after start wrong: %+v", status.Nodes)
	}
}

func TestInvokeCommand(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "cass")

	out, err := m.InvokeCommand(ctx, "rg1", "cass", "nodetool status", "10.0.0.4")
	if err != nil {
		t.Fatalf("InvokeCommand: %v", err)
	}

	if out == "" {
		t.Fatal("expected command output")
	}

	if _, err := m.InvokeCommand(ctx, "rg1", "ghost", "x", ""); !cerrors.IsNotFound(err) {
		t.Fatalf("invoke on missing cluster: got %v, want NotFound", err)
	}
}

func TestDataCenterInheritsClusterDeallocatedState(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "cass")

	// Deallocate the cluster, then add a new datacenter.
	if _, err := m.DeallocateCluster(ctx, "rg1", "cass"); err != nil {
		t.Fatalf("DeallocateCluster: %v", err)
	}

	dc, err := m.CreateOrUpdateDataCenter(ctx, mcdriver.CreateDataCenterConfig{
		ClusterName: "cass", ResourceGroup: "rg1", Name: "dc1", NodeCount: 3,
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateDataCenter: %v", err)
	}

	// The new DC must inherit the stopped cluster's state, not default to false.
	if !dc.Deallocated {
		t.Fatal("new datacenter did not inherit the cluster's deallocated state")
	}

	// Status is therefore internally consistent (all STOPPED).
	status, _ := m.ClusterStatus(ctx, "rg1", "cass")
	for _, n := range status.Nodes {
		if n.State != "STOPPED" {
			t.Fatalf("mixed status after add-to-deallocated: %+v", status.Nodes)
		}
	}

	// Starting the cluster brings the DC back with it.
	if _, err := m.StartCluster(ctx, "rg1", "cass"); err != nil {
		t.Fatalf("StartCluster: %v", err)
	}

	got, _ := m.GetDataCenter(ctx, "rg1", "cass", "dc1")
	if got.Deallocated {
		t.Fatal("datacenter still deallocated after cluster start")
	}
}

func TestDataCenterNodeCountBounded(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "cass")

	if _, err := m.CreateOrUpdateDataCenter(ctx, mcdriver.CreateDataCenterConfig{
		ClusterName: "cass", ResourceGroup: "rg1", Name: "dc1", NodeCount: maxNodesPerDataCenter + 1,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("oversized node count: got %v, want InvalidArgument", err)
	}
}

func TestClusterResultDoesNotAliasStore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateOrUpdateCluster(ctx, mcdriver.CreateClusterConfig{
		Name: "cass", ResourceGroup: "rg1", Tags: map[string]string{"k": "v"},
		ExternalSeedNodes: []string{"1.2.3.4"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, _ := m.GetCluster(ctx, "rg1", "cass")
	got.Tags["k"] = "MUTATED"
	got.ExternalSeedNodes[0] = "9.9.9.9"

	again, _ := m.GetCluster(ctx, "rg1", "cass")
	if again.Tags["k"] == "MUTATED" || again.ExternalSeedNodes[0] == "9.9.9.9" {
		t.Fatal("returned cluster aliases the store (clone-on-read broken)")
	}
}
