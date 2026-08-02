package cosmospostgresql

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

func clonePEC(in *cpgdriver.PrivateEndpointConnection) cpgdriver.PrivateEndpointConnection {
	pec := *in
	pec.GroupIDs = cloneStrings(in.GroupIDs)

	return pec
}

// CreateOrUpdatePrivateEndpointConnection creates or updates a private-endpoint
// connection (approving/rejecting the link).
func (m *Mock) CreateOrUpdatePrivateEndpointConnection(
	_ context.Context, rg, cluster, name, status, description string,
) (*cpgdriver.PrivateEndpointConnection, error) {
	if err := validName("private endpoint connection", name); err != nil {
		return nil, err
	}

	status = orDefault(status, "Approved")
	if status != "Approved" && status != "Rejected" && status != "Pending" {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid connection status %q", status)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireClusterLocked(rg, cluster); err != nil {
		return nil, err
	}

	key := childKey(rg, cluster, name)

	pec := cpgdriver.PrivateEndpointConnection{
		Name:              name,
		ClusterName:       cluster,
		ResourceGroup:     rg,
		ProvisioningState: cpgdriver.ProvisioningSucceeded,
		GroupIDs:          []string{"coordinator"},
		PrivateEndpointID: m.clusterResourceID(rg, cluster) + "/privateEndpoints/" + name,
		ConnectionStatus:  status,
		ConnectionDesc:    description,
		ActionsRequired:   "None",
	}

	if existing, ok := m.privateEPs.Get(key); ok {
		pec.PrivateEndpointID = existing.PrivateEndpointID
	}

	m.privateEPs.Set(key, pec)

	out := clonePEC(&pec)

	return &out, nil
}

// GetPrivateEndpointConnection returns a connection by name.
func (m *Mock) GetPrivateEndpointConnection(_ context.Context, rg, cluster, name string) (*cpgdriver.PrivateEndpointConnection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pec, ok := m.privateEPs.Get(childKey(rg, cluster, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "private endpoint connection %q not found", name)
	}

	out := clonePEC(&pec)

	return &out, nil
}

// ListPrivateEndpointConnections returns the connections of a cluster.
func (m *Mock) ListPrivateEndpointConnections(_ context.Context, rg, cluster string) ([]cpgdriver.PrivateEndpointConnection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireClusterLocked(rg, cluster); err != nil {
		return nil, err
	}

	return listChildren(m.privateEPs, rg, cluster, pecKey, clonePEC), nil
}

func pecKey(pec *cpgdriver.PrivateEndpointConnection) string {
	return childKey(pec.ResourceGroup, pec.ClusterName, pec.Name)
}

// DeletePrivateEndpointConnection removes a connection.
func (m *Mock) DeletePrivateEndpointConnection(_ context.Context, rg, cluster, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := childKey(rg, cluster, name)
	if !m.privateEPs.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "private endpoint connection %q not found", name)
	}

	m.privateEPs.Delete(key)

	return nil
}

// GetPrivateLinkResource returns a private-link resource (group) of a cluster.
// The mock exposes a single "coordinator" group.
func (m *Mock) GetPrivateLinkResource(_ context.Context, rg, cluster, name string) (*cpgdriver.PrivateLinkResource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cluster)
	}

	if name != "coordinator" {
		return nil, cerrors.Newf(cerrors.NotFound, "private link resource %q not found", name)
	}

	plr := privateLinkResource(rg, cluster, name)

	return &plr, nil
}

// ListPrivateLinkResources returns the private-link resources of a cluster.
func (m *Mock) ListPrivateLinkResources(_ context.Context, rg, cluster string) ([]cpgdriver.PrivateLinkResource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cluster)
	}

	return []cpgdriver.PrivateLinkResource{privateLinkResource(rg, cluster, "coordinator")}, nil
}

func privateLinkResource(rg, cluster, name string) cpgdriver.PrivateLinkResource {
	return cpgdriver.PrivateLinkResource{
		Name:              name,
		ClusterName:       cluster,
		ResourceGroup:     rg,
		GroupID:           name,
		RequiredMembers:   []string{"coordinator"},
		RequiredZoneNames: []string{"privatelink.postgres.cosmos.azure.com"},
	}
}
