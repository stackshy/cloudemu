// Package azurefirewall provides an in-memory implementation of the Azure
// Firewall (Microsoft.Network/azureFirewalls) and Firewall Policy
// (Microsoft.Network/firewallPolicies) stores. Both ARM bodies are stored
// natively (their nested shape has no cross-cloud equivalent), keyed by
// (resourceGroup, name).
package azurefirewall

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/azurefirewall/driver"
)

// Compile-time check that Mock implements the Azure Firewall store.
var _ driver.AzureFirewalls = (*Mock)(nil)

// Mock is an in-memory Azure Firewall + Firewall Policy store.
type Mock struct {
	firewalls *memstore.Store[driver.AzureFirewall]
	policies  *memstore.Store[driver.FirewallPolicy]
	opts      *config.Options
}

// New creates a new Azure Firewall mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		firewalls: memstore.New[driver.AzureFirewall](),
		policies:  memstore.New[driver.FirewallPolicy](),
		opts:      opts,
	}
}

// key keys a store by (resourceGroup, name). ARM names are case-insensitive, so
// the key is lower-cased to resolve differently-cased GET/DELETE requests to the
// same resource and keep a re-PUT an in-place update rather than a duplicate.
// Only the map key is normalized; the stored body preserves the original casing.
func key(rg, name string) string {
	return strings.ToLower(rg) + "/" + strings.ToLower(name)
}

// CreateOrUpdateAzureFirewall stores fw as a full replace and reports whether it
// did not previously exist.
//
//nolint:gocritic,dupl // hugeParam: value copied defensively; firewall/policy CRUD are structurally parallel over distinct types.
func (m *Mock) CreateOrUpdateAzureFirewall(
	_ context.Context, rg, name string, fw driver.AzureFirewall,
) (*driver.AzureFirewall, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "azure firewall name is required")
	}

	_, existed := m.firewalls.Get(key(rg, name))

	stored := cloneFirewall(fw)
	stored.Name = name
	stored.ResourceGroup = rg

	m.firewalls.Set(key(rg, name), stored)

	out := cloneFirewall(stored)

	return &out, !existed, nil
}

// GetAzureFirewall returns the stored firewall.
func (m *Mock) GetAzureFirewall(_ context.Context, rg, name string) (*driver.AzureFirewall, error) {
	fw, ok := m.firewalls.Get(key(rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "azure firewall %q not found", name)
	}

	out := cloneFirewall(fw)

	return &out, nil
}

// DeleteAzureFirewall removes the stored firewall.
func (m *Mock) DeleteAzureFirewall(_ context.Context, rg, name string) error {
	if !m.firewalls.Delete(key(rg, name)) {
		return cerrors.Newf(cerrors.NotFound, "azure firewall %q not found", name)
	}

	return nil
}

// ListAzureFirewalls returns the firewalls in rg, or all when rg is empty
// (subscription-wide list).
func (m *Mock) ListAzureFirewalls(_ context.Context, rg string) ([]driver.AzureFirewall, error) {
	all := m.firewalls.SortedValues()

	out := make([]driver.AzureFirewall, 0, len(all))

	for i := range all {
		if rg != "" && !strings.EqualFold(all[i].ResourceGroup, rg) {
			continue
		}

		out = append(out, cloneFirewall(all[i]))
	}

	return out, nil
}

// CreateOrUpdateFirewallPolicy stores pol as a full replace and reports whether
// it did not previously exist.
//
//nolint:gocritic,dupl // hugeParam: value copied defensively; firewall/policy CRUD are structurally parallel over distinct types.
func (m *Mock) CreateOrUpdateFirewallPolicy(
	_ context.Context, rg, name string, pol driver.FirewallPolicy,
) (*driver.FirewallPolicy, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "firewall policy name is required")
	}

	_, existed := m.policies.Get(key(rg, name))

	stored := clonePolicy(pol)
	stored.Name = name
	stored.ResourceGroup = rg

	m.policies.Set(key(rg, name), stored)

	out := clonePolicy(stored)

	return &out, !existed, nil
}

// GetFirewallPolicy returns the stored policy.
func (m *Mock) GetFirewallPolicy(_ context.Context, rg, name string) (*driver.FirewallPolicy, error) {
	pol, ok := m.policies.Get(key(rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall policy %q not found", name)
	}

	out := clonePolicy(pol)

	return &out, nil
}

// DeleteFirewallPolicy removes the stored policy.
func (m *Mock) DeleteFirewallPolicy(_ context.Context, rg, name string) error {
	if !m.policies.Delete(key(rg, name)) {
		return cerrors.Newf(cerrors.NotFound, "firewall policy %q not found", name)
	}

	return nil
}

// ListFirewallPolicies returns the policies in rg, or all when rg is empty
// (subscription-wide list).
func (m *Mock) ListFirewallPolicies(_ context.Context, rg string) ([]driver.FirewallPolicy, error) {
	all := m.policies.SortedValues()

	out := make([]driver.FirewallPolicy, 0, len(all))

	for i := range all {
		if rg != "" && !strings.EqualFold(all[i].ResourceGroup, rg) {
			continue
		}

		out = append(out, clonePolicy(all[i]))
	}

	return out, nil
}

// cloneFirewall deep-copies a firewall so stored and returned values never alias
// a caller's maps or slices.
//
//nolint:gocritic // hugeParam: clone by value is the intent.
func cloneFirewall(fw driver.AzureFirewall) driver.AzureFirewall {
	out := fw
	out.Zones = append([]string(nil), fw.Zones...)
	out.Tags = cloneStringMap(fw.Tags)
	out.OtherProps = cloneAnyMap(fw.OtherProps)
	out.IPConfigurations = append([]driver.AzureFirewallIPConfig(nil), fw.IPConfigurations...)

	return out
}

// clonePolicy deep-copies a policy so stored and returned values never alias a
// caller's maps.
//
//nolint:gocritic // hugeParam: clone by value is the intent.
func clonePolicy(pol driver.FirewallPolicy) driver.FirewallPolicy {
	out := pol
	out.Tags = cloneStringMap(pol.Tags)
	out.OtherProps = cloneAnyMap(pol.OtherProps)

	return out
}

// cloneAnyMap deep-copies a generic JSON map so no request-owned map or slice is
// aliased into the store.
func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}

	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAnyValue(v)
	}

	return out
}

// cloneAnyValue recursively deep-copies a decoded JSON value.
func cloneAnyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneAnyMap(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = cloneAnyValue(t[i])
		}

		return out
	default:
		return v
	}
}

// cloneStringMap copies a string map, or returns nil for an empty one.
func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
