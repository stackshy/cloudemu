package driver

import (
	"context"
	"time"
)

// IpamResourceDiscovery is IPAM's mechanism for finding resources across
// accounts/regions. Each IPAM gets a default one on creation.
type IpamResourceDiscovery struct {
	ID               string
	ARN              string
	Region           string
	OwnerID          string
	OperatingRegions []string
	Description      string
	State            string
	IsDefault        bool
	Tags             map[string]string
}

// IpamResourceDiscoveryConfig is the input to CreateIpamResourceDiscovery.
type IpamResourceDiscoveryConfig struct {
	Description      string
	OperatingRegions []string
	Tags             map[string]string
}

// IpamResourceDiscoveryAssociation links a resource discovery to an IPAM.
type IpamResourceDiscoveryAssociation struct {
	ID                      string
	ARN                     string
	IpamID                  string
	IpamARN                 string
	IpamRegion              string
	ResourceDiscoveryID     string
	OwnerID                 string
	State                   string
	IsDefault               bool
	ResourceDiscoveryStatus string
	Tags                    map[string]string
}

// IpamDiscoveredAccount is an account IPAM monitors via a resource discovery.
type IpamDiscoveredAccount struct {
	AccountID                   string
	DiscoveryRegion             string
	LastAttemptedDiscoveryTime  time.Time
	LastSuccessfulDiscoveryTime time.Time
}

// IpamDiscoveredResourceCidr is a CIDR IPAM discovered on a monitored resource.
type IpamDiscoveredResourceCidr struct {
	ResourceDiscoveryID              string
	ResourceCIDR                     string
	ResourceID                       string
	ResourceType                     string
	ResourceRegion                   string
	ResourceOwnerID                  string
	VPCID                            string
	SubnetID                         string
	AvailabilityZone                 string
	IPSource                         string
	NetworkInterfaceAttachmentStatus string
	IPUsage                          float64
	SampleTime                       time.Time
	Tags                             map[string]string
}

// IpamDiscoveredPublicAddress is a public IP IPAM discovered.
type IpamDiscoveredPublicAddress struct {
	ResourceDiscoveryID string
	Address             string
	AddressAllocationID string
	AddressOwnerID      string
	AddressRegion       string
	AddressType         string
	AssociationStatus   string
	Service             string
	VPCID               string
	SubnetID            string
	SampleTime          time.Time
}

// IPAMDiscovery is an OPTIONAL AWS capability for IPAM resource discovery.
//
//nolint:interfacebloat // mirrors the IPAM resource-discovery API surface.
type IPAMDiscovery interface {
	CreateIpamResourceDiscovery(ctx context.Context, cfg IpamResourceDiscoveryConfig) (*IpamResourceDiscovery, error)
	DescribeIpamResourceDiscoveries(ctx context.Context, ids []string) ([]IpamResourceDiscovery, error)
	ModifyIpamResourceDiscovery(ctx context.Context, id, description string, operatingRegions []string) (*IpamResourceDiscovery, error)
	DeleteIpamResourceDiscovery(ctx context.Context, id string) (*IpamResourceDiscovery, error)

	AssociateIpamResourceDiscovery(
		ctx context.Context, ipamID, resourceDiscoveryID string, tags map[string]string,
	) (*IpamResourceDiscoveryAssociation, error)
	DisassociateIpamResourceDiscovery(ctx context.Context, associationID string) (*IpamResourceDiscoveryAssociation, error)
	DescribeIpamResourceDiscoveryAssociations(ctx context.Context, ids []string) ([]IpamResourceDiscoveryAssociation, error)

	GetIpamDiscoveredAccounts(ctx context.Context, resourceDiscoveryID, region string) ([]IpamDiscoveredAccount, error)
	GetIpamDiscoveredResourceCidrs(ctx context.Context, resourceDiscoveryID, region string) ([]IpamDiscoveredResourceCidr, error)
	GetIpamDiscoveredPublicAddresses(ctx context.Context, resourceDiscoveryID, region string) ([]IpamDiscoveredPublicAddress, error)
}
