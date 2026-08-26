package loadbalancer

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Compile-time check that Mock implements the optional Azure LB surface.
var _ driver.AzureLoadBalancers = (*Mock)(nil)

// azureLBKey keys the native store by (resourceGroup, name), matching ARM's
// addressing.
func azureLBKey(rg, name string) string {
	return rg + "/" + name
}

// CreateOrUpdateAzureLoadBalancer stores the ARM load balancer as a full
// replace: the payload becomes the complete state, so any child (pool, rule,
// probe, frontend) omitted from it is dropped.
//
//nolint:gocritic // hugeParam: value carries slices copied defensively below.
func (m *Mock) CreateOrUpdateAzureLoadBalancer(_ context.Context, rg, name string,
	lb driver.AzureLoadBalancer,
) (*driver.AzureLoadBalancer, error) {
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "load balancer name is required")
	}

	stored := cloneAzureLB(lb)
	stored.Name = name
	stored.ResourceGroup = rg
	stored.ProvisioningState = "Succeeded"

	m.azureLBs.Set(azureLBKey(rg, name), stored)

	out := cloneAzureLB(stored)

	return &out, nil
}

// GetAzureLoadBalancer returns the stored ARM load balancer.
func (m *Mock) GetAzureLoadBalancer(_ context.Context, rg, name string) (*driver.AzureLoadBalancer, error) {
	lb, ok := m.azureLBs.Get(azureLBKey(rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "load balancer %q not found", name)
	}

	out := cloneAzureLB(lb)

	return &out, nil
}

// DeleteAzureLoadBalancer removes the stored ARM load balancer.
func (m *Mock) DeleteAzureLoadBalancer(_ context.Context, rg, name string) error {
	if !m.azureLBs.Delete(azureLBKey(rg, name)) {
		return cerrors.Newf(cerrors.NotFound, "load balancer %q not found", name)
	}

	return nil
}

// ListAzureLoadBalancers returns the stored ARM load balancers in rg, or all
// when rg is empty (subscription-wide list).
func (m *Mock) ListAzureLoadBalancers(_ context.Context, rg string) ([]driver.AzureLoadBalancer, error) {
	all := m.azureLBs.SortedValues()

	out := make([]driver.AzureLoadBalancer, 0, len(all))

	for i := range all {
		if rg != "" && !strings.EqualFold(all[i].ResourceGroup, rg) {
			continue
		}

		out = append(out, cloneAzureLB(all[i]))
	}

	return out, nil
}

// cloneAzureLB deep-copies the nested slices so stored and returned values
// never alias a caller's slices.
//
//nolint:gocritic // hugeParam: clone by value is the intent.
func cloneAzureLB(lb driver.AzureLoadBalancer) driver.AzureLoadBalancer {
	out := lb
	out.Frontends = append([]driver.AzureLBFrontend(nil), lb.Frontends...)
	out.BackendPools = append([]string(nil), lb.BackendPools...)
	out.Rules = append([]driver.AzureLBRule(nil), lb.Rules...)
	out.Probes = append([]driver.AzureLBProbe(nil), lb.Probes...)
	out.NatRules = append([]driver.AzureLBNatRule(nil), lb.NatRules...)
	out.NatPools = append([]driver.AzureLBNatPool(nil), lb.NatPools...)
	out.OutboundRules = append([]driver.AzureLBOutboundRule(nil), lb.OutboundRules...)

	if len(lb.Tags) > 0 {
		out.Tags = make(map[string]string, len(lb.Tags))
		for k, v := range lb.Tags {
			out.Tags[k] = v
		}
	}

	return out
}

// UpsertAzureLBBackendPool adds poolName to the load balancer's backend pools
// if absent, leaving every other child untouched.
func (m *Mock) UpsertAzureLBBackendPool(_ context.Context, rg, name, poolName string) (*driver.AzureLoadBalancer, error) {
	if poolName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "backend address pool name is required")
	}

	var updated driver.AzureLoadBalancer

	ok := m.azureLBs.Update(azureLBKey(rg, name), func(lb driver.AzureLoadBalancer) driver.AzureLoadBalancer {
		if !containsString(lb.BackendPools, poolName) {
			lb.BackendPools = append(append([]string(nil), lb.BackendPools...), poolName)
		}

		updated = lb

		return lb
	})
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "load balancer %q not found", name)
	}

	out := cloneAzureLB(updated)

	return &out, nil
}

// DeleteAzureLBBackendPool removes a single backend pool by name, leaving
// every other child untouched.
func (m *Mock) DeleteAzureLBBackendPool(_ context.Context, rg, name, poolName string) error {
	poolMissing := false

	ok := m.azureLBs.Update(azureLBKey(rg, name), func(lb driver.AzureLoadBalancer) driver.AzureLoadBalancer {
		idx := indexOfString(lb.BackendPools, poolName)
		if idx == -1 {
			poolMissing = true

			return lb
		}

		lb.BackendPools = removeStringAt(lb.BackendPools, idx)

		return lb
	})
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "load balancer %q not found", name)
	}

	if poolMissing {
		return cerrors.Newf(cerrors.NotFound, "backend address pool %q not found", poolName)
	}

	return nil
}

// UpsertAzureLBNatRule creates or replaces a single inbound NAT rule by name,
// leaving every other child untouched.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpsertAzureLBNatRule(
	_ context.Context, rg, name, natRuleName string, rule driver.AzureLBNatRule,
) (*driver.AzureLoadBalancer, error) {
	if natRuleName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "inbound NAT rule name is required")
	}

	rule.Name = natRuleName

	var (
		updated driver.AzureLoadBalancer
		valErr  error
	)

	ok := m.azureLBs.Update(azureLBKey(rg, name), func(lb driver.AzureLoadBalancer) driver.AzureLoadBalancer {
		if rule.FrontendName != "" && !hasFrontend(lb.Frontends, rule.FrontendName) {
			valErr = cerrors.Newf(cerrors.InvalidArgument,
				"inbound NAT rule %q references frontend IP configuration %q that does not exist",
				natRuleName, rule.FrontendName)

			return lb
		}

		rules := append([]driver.AzureLBNatRule(nil), lb.NatRules...)

		replaced := false

		for i := range rules {
			if rules[i].Name == natRuleName {
				rules[i] = rule
				replaced = true

				break
			}
		}

		if !replaced {
			rules = append(rules, rule)
		}

		lb.NatRules = rules
		updated = lb

		return lb
	})
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "load balancer %q not found", name)
	}

	if valErr != nil {
		return nil, valErr
	}

	out := cloneAzureLB(updated)

	return &out, nil
}

// DeleteAzureLBNatRule removes a single inbound NAT rule by name, leaving
// every other child untouched.
func (m *Mock) DeleteAzureLBNatRule(_ context.Context, rg, name, natRuleName string) error {
	ruleMissing := false

	ok := m.azureLBs.Update(azureLBKey(rg, name), func(lb driver.AzureLoadBalancer) driver.AzureLoadBalancer {
		idx := -1

		for i := range lb.NatRules {
			if lb.NatRules[i].Name == natRuleName {
				idx = i
				break
			}
		}

		if idx == -1 {
			ruleMissing = true

			return lb
		}

		lb.NatRules = append(append([]driver.AzureLBNatRule(nil), lb.NatRules[:idx]...), lb.NatRules[idx+1:]...)

		return lb
	})
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "load balancer %q not found", name)
	}

	if ruleMissing {
		return cerrors.Newf(cerrors.NotFound, "inbound NAT rule %q not found", natRuleName)
	}

	return nil
}

// hasFrontend reports whether name matches a frontend IP configuration in in.
func hasFrontend(in []driver.AzureLBFrontend, name string) bool {
	for i := range in {
		if strings.EqualFold(in[i].Name, name) {
			return true
		}
	}

	return false
}

// containsString reports whether s is present in in.
func containsString(in []string, s string) bool {
	return indexOfString(in, s) != -1
}

// indexOfString returns the index of s in in, or -1 if absent.
func indexOfString(in []string, s string) int {
	for i, v := range in {
		if v == s {
			return i
		}
	}

	return -1
}

// removeStringAt returns a copy of in with the element at idx removed.
func removeStringAt(in []string, idx int) []string {
	out := make([]string, 0, len(in)-1)
	out = append(out, in[:idx]...)
	out = append(out, in[idx+1:]...)

	return out
}
