package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// CreateEgressOnlyInternetGateway creates an egress-only internet gateway
// (outbound-only IPv6) attached to a VPC.
func (m *Mock) CreateEgressOnlyInternetGateway(
	_ context.Context, vpcID string, tags map[string]string,
) (*driver.EgressOnlyInternetGateway, error) {
	if !m.vpcs.Has(vpcID) {
		return nil, errors.Newf(errors.InvalidArgument, "vpc %q not found", vpcID)
	}

	eigw := &driver.EgressOnlyInternetGateway{
		ID:            idgen.GenerateID("eigw-"),
		AttachedVPCID: vpcID,
		State:         "attached",
		Tags:          copyTags(tags),
	}
	m.egressOnlyIGWs.Set(eigw.ID, eigw)

	out := cloneEgressOnlyIGW(eigw)

	return &out, nil
}

// DeleteEgressOnlyInternetGateway deletes an egress-only internet gateway.
func (m *Mock) DeleteEgressOnlyInternetGateway(_ context.Context, id string) error {
	if !m.egressOnlyIGWs.Delete(id) {
		return errors.Newf(errors.NotFound, "egress-only internet gateway %q not found", id)
	}

	return nil
}

// DescribeEgressOnlyInternetGateways returns egress-only IGWs matching ids.
func (m *Mock) DescribeEgressOnlyInternetGateways(_ context.Context, ids []string) ([]driver.EgressOnlyInternetGateway, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.egressOnlyIGWs, ids, cloneEgressOnlyIGW), nil
}

func cloneEgressOnlyIGW(e *driver.EgressOnlyInternetGateway) driver.EgressOnlyInternetGateway {
	out := *e
	out.Tags = copyTags(e.Tags)

	return out
}
