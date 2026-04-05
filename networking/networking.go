// Package networking provides a portable networking API with cross-cutting concerns.
package networking

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Networking is the portable networking type wrapping a driver.
type Networking struct {
	driver   driver.Networking
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

func NewNetworking(d driver.Networking, opts ...Option) *Networking {
	n := &Networking{driver: d}
	for _, opt := range opts {
		opt(n)
	}

	return n
}

type Option func(*Networking)

func WithRecorder(r *recorder.Recorder) Option     { return func(n *Networking) { n.recorder = r } }
func WithMetrics(m *metrics.Collector) Option      { return func(n *Networking) { n.metrics = m } }
func WithRateLimiter(l *ratelimit.Limiter) Option  { return func(n *Networking) { n.limiter = l } }
func WithErrorInjection(i *inject.Injector) Option { return func(n *Networking) { n.injector = i } }
func WithLatency(d time.Duration) Option           { return func(n *Networking) { n.latency = d } }

func (n *Networking) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if n.injector != nil {
		if err := n.injector.Check("networking", op); err != nil {
			n.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if n.limiter != nil {
		if err := n.limiter.Allow(); err != nil {
			n.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if n.latency > 0 {
		time.Sleep(n.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if n.metrics != nil {
		labels := map[string]string{"service": "networking", "operation": op}
		n.metrics.Counter("calls_total", 1, labels)
		n.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			n.metrics.Counter("errors_total", 1, labels)
		}
	}

	n.record(op, input, out, err, dur)

	return out, err
}

func (n *Networking) record(op string, input, output any, err error, dur time.Duration) {
	if n.recorder != nil {
		n.recorder.Record("networking", op, input, output, err, dur)
	}
}

func (n *Networking) CreateVPC(ctx context.Context, config driver.VPCConfig) (*driver.VPCInfo, error) {
	out, err := n.do(ctx, "CreateVPC", config, func() (any, error) { return n.driver.CreateVPC(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.VPCInfo), nil
}

func (n *Networking) DeleteVPC(ctx context.Context, id string) error {
	_, err := n.do(ctx, "DeleteVPC", id, func() (any, error) { return nil, n.driver.DeleteVPC(ctx, id) })
	return err
}

func (n *Networking) DescribeVPCs(ctx context.Context, ids []string) ([]driver.VPCInfo, error) {
	out, err := n.do(ctx, "DescribeVPCs", ids, func() (any, error) { return n.driver.DescribeVPCs(ctx, ids) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.VPCInfo), nil
}

func (n *Networking) CreateSubnet(ctx context.Context, config driver.SubnetConfig) (*driver.SubnetInfo, error) {
	out, err := n.do(ctx, "CreateSubnet", config, func() (any, error) { return n.driver.CreateSubnet(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.SubnetInfo), nil
}

func (n *Networking) DeleteSubnet(ctx context.Context, id string) error {
	_, err := n.do(ctx, "DeleteSubnet", id, func() (any, error) { return nil, n.driver.DeleteSubnet(ctx, id) })
	return err
}

func (n *Networking) DescribeSubnets(ctx context.Context, ids []string) ([]driver.SubnetInfo, error) {
	out, err := n.do(ctx, "DescribeSubnets", ids, func() (any, error) { return n.driver.DescribeSubnets(ctx, ids) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.SubnetInfo), nil
}

func (n *Networking) CreateSecurityGroup(ctx context.Context, config driver.SecurityGroupConfig) (*driver.SecurityGroupInfo, error) {
	out, err := n.do(ctx, "CreateSecurityGroup", config, func() (any, error) {
		return n.driver.CreateSecurityGroup(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.SecurityGroupInfo), nil
}

func (n *Networking) DeleteSecurityGroup(ctx context.Context, id string) error {
	_, err := n.do(ctx, "DeleteSecurityGroup", id, func() (any, error) {
		return nil, n.driver.DeleteSecurityGroup(ctx, id)
	})

	return err
}

func (n *Networking) DescribeSecurityGroups(ctx context.Context, ids []string) ([]driver.SecurityGroupInfo, error) {
	out, err := n.do(ctx, "DescribeSecurityGroups", ids, func() (any, error) {
		return n.driver.DescribeSecurityGroups(ctx, ids)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.SecurityGroupInfo), nil
}

func (n *Networking) AddIngressRule(ctx context.Context, groupID string, rule driver.SecurityRule) error {
	_, err := n.do(ctx, "AddIngressRule", rule, func() (any, error) {
		return nil, n.driver.AddIngressRule(ctx, groupID, rule)
	})

	return err
}

func (n *Networking) AddEgressRule(ctx context.Context, groupID string, rule driver.SecurityRule) error {
	_, err := n.do(ctx, "AddEgressRule", rule, func() (any, error) {
		return nil, n.driver.AddEgressRule(ctx, groupID, rule)
	})

	return err
}

func (n *Networking) RemoveIngressRule(ctx context.Context, groupID string, rule driver.SecurityRule) error {
	_, err := n.do(ctx, "RemoveIngressRule", rule, func() (any, error) {
		return nil, n.driver.RemoveIngressRule(ctx, groupID, rule)
	})

	return err
}

func (n *Networking) RemoveEgressRule(ctx context.Context, groupID string, rule driver.SecurityRule) error {
	_, err := n.do(ctx, "RemoveEgressRule", rule, func() (any, error) {
		return nil, n.driver.RemoveEgressRule(ctx, groupID, rule)
	})

	return err
}

// CreatePeeringConnection creates a VPC peering connection.
func (n *Networking) CreatePeeringConnection(
	ctx context.Context, config driver.PeeringConfig,
) (*driver.PeeringConnection, error) {
	out, err := n.do(ctx, "CreatePeeringConnection", config, func() (any, error) {
		return n.driver.CreatePeeringConnection(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.PeeringConnection), nil
}

// AcceptPeeringConnection accepts a pending peering connection.
func (n *Networking) AcceptPeeringConnection(ctx context.Context, peeringID string) error {
	_, err := n.do(ctx, "AcceptPeeringConnection", peeringID, func() (any, error) {
		return nil, n.driver.AcceptPeeringConnection(ctx, peeringID)
	})

	return err
}

// RejectPeeringConnection rejects a pending peering connection.
func (n *Networking) RejectPeeringConnection(ctx context.Context, peeringID string) error {
	_, err := n.do(ctx, "RejectPeeringConnection", peeringID, func() (any, error) {
		return nil, n.driver.RejectPeeringConnection(ctx, peeringID)
	})

	return err
}

// DeletePeeringConnection deletes a peering connection.
func (n *Networking) DeletePeeringConnection(ctx context.Context, peeringID string) error {
	_, err := n.do(ctx, "DeletePeeringConnection", peeringID, func() (any, error) {
		return nil, n.driver.DeletePeeringConnection(ctx, peeringID)
	})

	return err
}

// DescribePeeringConnections returns peering connections matching the given IDs.
func (n *Networking) DescribePeeringConnections(
	ctx context.Context, ids []string,
) ([]driver.PeeringConnection, error) {
	out, err := n.do(ctx, "DescribePeeringConnections", ids, func() (any, error) {
		return n.driver.DescribePeeringConnections(ctx, ids)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.PeeringConnection), nil
}

// CreateNATGateway creates a NAT gateway.
func (n *Networking) CreateNATGateway(
	ctx context.Context, config driver.NATGatewayConfig,
) (*driver.NATGateway, error) {
	out, err := n.do(ctx, "CreateNATGateway", config, func() (any, error) {
		return n.driver.CreateNATGateway(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.NATGateway), nil
}

// DeleteNATGateway deletes a NAT gateway.
func (n *Networking) DeleteNATGateway(ctx context.Context, id string) error {
	_, err := n.do(ctx, "DeleteNATGateway", id, func() (any, error) {
		return nil, n.driver.DeleteNATGateway(ctx, id)
	})

	return err
}

// DescribeNATGateways returns NAT gateways matching the given IDs.
func (n *Networking) DescribeNATGateways(
	ctx context.Context, ids []string,
) ([]driver.NATGateway, error) {
	out, err := n.do(ctx, "DescribeNATGateways", ids, func() (any, error) {
		return n.driver.DescribeNATGateways(ctx, ids)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.NATGateway), nil
}

// CreateFlowLog creates a flow log.
func (n *Networking) CreateFlowLog(
	ctx context.Context, config driver.FlowLogConfig,
) (*driver.FlowLog, error) {
	out, err := n.do(ctx, "CreateFlowLog", config, func() (any, error) {
		return n.driver.CreateFlowLog(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.FlowLog), nil
}

// DeleteFlowLog deletes a flow log.
func (n *Networking) DeleteFlowLog(ctx context.Context, id string) error {
	_, err := n.do(ctx, "DeleteFlowLog", id, func() (any, error) {
		return nil, n.driver.DeleteFlowLog(ctx, id)
	})

	return err
}

// DescribeFlowLogs returns flow logs matching the given IDs.
func (n *Networking) DescribeFlowLogs(
	ctx context.Context, ids []string,
) ([]driver.FlowLog, error) {
	out, err := n.do(ctx, "DescribeFlowLogs", ids, func() (any, error) {
		return n.driver.DescribeFlowLogs(ctx, ids)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.FlowLog), nil
}

// GetFlowLogRecords returns flow log records for the specified flow log.
func (n *Networking) GetFlowLogRecords(
	ctx context.Context, flowLogID string, limit int,
) ([]driver.FlowLogRecord, error) {
	out, err := n.do(ctx, "GetFlowLogRecords", flowLogID, func() (any, error) {
		return n.driver.GetFlowLogRecords(ctx, flowLogID, limit)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.FlowLogRecord), nil
}

// CreateRouteTable creates a route table.
func (n *Networking) CreateRouteTable(
	ctx context.Context, config driver.RouteTableConfig,
) (*driver.RouteTable, error) {
	out, err := n.do(ctx, "CreateRouteTable", config, func() (any, error) {
		return n.driver.CreateRouteTable(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.RouteTable), nil
}

// DeleteRouteTable deletes a route table.
func (n *Networking) DeleteRouteTable(ctx context.Context, id string) error {
	_, err := n.do(ctx, "DeleteRouteTable", id, func() (any, error) {
		return nil, n.driver.DeleteRouteTable(ctx, id)
	})

	return err
}

// DescribeRouteTables returns route tables matching the given IDs.
func (n *Networking) DescribeRouteTables(
	ctx context.Context, ids []string,
) ([]driver.RouteTable, error) {
	out, err := n.do(ctx, "DescribeRouteTables", ids, func() (any, error) {
		return n.driver.DescribeRouteTables(ctx, ids)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.RouteTable), nil
}

// CreateRoute adds a route to a route table.
func (n *Networking) CreateRoute(
	ctx context.Context, routeTableID, destinationCIDR, targetID, targetType string,
) error {
	_, err := n.do(ctx, "CreateRoute", routeTableID, func() (any, error) {
		return nil, n.driver.CreateRoute(ctx, routeTableID, destinationCIDR, targetID, targetType)
	})

	return err
}

// DeleteRoute removes a route from a route table.
func (n *Networking) DeleteRoute(ctx context.Context, routeTableID, destinationCIDR string) error {
	_, err := n.do(ctx, "DeleteRoute", routeTableID, func() (any, error) {
		return nil, n.driver.DeleteRoute(ctx, routeTableID, destinationCIDR)
	})

	return err
}

// CreateNetworkACL creates a network ACL.
func (n *Networking) CreateNetworkACL(
	ctx context.Context, vpcID string, tags map[string]string,
) (*driver.NetworkACL, error) {
	out, err := n.do(ctx, "CreateNetworkACL", vpcID, func() (any, error) {
		return n.driver.CreateNetworkACL(ctx, vpcID, tags)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.NetworkACL), nil
}

// DeleteNetworkACL deletes a network ACL.
func (n *Networking) DeleteNetworkACL(ctx context.Context, id string) error {
	_, err := n.do(ctx, "DeleteNetworkACL", id, func() (any, error) {
		return nil, n.driver.DeleteNetworkACL(ctx, id)
	})

	return err
}

// DescribeNetworkACLs returns network ACLs matching the given IDs.
func (n *Networking) DescribeNetworkACLs(
	ctx context.Context, ids []string,
) ([]driver.NetworkACL, error) {
	out, err := n.do(ctx, "DescribeNetworkACLs", ids, func() (any, error) {
		return n.driver.DescribeNetworkACLs(ctx, ids)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.NetworkACL), nil
}

// AddNetworkACLRule adds a rule to a network ACL.
func (n *Networking) AddNetworkACLRule(
	ctx context.Context, aclID string, rule *driver.NetworkACLRule,
) error {
	_, err := n.do(ctx, "AddNetworkACLRule", aclID, func() (any, error) {
		return nil, n.driver.AddNetworkACLRule(ctx, aclID, rule)
	})

	return err
}

// RemoveNetworkACLRule removes a rule from a network ACL.
func (n *Networking) RemoveNetworkACLRule(
	ctx context.Context, aclID string, ruleNumber int, egress bool,
) error {
	_, err := n.do(ctx, "RemoveNetworkACLRule", aclID, func() (any, error) {
		return nil, n.driver.RemoveNetworkACLRule(ctx, aclID, ruleNumber, egress)
	})

	return err
}

// CreateInternetGateway creates an internet gateway.
func (n *Networking) CreateInternetGateway(
	ctx context.Context, cfg driver.InternetGatewayConfig,
) (*driver.InternetGateway, error) {
	out, err := n.do(ctx, "CreateInternetGateway", cfg, func() (any, error) {
		return n.driver.CreateInternetGateway(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.InternetGateway), nil
}

// DeleteInternetGateway deletes an internet gateway.
func (n *Networking) DeleteInternetGateway(
	ctx context.Context, id string,
) error {
	_, err := n.do(ctx, "DeleteInternetGateway", id, func() (any, error) {
		return nil, n.driver.DeleteInternetGateway(ctx, id)
	})

	return err
}

// DescribeInternetGateways returns internet gateways
// matching the given IDs.
func (n *Networking) DescribeInternetGateways(
	ctx context.Context, ids []string,
) ([]driver.InternetGateway, error) {
	out, err := n.do(ctx, "DescribeInternetGateways", ids, func() (any, error) {
		return n.driver.DescribeInternetGateways(ctx, ids)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.InternetGateway), nil
}

// AttachInternetGateway attaches an internet gateway to a VPC.
func (n *Networking) AttachInternetGateway(
	ctx context.Context, igwID, vpcID string,
) error {
	_, err := n.do(ctx, "AttachInternetGateway", igwID, func() (any, error) {
		return nil, n.driver.AttachInternetGateway(ctx, igwID, vpcID)
	})

	return err
}

// DetachInternetGateway detaches an internet gateway from a VPC.
func (n *Networking) DetachInternetGateway(
	ctx context.Context, igwID, vpcID string,
) error {
	_, err := n.do(ctx, "DetachInternetGateway", igwID, func() (any, error) {
		return nil, n.driver.DetachInternetGateway(ctx, igwID, vpcID)
	})

	return err
}

// AllocateAddress allocates a new elastic IP address.
func (n *Networking) AllocateAddress(
	ctx context.Context, cfg driver.ElasticIPConfig,
) (*driver.ElasticIP, error) {
	out, err := n.do(ctx, "AllocateAddress", cfg, func() (any, error) {
		return n.driver.AllocateAddress(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ElasticIP), nil
}

// ReleaseAddress releases an elastic IP address.
func (n *Networking) ReleaseAddress(
	ctx context.Context, allocationID string,
) error {
	_, err := n.do(ctx, "ReleaseAddress", allocationID, func() (any, error) {
		return nil, n.driver.ReleaseAddress(ctx, allocationID)
	})

	return err
}

// DescribeAddresses returns elastic IPs matching the given IDs.
func (n *Networking) DescribeAddresses(
	ctx context.Context, ids []string,
) ([]driver.ElasticIP, error) {
	out, err := n.do(ctx, "DescribeAddresses", ids, func() (any, error) {
		return n.driver.DescribeAddresses(ctx, ids)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.ElasticIP), nil
}

// AssociateAddress associates an elastic IP with an instance.
func (n *Networking) AssociateAddress(
	ctx context.Context, allocationID, instanceID string,
) (string, error) {
	out, err := n.do(ctx, "AssociateAddress", allocationID, func() (any, error) {
		return n.driver.AssociateAddress(ctx, allocationID, instanceID)
	})
	if err != nil {
		return "", err
	}

	return out.(string), nil
}

// DisassociateAddress removes an elastic IP association.
func (n *Networking) DisassociateAddress(
	ctx context.Context, associationID string,
) error {
	_, err := n.do(ctx, "DisassociateAddress", associationID, func() (any, error) {
		return nil, n.driver.DisassociateAddress(ctx, associationID)
	})

	return err
}

// AssociateRouteTable associates a route table with a subnet.
func (n *Networking) AssociateRouteTable(
	ctx context.Context, routeTableID, subnetID string,
) (*driver.RouteTableAssociation, error) {
	out, err := n.do(ctx, "AssociateRouteTable", routeTableID, func() (any, error) {
		return n.driver.AssociateRouteTable(ctx, routeTableID, subnetID)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.RouteTableAssociation), nil
}

// DisassociateRouteTable removes a route table association.
func (n *Networking) DisassociateRouteTable(
	ctx context.Context, associationID string,
) error {
	_, err := n.do(ctx, "DisassociateRouteTable", associationID, func() (any, error) {
		return nil, n.driver.DisassociateRouteTable(ctx, associationID)
	})

	return err
}
