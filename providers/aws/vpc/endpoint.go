package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/networking/driver"
)

// copyStringSlice creates a shallow copy of a string slice.
func copyStringSlice(src []string) []string {
	if src == nil {
		return nil
	}

	out := make([]string, len(src))
	copy(out, src)

	return out
}

// CreateVPCEndpoint creates a new VPC endpoint.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateVPCEndpoint(
	_ context.Context, cfg driver.VPCEndpointConfig,
) (*driver.VPCEndpoint, error) {
	if cfg.VPCID == "" {
		return nil, errors.Newf(
			errors.InvalidArgument, "VPC ID is required",
		)
	}

	if cfg.ServiceName == "" {
		return nil, errors.Newf(
			errors.InvalidArgument, "service name is required",
		)
	}

	if !m.vpcs.Has(cfg.VPCID) {
		return nil, errors.Newf(
			errors.NotFound, "vpc %q not found", cfg.VPCID,
		)
	}

	id := idgen.GenerateID("vpce-")

	ep := &driver.VPCEndpoint{
		ID:               id,
		VPCID:            cfg.VPCID,
		ServiceName:      cfg.ServiceName,
		EndpointType:     cfg.EndpointType,
		State:            "available",
		SubnetIDs:        copyStringSlice(cfg.SubnetIDs),
		SecurityGroupIDs: copyStringSlice(cfg.SecurityGroupIDs),
		RouteTableIDs:    copyStringSlice(cfg.RouteTableIDs),
		Tags:             copyTags(cfg.Tags),
		CreatedAt:        m.opts.Clock.Now().Format(timeFormat),
	}
	m.endpoints.Set(id, ep)

	return copyEndpoint(ep), nil
}

// DeleteVPCEndpoint deletes the VPC endpoint with the given ID.
func (m *Mock) DeleteVPCEndpoint(
	_ context.Context, id string,
) error {
	if !m.endpoints.Delete(id) {
		return errors.Newf(
			errors.NotFound,
			"vpc endpoint %q not found", id,
		)
	}

	return nil
}

// DescribeVPCEndpoints returns VPC endpoints matching the
// given IDs, or all endpoints if ids is empty.
func (m *Mock) DescribeVPCEndpoints(
	_ context.Context, ids []string,
) ([]driver.VPCEndpoint, error) {
	return describeResources(
		m.endpoints, ids, toEndpointInfo,
	), nil
}

// ModifyVPCEndpoint updates a VPC endpoint configuration.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) ModifyVPCEndpoint(
	_ context.Context, id string, cfg driver.VPCEndpointConfig,
) (*driver.VPCEndpoint, error) {
	ep, ok := m.endpoints.Get(id)
	if !ok {
		return nil, errors.Newf(
			errors.NotFound,
			"vpc endpoint %q not found", id,
		)
	}

	if len(cfg.SubnetIDs) > 0 {
		ep.SubnetIDs = copyStringSlice(cfg.SubnetIDs)
	}

	if len(cfg.SecurityGroupIDs) > 0 {
		ep.SecurityGroupIDs = copyStringSlice(cfg.SecurityGroupIDs)
	}

	if len(cfg.RouteTableIDs) > 0 {
		ep.RouteTableIDs = copyStringSlice(cfg.RouteTableIDs)
	}

	if len(cfg.Tags) > 0 {
		ep.Tags = copyTags(cfg.Tags)
	}

	return copyEndpoint(ep), nil
}

func toEndpointInfo(ep *driver.VPCEndpoint) driver.VPCEndpoint {
	return *copyEndpoint(ep)
}

func copyEndpoint(ep *driver.VPCEndpoint) *driver.VPCEndpoint {
	return &driver.VPCEndpoint{
		ID:               ep.ID,
		VPCID:            ep.VPCID,
		ServiceName:      ep.ServiceName,
		EndpointType:     ep.EndpointType,
		State:            ep.State,
		SubnetIDs:        copyStringSlice(ep.SubnetIDs),
		SecurityGroupIDs: copyStringSlice(ep.SecurityGroupIDs),
		RouteTableIDs:    copyStringSlice(ep.RouteTableIDs),
		Tags:             copyTags(ep.Tags),
		CreatedAt:        ep.CreatedAt,
	}
}
