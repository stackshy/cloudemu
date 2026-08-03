package vpc

import (
	"context"
	"fmt"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// CreateVPCEndpointServiceConfiguration publishes a PrivateLink endpoint service
// backed by one or more network load balancers.
func (m *Mock) CreateVPCEndpointServiceConfiguration(
	_ context.Context, cfg driver.EndpointServiceConfig,
) (*driver.EndpointService, error) {
	if len(cfg.NetworkLoadBalancerARNs) == 0 {
		return nil, errors.New(errors.InvalidArgument, "at least one network load balancer ARN is required")
	}

	id := idgen.GenerateID("vpce-svc-")
	svc := &driver.EndpointService{
		ID:                      id,
		ServiceName:             fmt.Sprintf("com.amazonaws.vpce.%s.%s", m.opts.Region, id),
		State:                   "Available",
		NetworkLoadBalancerARNs: append([]string(nil), cfg.NetworkLoadBalancerARNs...),
		AcceptanceRequired:      cfg.AcceptanceRequired,
		AvailabilityZones:       []string{m.opts.Region + "a"},
		Tags:                    copyTags(cfg.Tags),
	}
	m.endpointServices.Set(id, svc)

	out := cloneEndpointService(svc)

	return &out, nil
}

// DeleteVPCEndpointServiceConfiguration deletes an endpoint service.
func (m *Mock) DeleteVPCEndpointServiceConfiguration(_ context.Context, id string) error {
	if !m.endpointServices.Delete(id) {
		return errors.Newf(errors.NotFound, "endpoint service %q not found", id)
	}

	return nil
}

// DescribeVPCEndpointServiceConfigurations returns endpoint services matching ids.
func (m *Mock) DescribeVPCEndpointServiceConfigurations(_ context.Context, ids []string) ([]driver.EndpointService, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.endpointServices, ids, cloneEndpointService), nil
}

func cloneEndpointService(s *driver.EndpointService) driver.EndpointService {
	out := *s
	out.NetworkLoadBalancerARNs = append([]string(nil), s.NetworkLoadBalancerARNs...)
	out.AvailabilityZones = append([]string(nil), s.AvailabilityZones...)
	out.Tags = copyTags(s.Tags)

	return out
}

// ModifyVPCEndpointServicePermissions adds/removes allowed principals on a service.
func (m *Mock) ModifyVPCEndpointServicePermissions(
	_ context.Context, serviceID string, addPrincipals, removePrincipals []string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.endpointServices.Has(serviceID) {
		return errors.Newf(errors.NotFound, "endpoint service %q not found", serviceID)
	}

	remove := make(map[string]bool, len(removePrincipals))
	for _, p := range removePrincipals {
		remove[p] = true
	}

	current := m.endpointServicePerms[serviceID]
	kept := current[:0:0]

	for _, p := range current {
		if !remove[p] {
			kept = append(kept, p)
		}
	}

	m.endpointServicePerms[serviceID] = append(kept, addPrincipals...)

	return nil
}

// DescribeVPCEndpointServicePermissions returns the allowed principals.
func (m *Mock) DescribeVPCEndpointServicePermissions(_ context.Context, serviceID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.endpointServices.Has(serviceID) {
		return nil, errors.Newf(errors.NotFound, "endpoint service %q not found", serviceID)
	}

	return append([]string(nil), m.endpointServicePerms[serviceID]...), nil
}
