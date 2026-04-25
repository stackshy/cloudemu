package chaos

import (
	"context"

	netdriver "github.com/stackshy/cloudemu/networking/driver"
)

// chaosNetworking wraps a networking driver. Hot-path: VPC/Subnet/SG CRUD +
// SG ingress/egress. Peering, NAT, flow logs, route tables, ACLs, IGW, EIP
// and endpoints delegate through (admin/topology ops).
type chaosNetworking struct {
	netdriver.Networking
	engine *Engine
}

// WrapNetworking returns a networking driver that consults engine on VPC/
// Subnet/SecurityGroup data-plane and rule-mutation calls.
func WrapNetworking(inner netdriver.Networking, engine *Engine) netdriver.Networking {
	return &chaosNetworking{Networking: inner, engine: engine}
}

func (c *chaosNetworking) CreateVPC(
	ctx context.Context, cfg netdriver.VPCConfig,
) (*netdriver.VPCInfo, error) {
	if err := applyChaos(ctx, c.engine, "networking", "CreateVPC"); err != nil {
		return nil, err
	}

	return c.Networking.CreateVPC(ctx, cfg)
}

func (c *chaosNetworking) DeleteVPC(ctx context.Context, id string) error {
	if err := applyChaos(ctx, c.engine, "networking", "DeleteVPC"); err != nil {
		return err
	}

	return c.Networking.DeleteVPC(ctx, id)
}

func (c *chaosNetworking) DescribeVPCs(ctx context.Context, ids []string) ([]netdriver.VPCInfo, error) {
	if err := applyChaos(ctx, c.engine, "networking", "DescribeVPCs"); err != nil {
		return nil, err
	}

	return c.Networking.DescribeVPCs(ctx, ids)
}

func (c *chaosNetworking) CreateSubnet(
	ctx context.Context, cfg netdriver.SubnetConfig,
) (*netdriver.SubnetInfo, error) {
	if err := applyChaos(ctx, c.engine, "networking", "CreateSubnet"); err != nil {
		return nil, err
	}

	return c.Networking.CreateSubnet(ctx, cfg)
}

func (c *chaosNetworking) DeleteSubnet(ctx context.Context, id string) error {
	if err := applyChaos(ctx, c.engine, "networking", "DeleteSubnet"); err != nil {
		return err
	}

	return c.Networking.DeleteSubnet(ctx, id)
}

func (c *chaosNetworking) DescribeSubnets(ctx context.Context, ids []string) ([]netdriver.SubnetInfo, error) {
	if err := applyChaos(ctx, c.engine, "networking", "DescribeSubnets"); err != nil {
		return nil, err
	}

	return c.Networking.DescribeSubnets(ctx, ids)
}

func (c *chaosNetworking) CreateSecurityGroup(
	ctx context.Context, cfg netdriver.SecurityGroupConfig,
) (*netdriver.SecurityGroupInfo, error) {
	if err := applyChaos(ctx, c.engine, "networking", "CreateSecurityGroup"); err != nil {
		return nil, err
	}

	return c.Networking.CreateSecurityGroup(ctx, cfg)
}

func (c *chaosNetworking) DeleteSecurityGroup(ctx context.Context, id string) error {
	if err := applyChaos(ctx, c.engine, "networking", "DeleteSecurityGroup"); err != nil {
		return err
	}

	return c.Networking.DeleteSecurityGroup(ctx, id)
}

func (c *chaosNetworking) DescribeSecurityGroups(ctx context.Context, ids []string) ([]netdriver.SecurityGroupInfo, error) {
	if err := applyChaos(ctx, c.engine, "networking", "DescribeSecurityGroups"); err != nil {
		return nil, err
	}

	return c.Networking.DescribeSecurityGroups(ctx, ids)
}

func (c *chaosNetworking) AddIngressRule(ctx context.Context, groupID string, rule netdriver.SecurityRule) error {
	if err := applyChaos(ctx, c.engine, "networking", "AddIngressRule"); err != nil {
		return err
	}

	return c.Networking.AddIngressRule(ctx, groupID, rule)
}

func (c *chaosNetworking) AddEgressRule(ctx context.Context, groupID string, rule netdriver.SecurityRule) error {
	if err := applyChaos(ctx, c.engine, "networking", "AddEgressRule"); err != nil {
		return err
	}

	return c.Networking.AddEgressRule(ctx, groupID, rule)
}
