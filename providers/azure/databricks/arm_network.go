package databricks

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

// Workspace sub-resource collection names.
const (
	pecType      = "privateEndpointConnections"
	plrType      = "privateLinkResources"
	peeringType  = "virtualNetworkPeerings"
	outboundType = "outboundNetworkDependenciesEndpoints"
)

// requireWorkspace returns NotFound when the parent workspace is absent. The
// workspace sub-resources (PEC, private link, peering, outbound) are all scoped
// under a workspace and must reject requests for a missing one.
func (m *Mock) requireWorkspace(resourceGroup, workspace string) error {
	if !m.workspaces.Has(key(resourceGroup, workspace)) {
		return errors.Newf(errors.NotFound, "workspace %q not found", workspace)
	}

	return nil
}

// subKey builds a store key scoped under a workspace: rg/workspace/type/name.
func subKey(resourceGroup, workspace, childType, name string) string {
	return key(resourceGroup, workspace) + "/" + childType + "/" + name
}

// subID builds the ARM ID for a workspace sub-resource. The subscription is
// taken from the parent workspace so a child's id shares its parent's
// subscription rather than defaulting to the emulator account.
func (m *Mock) subID(resourceGroup, workspace, childType, name string) string {
	sub := m.opts.AccountID
	if ws, ok := m.workspaces.Get(key(resourceGroup, workspace)); ok && ws.Subscription != "" {
		sub = ws.Subscription
	}

	return idgen.AzureID(sub, resourceGroup, providerNamespace, resourceType, workspace) +
		"/" + childType + "/" + name
}

// --- Private endpoint connections ---

// PutPrivateEndpointConnection creates or updates a private-endpoint connection
// on a workspace (store-and-echo of the approval state).
func (m *Mock) PutPrivateEndpointConnection(
	_ context.Context, resourceGroup, workspace, name, status, description string,
) (*driver.PrivateEndpointConnection, error) {
	if err := m.requireWorkspace(resourceGroup, workspace); err != nil {
		return nil, err
	}

	if name == "" {
		return nil, errors.New(errors.InvalidArgument, "private endpoint connection name is required")
	}

	if status == "" {
		status = "Approved"
	}

	k := subKey(resourceGroup, workspace, pecType, name)

	c := &driver.PrivateEndpointConnection{
		ID:                m.subID(resourceGroup, workspace, pecType, name),
		Name:              name,
		GroupIDs:          []string{groupUIAPI},
		Status:            status,
		Description:       description,
		ProvisioningState: driver.StateSucceeded,
	}

	// Preserve a previously recorded private-endpoint reference across updates.
	if existing, ok := m.privateEndpoints.Get(k); ok {
		c.PrivateEndpointID = existing.PrivateEndpointID
	}

	m.privateEndpoints.Set(k, c)

	return clonePEC(c), nil
}

// GetPrivateEndpointConnection returns a private-endpoint connection by name.
func (m *Mock) GetPrivateEndpointConnection(
	_ context.Context, resourceGroup, workspace, name string,
) (*driver.PrivateEndpointConnection, error) {
	c, ok := m.privateEndpoints.Get(subKey(resourceGroup, workspace, pecType, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "private endpoint connection %q not found", name)
	}

	return clonePEC(c), nil
}

// DeletePrivateEndpointConnection removes a private-endpoint connection.
func (m *Mock) DeletePrivateEndpointConnection(_ context.Context, resourceGroup, workspace, name string) error {
	if !m.privateEndpoints.Delete(subKey(resourceGroup, workspace, pecType, name)) {
		return errors.Newf(errors.NotFound, "private endpoint connection %q not found", name)
	}

	return nil
}

// ListPrivateEndpointConnections lists a workspace's private-endpoint connections.
//
//nolint:dupl // parallel per-collection prefix scan; mirrors ListVNetPeerings over a different store/type
func (m *Mock) ListPrivateEndpointConnections(
	_ context.Context, resourceGroup, workspace string,
) ([]driver.PrivateEndpointConnection, error) {
	if err := m.requireWorkspace(resourceGroup, workspace); err != nil {
		return nil, err
	}

	prefix := key(resourceGroup, workspace) + "/" + pecType + "/"
	out := make([]driver.PrivateEndpointConnection, 0)

	for k, c := range m.privateEndpoints.All() {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, *clonePEC(c))
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}

func clonePEC(c *driver.PrivateEndpointConnection) *driver.PrivateEndpointConnection {
	clone := *c
	clone.GroupIDs = append([]string(nil), c.GroupIDs...)

	return &clone
}

// --- Private link resources (synthesized, per workspace) ---

// Databricks workspace private-link group IDs.
const (
	groupUIAPI = "databricks_ui_api"
	groupAuth  = "browser_authentication"
)

// GetPrivateLinkResource returns the private-link resource for a group id.
func (m *Mock) GetPrivateLinkResource(
	_ context.Context, resourceGroup, workspace, groupID string,
) (*driver.GroupIDInformation, error) {
	if err := m.requireWorkspace(resourceGroup, workspace); err != nil {
		return nil, err
	}

	all := m.privateLinkResources(resourceGroup, workspace)
	for i := range all {
		if all[i].GroupID == groupID || all[i].Name == groupID {
			out := all[i]

			return &out, nil
		}
	}

	return nil, errors.Newf(errors.NotFound, "private link resource %q not found", groupID)
}

// ListPrivateLinkResources lists a workspace's private-link resources.
func (m *Mock) ListPrivateLinkResources(
	_ context.Context, resourceGroup, workspace string,
) ([]driver.GroupIDInformation, error) {
	if err := m.requireWorkspace(resourceGroup, workspace); err != nil {
		return nil, err
	}

	return m.privateLinkResources(resourceGroup, workspace), nil
}

// privateLinkResources returns the synthesized private-link group set for a
// workspace: the two group IDs a real Databricks workspace exposes.
func (m *Mock) privateLinkResources(resourceGroup, workspace string) []driver.GroupIDInformation {
	return []driver.GroupIDInformation{
		{
			ID:                m.subID(resourceGroup, workspace, plrType, groupUIAPI),
			Name:              groupUIAPI,
			GroupID:           groupUIAPI,
			RequiredMembers:   []string{"databricks_ui_api"},
			RequiredZoneNames: []string{"privatelink.azuredatabricks.net"},
		},
		{
			ID:                m.subID(resourceGroup, workspace, plrType, groupAuth),
			Name:              groupAuth,
			GroupID:           groupAuth,
			RequiredMembers:   []string{"browser_authentication"},
			RequiredZoneNames: []string{"privatelink.azuredatabricks.net"},
		},
	}
}

// --- Virtual network peerings ---

// CreateOrUpdateVNetPeering creates or updates a workspace VNet peering
// (store-and-echo; peering springs to Connected/Succeeded synchronously).
func (m *Mock) CreateOrUpdateVNetPeering(
	_ context.Context, resourceGroup, workspace, name string, cfg driver.VirtualNetworkPeeringConfig,
) (*driver.VirtualNetworkPeering, error) {
	if err := m.requireWorkspace(resourceGroup, workspace); err != nil {
		return nil, err
	}

	if name == "" {
		return nil, errors.New(errors.InvalidArgument, "peering name is required")
	}

	p := &driver.VirtualNetworkPeering{
		ID:                        m.subID(resourceGroup, workspace, peeringType, name),
		Name:                      name,
		AllowForwardedTraffic:     cfg.AllowForwardedTraffic,
		AllowGatewayTransit:       cfg.AllowGatewayTransit,
		AllowVirtualNetworkAccess: cfg.AllowVirtualNetworkAccess,
		UseRemoteGateways:         cfg.UseRemoteGateways,
		DatabricksVNetID:          cfg.DatabricksVNetID,
		DatabricksAddressSpace:    cloneAddressSpace(cfg.DatabricksAddressSpace),
		RemoteVNetID:              cfg.RemoteVNetID,
		RemoteAddressSpace:        cloneAddressSpace(cfg.RemoteAddressSpace),
		PeeringState:              driver.PeeringStateConnected,
		ProvisioningState:         driver.StateSucceeded,
	}

	m.vnetPeerings.Set(subKey(resourceGroup, workspace, peeringType, name), p)

	return clonePeering(p), nil
}

// GetVNetPeering returns a workspace VNet peering by name.
func (m *Mock) GetVNetPeering(
	_ context.Context, resourceGroup, workspace, name string,
) (*driver.VirtualNetworkPeering, error) {
	p, ok := m.vnetPeerings.Get(subKey(resourceGroup, workspace, peeringType, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "virtual network peering %q not found", name)
	}

	return clonePeering(p), nil
}

// DeleteVNetPeering removes a workspace VNet peering.
func (m *Mock) DeleteVNetPeering(_ context.Context, resourceGroup, workspace, name string) error {
	if !m.vnetPeerings.Delete(subKey(resourceGroup, workspace, peeringType, name)) {
		return errors.Newf(errors.NotFound, "virtual network peering %q not found", name)
	}

	return nil
}

// ListVNetPeerings lists a workspace's VNet peerings.
//
//nolint:dupl // parallel per-collection prefix scan; mirrors ListPrivateEndpointConnections over a different store/type
func (m *Mock) ListVNetPeerings(
	_ context.Context, resourceGroup, workspace string,
) ([]driver.VirtualNetworkPeering, error) {
	if err := m.requireWorkspace(resourceGroup, workspace); err != nil {
		return nil, err
	}

	prefix := key(resourceGroup, workspace) + "/" + peeringType + "/"
	out := make([]driver.VirtualNetworkPeering, 0)

	for k, p := range m.vnetPeerings.All() {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, *clonePeering(p))
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}

func clonePeering(p *driver.VirtualNetworkPeering) *driver.VirtualNetworkPeering {
	clone := *p
	clone.DatabricksAddressSpace = cloneAddressSpace(p.DatabricksAddressSpace)
	clone.RemoteAddressSpace = cloneAddressSpace(p.RemoteAddressSpace)

	return &clone
}

func cloneAddressSpace(in *driver.AddressSpace) *driver.AddressSpace {
	if in == nil {
		return nil
	}

	return &driver.AddressSpace{AddressPrefixes: append([]string(nil), in.AddressPrefixes...)}
}

// --- Outbound network dependencies (synthesized, per workspace) ---

// ListOutboundNetworkDependencies returns the synthesized outbound network
// dependency endpoints a workspace reaches. Store-and-echo: the domains mirror
// the real control-plane categories; live reachability is not probed.
func (m *Mock) ListOutboundNetworkDependencies(
	_ context.Context, resourceGroup, workspace string,
) ([]driver.OutboundEndpoint, error) {
	if err := m.requireWorkspace(resourceGroup, workspace); err != nil {
		return nil, err
	}

	https := []driver.EndpointDetail{{Port: 443}}

	return []driver.OutboundEndpoint{
		{
			Category: "control-plane",
			Endpoints: []driver.EndpointDependency{
				{DomainName: "cp.azuredatabricks.net", EndpointDetails: https},
			},
		},
		{
			Category: "azure-storage",
			Endpoints: []driver.EndpointDependency{
				{DomainName: "dbstorage.blob.core.windows.net", EndpointDetails: https},
			},
		},
		{
			Category: "azure-eventhub",
			Endpoints: []driver.EndpointDependency{
				{DomainName: "prod.servicebus.windows.net", EndpointDetails: https},
			},
		},
	}, nil
}
