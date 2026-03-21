package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/networking/driver"
)

// NAT gateway state constants.
const (
	NATStateAvailable = "available"
	NATStateDeleted   = "deleted"
)

type natGatewayData struct {
	ID        string
	SubnetID  string
	VPCID     string
	PublicIP  string
	State     string
	CreatedAt string
	Tags      map[string]string
}

// CreateNATGateway creates a NAT gateway in the specified subnet.
func (m *Mock) CreateNATGateway(_ context.Context, cfg driver.NATGatewayConfig) (*driver.NATGateway, error) {
	if cfg.SubnetID == "" {
		return nil, errors.New(errors.InvalidArgument, "subnet ID is required")
	}

	subnet, ok := m.subnets.Get(cfg.SubnetID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "subnet %q not found", cfg.SubnetID)
	}

	id := idgen.GenerateID("nat-")
	nat := &natGatewayData{
		ID:        id,
		SubnetID:  cfg.SubnetID,
		VPCID:     subnet.VPCID,
		PublicIP:  mockPublicIP(id),
		State:     NATStateAvailable,
		CreatedAt: m.opts.Clock.Now().Format(timeFormat),
		Tags:      copyTags(cfg.Tags),
	}
	m.natGateways.Set(id, nat)

	info := toNATGatewayInfo(nat)

	return &info, nil
}

// DeleteNATGateway deletes the NAT gateway with the given ID.
func (m *Mock) DeleteNATGateway(_ context.Context, id string) error {
	if !m.natGateways.Delete(id) {
		return errors.Newf(errors.NotFound, "NAT gateway %q not found", id)
	}

	return nil
}

// DescribeNATGateways returns NAT gateways matching the given IDs, or all if empty.
func (m *Mock) DescribeNATGateways(_ context.Context, ids []string) ([]driver.NATGateway, error) {
	return describeResources(m.natGateways, ids, toNATGatewayInfo), nil
}

func toNATGatewayInfo(n *natGatewayData) driver.NATGateway {
	return driver.NATGateway{
		ID:        n.ID,
		SubnetID:  n.SubnetID,
		VPCID:     n.VPCID,
		PublicIP:  n.PublicIP,
		State:     n.State,
		CreatedAt: n.CreatedAt,
		Tags:      copyTags(n.Tags),
	}
}
