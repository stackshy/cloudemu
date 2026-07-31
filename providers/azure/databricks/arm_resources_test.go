package databricks

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions())
}

func seedWS(t *testing.T, m *Mock, rg, name string) {
	t.Helper()

	_, err := m.CreateWorkspace(context.Background(), driver.WorkspaceConfig{
		Name: name, ResourceGroup: rg, Location: "eastus", ManagedResourceGroupID: "/subscriptions/s/resourceGroups/managed",
	})
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
}

// --- Access connectors ---

func TestAccessConnectorLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	ac, err := m.CreateOrUpdateAccessConnector(ctx, driver.AccessConnectorConfig{
		Name: "ac1", ResourceGroup: "rg", Location: "eastus",
		Tags:     map[string]string{"env": "test"},
		Identity: &driver.ManagedIdentity{Type: "SystemAssigned"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if ac.ProvisioningState != driver.StateSucceeded {
		t.Fatalf("provisioning = %q", ac.ProvisioningState)
	}

	if ac.Identity == nil || ac.Identity.PrincipalID == "" || ac.Identity.TenantID == "" {
		t.Fatalf("system-assigned identity should synthesize principal/tenant, got %+v", ac.Identity)
	}

	// Deterministic synthesis: same name → same GUIDs on re-create.
	principal := ac.Identity.PrincipalID

	got, err := m.GetAccessConnector(ctx, "rg", "ac1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Identity.PrincipalID != principal {
		t.Fatalf("principal not stable: %q vs %q", got.Identity.PrincipalID, principal)
	}

	// PATCH tags only; identity nil leaves identity unchanged.
	upd, err := m.UpdateAccessConnector(ctx, "rg", "ac1", map[string]string{"env": "prod"}, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if upd.Tags["env"] != "prod" || upd.Identity == nil {
		t.Fatalf("patch = %+v", upd)
	}

	if err := m.DeleteAccessConnector(ctx, "rg", "ac1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := m.GetAccessConnector(ctx, "rg", "ac1"); !errors.IsNotFound(err) {
		t.Fatalf("get after delete = %v, want NotFound", err)
	}
}

func TestAccessConnectorValidationAndListing(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.CreateOrUpdateAccessConnector(ctx, driver.AccessConnectorConfig{Name: "x", ResourceGroup: "rg"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("empty location = %v, want InvalidArgument", err)
	}

	mk := func(rg, n string) {
		if _, err := m.CreateOrUpdateAccessConnector(ctx, driver.AccessConnectorConfig{Name: n, ResourceGroup: rg, Location: "eastus"}); err != nil {
			t.Fatalf("create %s/%s: %v", rg, n, err)
		}
	}
	mk("rg1", "a")
	mk("rg1", "b")
	mk("rg2", "c")

	byRG, _ := m.ListAccessConnectorsByResourceGroup(ctx, "rg1")
	if len(byRG) != 2 {
		t.Fatalf("list rg1 = %d, want 2", len(byRG))
	}

	all, _ := m.ListAccessConnectors(ctx)
	if len(all) != 3 {
		t.Fatalf("list all = %d, want 3", len(all))
	}

	// "None" identity resolves to nil.
	ac, _ := m.CreateOrUpdateAccessConnector(ctx, driver.AccessConnectorConfig{
		Name: "none", ResourceGroup: "rg1", Location: "eastus", Identity: &driver.ManagedIdentity{Type: "None"},
	})
	if ac.Identity != nil {
		t.Fatalf("None identity should be nil, got %+v", ac.Identity)
	}
}

// --- Private endpoint connections ---

func TestPrivateEndpointConnectionLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	seedWS(t, m, "rg", "ws")

	c, err := m.PutPrivateEndpointConnection(ctx, "rg", "ws", "pec1", "", "hi")
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	if c.Status != "Approved" || c.ProvisioningState != driver.StateSucceeded {
		t.Fatalf("pec = %+v", c)
	}

	if len(c.GroupIDs) == 0 || c.GroupIDs[0] != groupUIAPI {
		t.Fatalf("groupIDs = %v", c.GroupIDs)
	}

	list, _ := m.ListPrivateEndpointConnections(ctx, "rg", "ws")
	if len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}

	if err := m.DeletePrivateEndpointConnection(ctx, "rg", "ws", "pec1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := m.GetPrivateEndpointConnection(ctx, "rg", "ws", "pec1"); !errors.IsNotFound(err) {
		t.Fatalf("get after delete = %v", err)
	}
}

func TestPrivateEndpointConnectionMissingWorkspace(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.PutPrivateEndpointConnection(ctx, "rg", "ghost", "pec", "Approved", ""); !errors.IsNotFound(err) {
		t.Fatalf("put on missing workspace = %v, want NotFound", err)
	}

	if _, err := m.ListPrivateEndpointConnections(ctx, "rg", "ghost"); !errors.IsNotFound(err) {
		t.Fatalf("list on missing workspace = %v, want NotFound", err)
	}
}

// --- Private link resources ---

func TestPrivateLinkResources(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	seedWS(t, m, "rg", "ws")

	list, err := m.ListPrivateLinkResources(ctx, "rg", "ws")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("want 2 private link resources, got %d", len(list))
	}

	g, err := m.GetPrivateLinkResource(ctx, "rg", "ws", groupUIAPI)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if g.GroupID != groupUIAPI || len(g.RequiredZoneNames) == 0 {
		t.Fatalf("plr = %+v", g)
	}

	if _, err := m.GetPrivateLinkResource(ctx, "rg", "ws", "nope"); !errors.IsNotFound(err) {
		t.Fatalf("get unknown = %v, want NotFound", err)
	}

	if _, err := m.ListPrivateLinkResources(ctx, "rg", "ghost"); !errors.IsNotFound(err) {
		t.Fatalf("list on missing workspace = %v, want NotFound", err)
	}
}

// --- VNet peering ---

func TestVNetPeeringLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	seedWS(t, m, "rg", "ws")

	p, err := m.CreateOrUpdateVNetPeering(ctx, "rg", "ws", "peer1", driver.VirtualNetworkPeeringConfig{
		AllowVirtualNetworkAccess: true,
		RemoteVNetID:              "/subscriptions/s/rg/x/providers/Microsoft.Network/virtualNetworks/remote",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if p.PeeringState != driver.PeeringStateConnected || p.ProvisioningState != driver.StateSucceeded {
		t.Fatalf("peering state = %+v", p)
	}

	if !p.AllowVirtualNetworkAccess || p.RemoteVNetID == "" {
		t.Fatalf("peering fields not echoed: %+v", p)
	}

	list, _ := m.ListVNetPeerings(ctx, "rg", "ws")
	if len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}

	if err := m.DeleteVNetPeering(ctx, "rg", "ws", "peer1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := m.GetVNetPeering(ctx, "rg", "ws", "peer1"); !errors.IsNotFound(err) {
		t.Fatalf("get after delete = %v", err)
	}
}

func TestVNetPeeringMissingWorkspace(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.CreateOrUpdateVNetPeering(ctx, "rg", "ghost", "peer", driver.VirtualNetworkPeeringConfig{})
	if !errors.IsNotFound(err) {
		t.Fatalf("create on missing workspace = %v, want NotFound", err)
	}
}

// --- Outbound & operations ---

func TestOutboundNetworkDependencies(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	seedWS(t, m, "rg", "ws")

	eps, err := m.ListOutboundNetworkDependencies(ctx, "rg", "ws")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(eps) == 0 {
		t.Fatal("want at least one outbound category")
	}

	for _, e := range eps {
		if e.Category == "" || len(e.Endpoints) == 0 || e.Endpoints[0].DomainName == "" {
			t.Fatalf("malformed outbound endpoint: %+v", e)
		}
	}

	if _, err := m.ListOutboundNetworkDependencies(ctx, "rg", "ghost"); !errors.IsNotFound(err) {
		t.Fatalf("list on missing workspace = %v, want NotFound", err)
	}
}

func TestListOperations(t *testing.T) {
	m := newMock(t)

	ops, err := m.ListOperations(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(ops) == 0 {
		t.Fatal("want a non-empty operations catalog")
	}

	found := false

	for _, o := range ops {
		if o.Provider != providerNamespace {
			t.Fatalf("op provider = %q, want %q", o.Provider, providerNamespace)
		}

		if o.Name == providerNamespace+"/workspaces/read" {
			found = true
		}
	}

	if !found {
		t.Fatal("expected workspaces/read in the operations catalog")
	}
}
