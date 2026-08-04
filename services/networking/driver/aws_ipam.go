package driver

import "context"

// ---- AWS IPAM (IP Address Manager) — OPTIONAL capability (type-asserted) ----
//
// IPAM is an AWS-only VPC feature exposed on the EC2 query API. Like the other
// AWS networking specifics it is an optional capability discovered via a type
// assertion on the vpc driver, so the portable Networking interface stays clean.

// Ipam is the top-level IP Address Manager. Creating one implicitly creates a
// public and a private default scope.
type Ipam struct {
	ID                                    string
	ARN                                   string
	Region                                string
	PublicDefaultScopeID                  string
	PrivateDefaultScopeID                 string
	ScopeCount                            int
	DefaultResourceDiscoveryID            string
	DefaultResourceDiscoveryAssociationID string
	ResourceDiscoveryAssociationCount     int
	OperatingRegions                      []string
	Description                           string
	Tier                                  string
	State                                 string
	Tags                                  map[string]string
}

// IpamConfig is the input to CreateIpam.
type IpamConfig struct {
	Description      string
	Tier             string
	OperatingRegions []string
	Tags             map[string]string
}

// IpamScope groups pools. Each IPAM has a public and a private default scope;
// additional private scopes may be created.
type IpamScope struct {
	ID          string
	ARN         string
	IpamARN     string
	ScopeType   string // public | private
	IsDefault   bool
	PoolCount   int
	Description string
	State       string
	Tags        map[string]string
}

// IpamScopeConfig is the input to CreateIpamScope.
type IpamScopeConfig struct {
	IpamID      string
	Description string
	Tags        map[string]string
}

// IpamPool is a CIDR pool within a scope.
type IpamPool struct {
	ID                             string
	ARN                            string
	IpamScopeARN                   string
	IpamScopeType                  string
	AddressFamily                  string // ipv4 | ipv6
	Locale                         string
	PoolDepth                      int
	Description                    string
	State                          string
	AllocationMinNetmaskLength     int
	AllocationMaxNetmaskLength     int
	AllocationDefaultNetmaskLength int
	Tags                           map[string]string
}

// IpamPoolConfig is the input to CreateIpamPool.
type IpamPoolConfig struct {
	IpamScopeID                    string
	AddressFamily                  string
	Locale                         string
	Description                    string
	AllocationMinNetmaskLength     int
	AllocationMaxNetmaskLength     int
	AllocationDefaultNetmaskLength int
	Tags                           map[string]string
}

// IpamPoolCidr is a CIDR provisioned into a pool (the pool's supply).
type IpamPoolCidr struct {
	ID            string
	CIDR          string
	NetmaskLength int
	State         string
}

// IpamPoolAllocation is a CIDR handed out from a pool (the pool's usage).
type IpamPoolAllocation struct {
	ID           string
	CIDR         string
	ResourceType string
	ResourceID   string
	Description  string
	Tags         map[string]string
}

// AllocateIpamPoolCidrConfig is the input to AllocateIpamPoolCidr.
type AllocateIpamPoolCidrConfig struct {
	IpamPoolID    string
	CIDR          string
	NetmaskLength int
	Description   string
	Tags          map[string]string
}

// IPAM is an OPTIONAL AWS capability (type-asserted on the vpc driver).
//
//nolint:interfacebloat // mirrors the IPAM core-lifecycle API surface.
type IPAM interface {
	CreateIpam(ctx context.Context, cfg IpamConfig) (*Ipam, error)
	DescribeIpams(ctx context.Context, ids []string) ([]Ipam, error)
	ModifyIpam(ctx context.Context, id, description string) (*Ipam, error)
	DeleteIpam(ctx context.Context, id string) (*Ipam, error)

	CreateIpamScope(ctx context.Context, cfg IpamScopeConfig) (*IpamScope, error)
	DescribeIpamScopes(ctx context.Context, ids []string) ([]IpamScope, error)
	ModifyIpamScope(ctx context.Context, id, description string) (*IpamScope, error)
	DeleteIpamScope(ctx context.Context, id string) (*IpamScope, error)

	CreateIpamPool(ctx context.Context, cfg IpamPoolConfig) (*IpamPool, error)
	DescribeIpamPools(ctx context.Context, ids []string) ([]IpamPool, error)
	ModifyIpamPool(ctx context.Context, id, description string) (*IpamPool, error)
	DeleteIpamPool(ctx context.Context, id string) (*IpamPool, error)

	ProvisionIpamPoolCidr(ctx context.Context, poolID, cidr string, netmaskLength int) (*IpamPoolCidr, error)
	DeprovisionIpamPoolCidr(ctx context.Context, poolID, cidr string) (*IpamPoolCidr, error)
	GetIpamPoolCidrs(ctx context.Context, poolID string) ([]IpamPoolCidr, error)

	AllocateIpamPoolCidr(ctx context.Context, cfg AllocateIpamPoolCidrConfig) (*IpamPoolAllocation, error)
	ReleaseIpamPoolAllocation(ctx context.Context, poolID, allocationID string) error
	GetIpamPoolAllocations(ctx context.Context, poolID string) ([]IpamPoolAllocation, error)
	ModifyIpamPoolAllocation(ctx context.Context, allocationID, description string) (*IpamPoolAllocation, error)
}
