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

	if _, _, err := m.CreateOrUpdateCluster(context.Background(), cpgdriver.CreateClusterConfig{
		Name: name, ResourceGroup: rg, Location: "eastus", NodeCount: nodeCount,
	}); err != nil {
		t.Fatalf("CreateOrUpdateCluster %s: %v", name, err)
	}

	return name
}

func TestClusterLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	c, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
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

	if _, _, err := m.CreateOrUpdateCluster(context.Background(), cpgdriver.CreateClusterConfig{
		Name: "big", ResourceGroup: "rg1", NodeCount: maxNodeCount + 1,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("oversized nodeCount: got %v, want InvalidArgument", err)
	}
}

func TestFirewallRulesAndRoles(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 2)

	// A firewall rule under a missing cluster is rejected with NotFound.
	if _, err := m.CreateOrUpdateFirewallRule(ctx, cpgdriver.CreateFirewallRuleConfig{
		ResourceGroup: "rg1", ClusterName: "ghost", Name: "all", StartIPAddress: "0.0.0.0", EndIPAddress: "255.255.255.255",
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("fw under missing cluster: got %v, want NotFound", err)
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

	if _, err := m.CreateRole(ctx, cpgdriver.CreateRoleConfig{ResourceGroup: "rg1", ClusterName: "pg1", Name: "app", Password: "R0lePass!"}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := m.CreateRole(ctx, cpgdriver.CreateRoleConfig{
		ResourceGroup: "rg1", ClusterName: "pg1", Name: "app", Password: "R0lePass!",
	}); !cerrors.IsAlreadyExists(err) {
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
	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
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

	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
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

func TestPatchNodeCountValidated(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 2)

	// A negative PATCH nodeCount must be rejected (would otherwise store a bad
	// cap and crash node derivation).
	if _, err := m.UpdateCluster(ctx, "rg1", "pg1", cpgdriver.ClusterPatch{NodeCount: intPtr(-2)}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("negative PATCH nodeCount: got %v, want InvalidArgument", err)
	}

	if _, err := m.UpdateCluster(ctx, "rg1", "pg1", cpgdriver.ClusterPatch{NodeCount: intPtr(maxNodeCount + 1)}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("oversized PATCH nodeCount: got %v, want InvalidArgument", err)
	}

	// The stored cluster is unchanged, and node derivation still works.
	if _, err := m.ListServers(ctx, "rg1", "pg1"); err != nil {
		t.Fatalf("ListServers after rejected patch: %v", err)
	}
}

func TestClusterNameGloballyUnique(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 1)

	// Same name in a different resource group is rejected.
	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "pg1", ResourceGroup: "rg2", Location: "eastus",
	}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate name across RGs: got %v, want AlreadyExists", err)
	}

	// Re-PUT of the same rg/name is an update, not a conflict.
	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "pg1", ResourceGroup: "rg1", Location: "eastus", NodeCount: 3,
	}); err != nil {
		t.Fatalf("re-PUT same cluster: %v", err)
	}
}

func TestReplicaSourceValidated(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "primary", 2)

	// A bogus source is rejected.
	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "rep1", ResourceGroup: "rg1", SourceResourceID: m.clusterResourceID("rg1", "ghost"),
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("bogus replica source: got %v, want NotFound", err)
	}

	// A valid replica, then a replica-of-a-replica is rejected.
	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "rep1", ResourceGroup: "rg1", SourceResourceID: m.clusterResourceID("rg1", "primary"),
	}); err != nil {
		t.Fatalf("valid replica: %v", err)
	}

	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "rep2", ResourceGroup: "rg1", SourceResourceID: m.clusterResourceID("rg1", "rep1"),
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("chained replica: got %v, want InvalidArgument", err)
	}
}

func TestDeleteUnlinksReplicas(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "primary", 2)

	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "rep1", ResourceGroup: "rg1", SourceResourceID: m.clusterResourceID("rg1", "primary"),
	}); err != nil {
		t.Fatalf("create replica: %v", err)
	}

	// Deleting the source orphans the replica (clears its SourceResourceID).
	if err := m.DeleteCluster(ctx, "rg1", "primary"); err != nil {
		t.Fatalf("delete primary: %v", err)
	}

	rep, _ := m.GetCluster(ctx, "rg1", "rep1")
	if rep.SourceResourceID != "" {
		t.Fatalf("replica still links a deleted source: %+v", rep.SourceResourceID)
	}
}

func TestDeleteReplicaUnlinksFromSource(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "primary", 2)

	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "rep1", ResourceGroup: "rg1", SourceResourceID: m.clusterResourceID("rg1", "primary"),
	}); err != nil {
		t.Fatalf("create replica: %v", err)
	}

	if err := m.DeleteCluster(ctx, "rg1", "rep1"); err != nil {
		t.Fatalf("delete replica: %v", err)
	}

	primary, _ := m.GetCluster(ctx, "rg1", "primary")
	if len(primary.ReadReplicas) != 0 {
		t.Fatalf("source still lists a deleted replica: %+v", primary.ReadReplicas)
	}
}

func TestFirewallIPValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 1)

	// Non-IPv4.
	if _, err := m.CreateOrUpdateFirewallRule(ctx, cpgdriver.CreateFirewallRuleConfig{
		ResourceGroup: "rg1", ClusterName: "pg1", Name: "bad", StartIPAddress: "not-an-ip", EndIPAddress: "1.2.3.4",
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad IP: got %v, want InvalidArgument", err)
	}

	// Reversed range.
	if _, err := m.CreateOrUpdateFirewallRule(ctx, cpgdriver.CreateFirewallRuleConfig{
		ResourceGroup: "rg1", ClusterName: "pg1", Name: "rev", StartIPAddress: "203.0.113.50", EndIPAddress: "203.0.113.10",
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("reversed range: got %v, want InvalidArgument", err)
	}
}

func TestListChildrenRequireParent(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.ListFirewallRules(ctx, "rg1", "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("list fw missing parent: got %v, want NotFound", err)
	}

	if _, err := m.ListRoles(ctx, "rg1", "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("list roles missing parent: got %v, want NotFound", err)
	}

	if _, err := m.ListPrivateEndpointConnections(ctx, "rg1", "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("list PE missing parent: got %v, want NotFound", err)
	}
}

func TestClusterStateGuards(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 1)

	// Can't start an already-running cluster.
	if err := m.StartCluster(ctx, "rg1", "pg1"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("start running cluster: got %v, want FailedPrecondition", err)
	}

	// Stop → can't stop again; start brings it back.
	if err := m.StopCluster(ctx, "rg1", "pg1"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if err := m.StopCluster(ctx, "rg1", "pg1"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("stop stopped cluster: got %v, want FailedPrecondition", err)
	}

	if err := m.StartCluster(ctx, "rg1", "pg1"); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := m.RestartCluster(ctx, "rg1", "pg1"); err != nil {
		t.Fatalf("restart running cluster: %v", err)
	}
}

func TestConfigValueValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 1)

	// Empty value rejected.
	if _, err := m.UpdateCoordinatorConfiguration(ctx, "rg1", "pg1", "max_connections", ""); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("empty value: got %v, want InvalidArgument", err)
	}

	// Out-of-range integer rejected (max_connections range is 25-3000).
	if _, err := m.UpdateCoordinatorConfiguration(ctx, "rg1", "pg1", "max_connections", "999999"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("out-of-range: got %v, want InvalidArgument", err)
	}

	// Enum violation rejected (array_nulls allows on,off).
	if _, err := m.UpdateNodeConfiguration(ctx, "rg1", "pg1", "array_nulls", "maybe"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("enum violation: got %v, want InvalidArgument", err)
	}

	// A valid in-range value is accepted.
	if _, err := m.UpdateCoordinatorConfiguration(ctx, "rg1", "pg1", "max_connections", "500"); err != nil {
		t.Fatalf("valid value: %v", err)
	}
}

func TestRolePasswordRequired(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 1)

	if _, err := m.CreateRole(ctx, cpgdriver.CreateRoleConfig{ResourceGroup: "rg1", ClusterName: "pg1", Name: "app"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("role without password: got %v, want InvalidArgument", err)
	}
}

func TestFirewallAndRoleGetDelete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 1)

	if _, err := m.CreateOrUpdateFirewallRule(ctx, cpgdriver.CreateFirewallRuleConfig{
		ResourceGroup: "rg1", ClusterName: "pg1", Name: "fw", StartIPAddress: "10.0.0.0", EndIPAddress: "10.0.0.255",
	}); err != nil {
		t.Fatalf("create fw: %v", err)
	}

	if _, err := m.GetFirewallRule(ctx, "rg1", "pg1", "fw"); err != nil {
		t.Fatalf("GetFirewallRule: %v", err)
	}

	if err := m.DeleteFirewallRule(ctx, "rg1", "pg1", "fw"); err != nil {
		t.Fatalf("DeleteFirewallRule: %v", err)
	}

	if _, err := m.GetFirewallRule(ctx, "rg1", "pg1", "fw"); !cerrors.IsNotFound(err) {
		t.Fatalf("get deleted fw: got %v, want NotFound", err)
	}

	if _, err := m.CreateRole(ctx, cpgdriver.CreateRoleConfig{ResourceGroup: "rg1", ClusterName: "pg1", Name: "app", Password: "R0lePass!"}); err != nil {
		t.Fatalf("create role: %v", err)
	}

	if _, err := m.GetRole(ctx, "rg1", "pg1", "app"); err != nil {
		t.Fatalf("GetRole: %v", err)
	}

	if err := m.DeleteRole(ctx, "rg1", "pg1", "app"); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}

	if err := m.DeleteRole(ctx, "rg1", "pg1", "app"); !cerrors.IsNotFound(err) {
		t.Fatalf("delete missing role: got %v, want NotFound", err)
	}
}

func TestConfigurationsListingAndServerConfigs(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 1)

	cfgs, err := m.ListConfigurations(ctx, "rg1", "pg1")
	if err != nil || len(cfgs) == 0 {
		t.Fatalf("ListConfigurations: %v len=%d", err, len(cfgs))
	}

	scs, err := m.ListServerConfigurations(ctx, "rg1", "pg1", "pg1-c")
	if err != nil || len(scs) == 0 {
		t.Fatalf("ListServerConfigurations: %v len=%d", err, len(scs))
	}

	if _, err := m.GetNodeConfiguration(ctx, "rg1", "pg1", "work_mem"); err != nil {
		t.Fatalf("GetNodeConfiguration: %v", err)
	}

	// Listing under a missing cluster is NotFound.
	if _, err := m.ListConfigurations(ctx, "rg1", "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("ListConfigurations missing parent: got %v, want NotFound", err)
	}
}

func TestPrivateEndpointsAndLinks(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 1)

	// Invalid connection status is rejected.
	if _, err := m.CreateOrUpdatePrivateEndpointConnection(ctx, "rg1", "pg1", "pe1", "Bogus", ""); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad PE status: got %v, want InvalidArgument", err)
	}

	pec, err := m.CreateOrUpdatePrivateEndpointConnection(ctx, "rg1", "pg1", "pe1", "Approved", "ok")
	if err != nil || pec.ActionsRequired != "None" {
		t.Fatalf("create PE: %v %+v", err, pec)
	}

	if _, err := m.GetPrivateEndpointConnection(ctx, "rg1", "pg1", "pe1"); err != nil {
		t.Fatalf("GetPrivateEndpointConnection: %v", err)
	}

	pecs, _ := m.ListPrivateEndpointConnections(ctx, "rg1", "pg1")
	if len(pecs) != 1 {
		t.Fatalf("ListPrivateEndpointConnections: got %d, want 1", len(pecs))
	}

	if err := m.DeletePrivateEndpointConnection(ctx, "rg1", "pg1", "pe1"); err != nil {
		t.Fatalf("DeletePrivateEndpointConnection: %v", err)
	}

	// Private-link resources: one "coordinator" group.
	plrs, err := m.ListPrivateLinkResources(ctx, "rg1", "pg1")
	if err != nil || len(plrs) != 1 {
		t.Fatalf("ListPrivateLinkResources: %v len=%d", err, len(plrs))
	}

	if _, err := m.GetPrivateLinkResource(ctx, "rg1", "pg1", "coordinator"); err != nil {
		t.Fatalf("GetPrivateLinkResource: %v", err)
	}

	if _, err := m.GetPrivateLinkResource(ctx, "rg1", "pg1", "nope"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetPrivateLinkResource missing: got %v, want NotFound", err)
	}
}

func TestReplicaSourceImmutableOnRePut(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "primary", 2)
	mustCluster(t, m, "rg1", "other", 2)

	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "replica", ResourceGroup: "rg1", SourceResourceID: m.clusterResourceID("rg1", "primary"),
	}); err != nil {
		t.Fatalf("create replica: %v", err)
	}

	// Re-PUT the replica trying to re-point it at "other": the source is
	// immutable, so the link graph must not change.
	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "replica", ResourceGroup: "rg1", SourceResourceID: m.clusterResourceID("rg1", "other"),
	}); err != nil {
		t.Fatalf("re-PUT replica: %v", err)
	}

	rep, _ := m.GetCluster(ctx, "rg1", "replica")
	if rep.SourceResourceID != m.clusterResourceID("rg1", "primary") {
		t.Fatalf("replica source changed on re-PUT: %q", rep.SourceResourceID)
	}

	other, _ := m.GetCluster(ctx, "rg1", "other")
	if len(other.ReadReplicas) != 0 {
		t.Fatalf("re-PUT wrongly linked 'other': %+v", other.ReadReplicas)
	}

	primary, _ := m.GetCluster(ctx, "rg1", "primary")
	if len(primary.ReadReplicas) != 1 {
		t.Fatalf("primary lost its replica link: %+v", primary.ReadReplicas)
	}
}

func TestCreatedFlag(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, created, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{Name: "pg1", ResourceGroup: "rg1"})
	if err != nil || !created {
		t.Fatalf("first PUT: created=%v err=%v", created, err)
	}

	_, created, err = m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{Name: "pg1", ResourceGroup: "rg1"})
	if err != nil || created {
		t.Fatalf("re-PUT: created=%v err=%v", created, err)
	}
}

func TestPatchAppliesWritableFields(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "pg1", 2)

	pub, shards := true, true
	up, err := m.UpdateCluster(ctx, "rg1", "pg1", cpgdriver.ClusterPatch{
		CoordinatorEnablePublicIPAccess: &pub,
		NodeEnablePublicIPAccess:        &pub,
		EnableShardsOnCoordinator:       &shards,
		NodeCount:                       intPtr(0),
	})
	if err != nil {
		t.Fatalf("UpdateCluster: %v", err)
	}

	if !up.CoordinatorEnablePublicIPAccess || !up.NodeEnablePublicIPAccess || !up.EnableShardsOnCoordinator {
		t.Fatalf("public-IP / shards flags not applied: %+v", up)
	}

	// PATCH scale-to-single-node (nodeCount 0) must take effect.
	if up.NodeCount != 0 {
		t.Fatalf("scale-to-single-node not applied: %d", up.NodeCount)
	}
}

func TestReplicaNodesReadOnly(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "rg1", "primary", 2)

	if _, _, err := m.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "rep1", ResourceGroup: "rg1", NodeCount: 2, SourceResourceID: m.clusterResourceID("rg1", "primary"),
	}); err != nil {
		t.Fatalf("create replica: %v", err)
	}

	nodes, _ := m.ListServers(ctx, "rg1", "rep1")
	for i := range nodes {
		if !nodes[i].IsReadOnly {
			t.Fatalf("replica node %q (%s) is not read-only", nodes[i].Name, nodes[i].Role)
		}
	}
}
