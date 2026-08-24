package loadbalancer

import (
	"context"

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
		if rg != "" && all[i].ResourceGroup != rg {
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

	if len(lb.Tags) > 0 {
		out.Tags = make(map[string]string, len(lb.Tags))
		for k, v := range lb.Tags {
			out.Tags[k] = v
		}
	}

	return out
}
