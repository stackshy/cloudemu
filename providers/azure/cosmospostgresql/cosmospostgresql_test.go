package cosmospostgresql

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

const sub = "sub-123"

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"), config.WithAccountID(sub))

	return New(opts)
}

func mustCluster(t *testing.T, m *Mock, rg, name string, nodeCount int) string {
	t.Helper()

	if _, err := m.CreateOrUpdateCluster(context.Background(), cpgdriver.CreateClusterConfig{
		Name: name, ResourceGroup: rg, Location: "eastus", NodeCount: nodeCount,
	}); err != nil {
		t.Fatalf("CreateOrUpdateCluster %s: %v", name, err)
	}

	return name
}

func TestClusterLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	c, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "pg1", ResourceGroup: "rg1", Location: "eastus", NodeCount: 2,
		Tags: map[string]string{"env": "prod"}, EnableHa: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if c.ProvisioningState != cpgdriver.ProvisioningSucceeded || c.CitusVersion != defaultCitusVersion {
		t.Fatalf("defaults wrong: %+v", c)
	}

	// PATCH: scale nodes + change HA.
	ha := false
	up, err := m.UpdateCluster(ctx, "rg1", "pg1", cpgdriver.ClusterPatch{
		NodeCount: intPtr(4), EnableHa: &ha, Tags: map[string]string{"env": "dev"},
	})
	if err != nil || up.NodeCount != 4 || up.EnableHa {
		t.Fatalf("patch not applied: %+v err=%v", up, err)
	}

	if up.Tags["env"] != "dev" {
		t.Fatalf("tags not replaced: %+v", up.Tags)
	}

	// List by RG + subscription.
	byRG, _ := m.ListClustersByResourceGroup(ctx, "rg1")
	bySub, _ := m.ListClustersBySubscription(ctx)

	if len(byRG) != 1 || len(bySub) != 1 {
		t.Fatalf("list wrong: rg=%d sub=%d", len(byRG), len(bySub))
	}

	// Stop / start toggles state.
	if err := m.StopCluster(ctx, "rg1", "pg1"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if got, _ := m.GetCluster(ctx, "rg1", "pg1"); got.State != "Stopped" {
		t.Fatalf("state after stop: %q", got.State)
	}

	if err := m.StartCluster(ctx, "rg1", "pg1"); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Delete.
	if err := m.DeleteCluster(ctx, "rg1", "pg1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := m.GetCluster(ctx, "rg1", "pg1"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete: got %v, want NotFound", err)
	}
}

func TestNodeCountBounded(t *testing.T) {
	m := newTestMock()

	if _, err := m.CreateOrUpdateCluster(context.Background(), cpgdriver.CreateClusterConfig{
		Name: "big", ResourceGroup: "rg1", NodeCount: maxNodeCount + 1,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("oversized nodeCount: got %v, want InvalidArgument", err)
	}
}

func TestFirewallRulesAndRoles(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 2)

	// A firewall rule under a missing cluster is rejected.
	if _, err := m.CreateOrUpdateFirewallRule(ctx, cpgdriver.CreateFirewallRuleConfig{
		ResourceGroup: "rg1", ClusterName: "ghost", Name: "all", StartIPAddress: "0.0.0.0", EndIPAddress: "255.255.255.255",
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("fw under missing cluster: got %v, want InvalidArgument", err)
	}

	if _, err := m.CreateOrUpdateFirewallRule(ctx, cpgdriver.CreateFirewallRuleConfig{
		ResourceGroup: "rg1", ClusterName: "pg1", Name: "all", StartIPAddress: "0.0.0.0", EndIPAddress: "255.255.255.255",
	}); err != nil {
		t.Fatalf("CreateOrUpdateFirewallRule: %v", err)
	}

	rules, _ := m.ListFirewallRules(ctx, "rg1", "pg1")
	if len(rules) != 1 || rules[0].EndIPAddress != "255.255.255.255" {
		t.Fatalf("list fw wrong: %+v", rules)
	}

	if _, err := m.CreateRole(ctx, cpgdriver.CreateRoleConfig{ResourceGroup: "rg1", ClusterName: "pg1", Name: "app"}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := m.CreateRole(ctx, cpgdriver.CreateRoleConfig{ResourceGroup: "rg1", ClusterName: "pg1", Name: "app"}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate role: got %v, want AlreadyExists", err)
	}

	// Cascade delete removes children.
	if err := m.DeleteCluster(ctx, "rg1", "pg1"); err != nil {
		t.Fatalf("delete cluster: %v", err)
	}

	if r, _ := m.ListFirewallRules(ctx, "rg1", "pg1"); len(r) != 0 {
		t.Fatalf("firewall rules survived cluster delete: %+v", r)
	}
}

func TestDerivedServers(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 2)

	servers, err := m.ListServers(ctx, "rg1", "pg1")
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}

	// One coordinator + two workers.
	if len(servers) != 3 || servers[0].Role != cpgdriver.RoleCoordinator {
		t.Fatalf("derived nodes wrong: %+v", servers)
	}

	if _, err := m.GetServer(ctx, "rg1", "pg1", "pg1-c"); err != nil {
		t.Fatalf("GetServer coordinator: %v", err)
	}

	if _, err := m.GetServer(ctx, "rg1", "pg1", "pg1-w9"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetServer missing: got %v, want NotFound", err)
	}
}

func TestConfigurations(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 1)

	// Default value comes from the catalog.
	sc, err := m.GetCoordinatorConfiguration(ctx, "rg1", "pg1", "max_connections")
	if err != nil || sc.Value != "300" || sc.Source != "system-default" {
		t.Fatalf("default coordinator config wrong: %+v err=%v", sc, err)
	}

	// Update overrides the coordinator value only.
	if _, err := m.UpdateCoordinatorConfiguration(ctx, "rg1", "pg1", "max_connections", "500"); err != nil {
		t.Fatalf("UpdateCoordinatorConfiguration: %v", err)
	}

	sc, _ = m.GetCoordinatorConfiguration(ctx, "rg1", "pg1", "max_connections")
	if sc.Value != "500" || sc.Source != "user-override" {
		t.Fatalf("coordinator override not applied: %+v", sc)
	}

	node, _ := m.GetNodeConfiguration(ctx, "rg1", "pg1", "max_connections")
	if node.Value != "300" {
		t.Fatalf("node value should be unchanged default: %+v", node)
	}

	// Cluster-wide view shows both role groups.
	cfg, err := m.GetConfiguration(ctx, "rg1", "pg1", "max_connections")
	if err != nil || len(cfg.RoleGroups) != 2 {
		t.Fatalf("GetConfiguration wrong: %+v err=%v", cfg, err)
	}

	if _, err := m.GetConfiguration(ctx, "rg1", "pg1", "no_such_param"); !cerrors.IsNotFound(err) {
		t.Fatalf("unknown config: got %v, want NotFound", err)
	}
}

func TestReadReplicaPromotion(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "primary", 2)

	srcID := m.clusterResourceID("rg1", "primary")

	// Create a replica pointing at the primary.
	if _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "replica", ResourceGroup: "rg1", Location: "westus",
		SourceResourceID: srcID, SourceLocation: "eastus",
	}); err != nil {
		t.Fatalf("create replica: %v", err)
	}

	// The primary now lists the replica.
	primary, _ := m.GetCluster(ctx, "rg1", "primary")
	if len(primary.ReadReplicas) != 1 {
		t.Fatalf("primary should list one replica: %+v", primary.ReadReplicas)
	}

	// Promote detaches it.
	if err := m.PromoteReadReplica(ctx, "rg1", "replica"); err != nil {
		t.Fatalf("PromoteReadReplica: %v", err)
	}

	rep, _ := m.GetCluster(ctx, "rg1", "replica")
	if rep.SourceResourceID != "" {
		t.Fatalf("replica still linked after promote: %+v", rep)
	}

	primary, _ = m.GetCluster(ctx, "rg1", "primary")
	if len(primary.ReadReplicas) != 0 {
		t.Fatalf("primary should have no replicas after promote: %+v", primary.ReadReplicas)
	}

	// Promoting a non-replica fails.
	if err := m.PromoteReadReplica(ctx, "rg1", "primary"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("promote non-replica: got %v, want FailedPrecondition", err)
	}
}

func TestCheckNameAvailability(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	na, err := m.CheckNameAvailability(ctx, "free", "")
	if err != nil || !na.NameAvailable {
		t.Fatalf("free name should be available: %+v err=%v", na, err)
	}

	mustCluster(t, m, "rg1", "taken", 1)

	na, _ = m.CheckNameAvailability(ctx, "taken", "")
	if na.NameAvailable {
		t.Fatalf("taken name should be unavailable: %+v", na)
	}
}

func TestClusterResultDoesNotAliasStore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "pg1", ResourceGroup: "rg1", Tags: map[string]string{"env": "prod"},
		MaintenanceWindow: &cpgdriver.MaintenanceWindow{DayOfWeek: 1, StartHour: 2},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, _ := m.GetCluster(ctx, "rg1", "pg1")
	got.Tags["env"] = "hacked"
	got.MaintenanceWindow.StartHour = 23

	again, _ := m.GetCluster(ctx, "rg1", "pg1")
	if again.Tags["env"] != "prod" || again.MaintenanceWindow.StartHour != 2 {
		t.Fatal("returned cluster aliases the store (clone-on-read broken)")
	}
}

func intPtr(i int) *int { return &i }
