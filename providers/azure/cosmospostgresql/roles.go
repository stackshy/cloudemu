package cosmospostgresql

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

// CreateRole provisions a Postgres role on a cluster.
func (m *Mock) CreateRole(_ context.Context, cfg cpgdriver.CreateRoleConfig) (*cpgdriver.Role, error) {
	if err := validName("role", cfg.Name); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(clusterKey(cfg.ResourceGroup, cfg.ClusterName)) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "cluster %q not found", cfg.ClusterName)
	}

	key := childKey(cfg.ResourceGroup, cfg.ClusterName, cfg.Name)
	if m.roles.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "role %q already exists", cfg.Name)
	}

	role := cpgdriver.Role{
		Name:              cfg.Name,
		ClusterName:       cfg.ClusterName,
		ResourceGroup:     cfg.ResourceGroup,
		ProvisioningState: cpgdriver.ProvisioningSucceeded,
	}
	m.roles.Set(key, role)

	out := role

	return &out, nil
}

// GetRole returns a role by name.
func (m *Mock) GetRole(_ context.Context, rg, cluster, name string) (*cpgdriver.Role, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	role, ok := m.roles.Get(childKey(rg, cluster, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "role %q not found", name)
	}

	out := role

	return &out, nil
}

// ListRoles returns the roles of a cluster.
func (m *Mock) ListRoles(_ context.Context, rg, cluster string) ([]cpgdriver.Role, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return listChildren(m.roles, rg, cluster, roleKey, identity[cpgdriver.Role]), nil
}

func roleKey(role *cpgdriver.Role) string {
	return childKey(role.ResourceGroup, role.ClusterName, role.Name)
}

// DeleteRole removes a role.
func (m *Mock) DeleteRole(_ context.Context, rg, cluster, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := childKey(rg, cluster, name)
	if !m.roles.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "role %q not found", name)
	}

	m.roles.Delete(key)

	return nil
}
