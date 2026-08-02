package cosmospostgresql

import (
	"bytes"
	"context"
	"net"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

// validateIPRange rejects non-IPv4 endpoints and reversed ranges, matching the
// real Azure firewall-rule validation.
func validateIPRange(start, end string) error {
	s, e := net.ParseIP(start).To4(), net.ParseIP(end).To4()
	if s == nil || e == nil {
		return cerrors.New(cerrors.InvalidArgument, "startIpAddress and endIpAddress must be valid IPv4 addresses")
	}

	if bytes.Compare(s, e) > 0 {
		return cerrors.New(cerrors.InvalidArgument, "startIpAddress must be less than or equal to endIpAddress")
	}

	return nil
}

// CreateOrUpdateFirewallRule creates or replaces a firewall rule on a cluster.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateOrUpdateFirewallRule(_ context.Context, cfg cpgdriver.CreateFirewallRuleConfig) (*cpgdriver.FirewallRule, error) {
	if err := validName("firewall rule", cfg.Name); err != nil {
		return nil, err
	}

	if err := validateIPRange(cfg.StartIPAddress, cfg.EndIPAddress); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireClusterLocked(cfg.ResourceGroup, cfg.ClusterName); err != nil {
		return nil, err
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

	if err := m.requireClusterLocked(rg, cluster); err != nil {
		return nil, err
	}

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
