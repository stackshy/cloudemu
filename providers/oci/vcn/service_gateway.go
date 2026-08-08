package vcn

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// EndpointTypeGateway is the endpoint kind an OCI service gateway maps to:
// traffic is routed to the Oracle Services Network, never through an ENI.
const EndpointTypeGateway = "Gateway"

// CreateVPCEndpoint creates a service gateway. cfg.ServiceName carries the
// OCID of the service the gateway fronts, which is what OCI identifies it by.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateVPCEndpoint(_ context.Context, cfg driver.VPCEndpointConfig) (*driver.VPCEndpoint, error) {
	if cfg.VPCID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VCN OCID is required")
	}

	if cfg.ServiceName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "service is required")
	}

	if !m.vcns.Has(cfg.VPCID) {
		return nil, cerrors.Newf(cerrors.NotFound, "VCN %q not found", cfg.VPCID)
	}

	id := m.newOCID(typeServiceGateway)
	endpointType := cfg.EndpointType

	if endpointType == "" {
		endpointType = EndpointTypeGateway
	}

	sg := &serviceGatewayData{
		ID:            id,
		VCNID:         cfg.VPCID,
		ServiceName:   cfg.ServiceName,
		EndpointType:  endpointType,
		State:         StateAvailable,
		RouteTableIDs: copyStringSlice(cfg.RouteTableIDs),
		Tags:          copyTags(cfg.Tags),
		CreatedAt:     m.now(),
	}

	m.serviceGWs.Set(id, sg)
	m.record(id)

	return toServiceGatewayInfoPtr(sg), nil
}

type serviceGatewayData struct {
	ID            string
	VCNID         string
	ServiceName   string
	EndpointType  string
	State         string
	RouteTableIDs []string
	Tags          map[string]string
	CreatedAt     string
}

// DeleteVPCEndpoint deletes a service gateway.
func (m *Mock) DeleteVPCEndpoint(_ context.Context, id string) error {
	if !m.serviceGWs.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "service gateway %q not found", id)
	}

	m.forget(id)

	return nil
}

// DescribeVPCEndpoints returns service gateways matching the given OCIDs, or
// all if empty.
func (m *Mock) DescribeVPCEndpoints(_ context.Context, ids []string) ([]driver.VPCEndpoint, error) {
	return describeResources(m.serviceGWs, ids, toServiceGatewayInfo), nil
}

// ModifyVPCEndpoint updates a service gateway's services, route table and tags.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) ModifyVPCEndpoint(_ context.Context, id string, cfg driver.VPCEndpointConfig) (*driver.VPCEndpoint, error) {
	sg, ok := m.serviceGWs.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "service gateway %q not found", id)
	}

	if cfg.ServiceName != "" {
		sg.ServiceName = cfg.ServiceName
	}

	if len(cfg.RouteTableIDs) > 0 {
		sg.RouteTableIDs = copyStringSlice(cfg.RouteTableIDs)
	}

	if len(cfg.Tags) > 0 {
		sg.Tags = copyTags(cfg.Tags)
	}

	return toServiceGatewayInfoPtr(sg), nil
}

func toServiceGatewayInfo(sg *serviceGatewayData) driver.VPCEndpoint {
	return driver.VPCEndpoint{
		ID:            sg.ID,
		VPCID:         sg.VCNID,
		ServiceName:   sg.ServiceName,
		EndpointType:  sg.EndpointType,
		State:         sg.State,
		RouteTableIDs: copyStringSlice(sg.RouteTableIDs),
		Tags:          copyTags(sg.Tags),
		CreatedAt:     sg.CreatedAt,
	}
}

func toServiceGatewayInfoPtr(sg *serviceGatewayData) *driver.VPCEndpoint {
	info := toServiceGatewayInfo(sg)

	return &info
}
