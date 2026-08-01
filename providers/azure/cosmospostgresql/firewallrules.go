package cosmospostgresql

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

// CreateOrUpdateFirewallRule creates or replaces a firewall rule on a cluster.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateOrUpdateFirewallRule(_ context.Context, cfg cpgdriver.CreateFirewallRuleConfig) (*cpgdriver.FirewallRule, error) {
	if err := validName("firewall rule", cfg.Name); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(clusterKey(cfg.ResourceGroup, cfg.ClusterName)) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "cluster %q not found", cfg.ClusterName)
	}

	fr := cpgdriver.FirewallRule{
		Name:              cfg.Name,
		ClusterName:       cfg.ClusterName,
		ResourceGroup:     cfg.ResourceGroup,
		ProvisioningState: cpgdriver.ProvisioningSucceeded,
		StartIPAddress:    cfg.StartIPAddress,
		EndIPAddress:      cfg.EndIPAddress,
	}
	m.firewallRules.Set(childKey(cfg.ResourceGroup, cfg.ClusterName, cfg.Name), fr)

	out := fr

	return &out, nil
}

// GetFirewallRule returns a firewall rule by name.
func (m *Mock) GetFirewallRule(_ context.Context, rg, cluster, name string) (*cpgdriver.FirewallRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	fr, ok := m.firewallRules.Get(childKey(rg, cluster, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", name)
	}

	out := fr

	return &out, nil
}

// ListFirewallRules returns the firewall rules of a cluster.
func (m *Mock) ListFirewallRules(_ context.Context, rg, cluster string) ([]cpgdriver.FirewallRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return listChildren(m.firewallRules, rg, cluster, firewallRuleKey, identity[cpgdriver.FirewallRule]), nil
}

func firewallRuleKey(fr *cpgdriver.FirewallRule) string {
	return childKey(fr.ResourceGroup, fr.ClusterName, fr.Name)
}

func identity[T any](v *T) T { return *v }

// DeleteFirewallRule removes a firewall rule.
func (m *Mock) DeleteFirewallRule(_ context.Context, rg, cluster, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := childKey(rg, cluster, name)
	if !m.firewallRules.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", name)
	}

	m.firewallRules.Delete(key)

	return nil
}
