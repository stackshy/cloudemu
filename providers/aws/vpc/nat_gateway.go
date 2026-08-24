package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// NAT gateway state and connectivity constants.
const (
	NATStateAvailable = "available"
	NATStateDeleted   = "deleted"

	natConnectivityPublic  = "public"
	natConnectivityPrivate = "private"
)

type natGatewayData struct {
	ID                 string
	SubnetID           string
	VPCID              string
	PublicIP           string
	PrivateIP          string
	AllocationID       string
	NetworkInterfaceID string
	ConnectivityType   string
	State              string
	CreatedAt          string
	Tags               map[string]string
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

	// A real NAT gateway occupies an ENI in its subnet for as long as it
	// lives, and that ENI is what refuses a VPC delete issued too early.
	eni := m.attachManagedENI(subnet.VPCID, cfg.SubnetID, natENIDescription(id))

	connectivity := natConnectivityPublic
	if cfg.ConnectivityType == natConnectivityPrivate {
		connectivity = natConnectivityPrivate
	}

	nat := &natGatewayData{
		ID:                 id,
		SubnetID:           cfg.SubnetID,
		VPCID:              subnet.VPCID,
		PrivateIP:          mockPublicIP(id + "-private"),
		AllocationID:       cfg.AllocationID,
		NetworkInterfaceID: eni.ID,
		ConnectivityType:   connectivity,
		State:              NATStateAvailable,
		CreatedAt:          m.opts.Clock.Now().Format(timeFormat),
		Tags:               copyTags(cfg.Tags),
	}
	// A private NAT gateway has no public/Elastic IP address.
	if connectivity == natConnectivityPublic {
		nat.PublicIP = mockPublicIP(id)

		// The NAT gateway holds the Elastic IP, so real EC2 refuses to release it
		// until the NAT gateway is deleted (InvalidIPAddress.InUse).
		if cfg.AllocationID != "" {
			if eip, ok := m.eips.Get(cfg.AllocationID); ok {
				eip.AssociationID = idgen.GenerateID("eipassoc-")
			}
		}
	}

	m.natGateways.Set(id, nat)

	info := toNATGatewayInfo(nat)

	return &info, nil
}

// DeleteNATGateway deletes the NAT gateway with the given ID.
func (m *Mock) DeleteNATGateway(_ context.Context, id string) error {
	nat, ok := m.natGateways.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "NAT gateway %q not found", id)
	}

	m.natGateways.Delete(id)
	m.releaseManagedENIs(natENIDescription(id))

	// Releasing the NAT gateway frees its Elastic IP so it can be released.
	if nat.AllocationID != "" {
		if eip, ok := m.eips.Get(nat.AllocationID); ok {
			eip.AssociationID = ""
		}
	}

	return nil
}

func natENIDescription(natID string) string {
	return "Interface for NAT Gateway " + natID
}

// DescribeNATGateways returns NAT gateways matching the given IDs, or all if empty.
func (m *Mock) DescribeNATGateways(_ context.Context, ids []string) ([]driver.NATGateway, error) {
	return describeResources(m.natGateways, ids, toNATGatewayInfo), nil
}

func toNATGatewayInfo(n *natGatewayData) driver.NATGateway {
	return driver.NATGateway{
		ID:                 n.ID,
		SubnetID:           n.SubnetID,
		VPCID:              n.VPCID,
		PublicIP:           n.PublicIP,
		PrivateIP:          n.PrivateIP,
		AllocationID:       n.AllocationID,
		NetworkInterfaceID: n.NetworkInterfaceID,
		ConnectivityType:   n.ConnectivityType,
		State:              n.State,
		CreatedAt:          n.CreatedAt,
		Tags:               copyTags(n.Tags),
	}
}
