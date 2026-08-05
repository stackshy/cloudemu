package driver

import (
	"context"
	"time"
)

// IpamResourceCidr is a resource (VPC / subnet / public IPv4 pool) CIDR that
// IPAM tracks within a scope, with its compliance and utilization state.
type IpamResourceCidr struct {
	IpamID           string
	IpamScopeID      string
	IpamPoolID       string
	ResourceCIDR     string
	ResourceID       string
	ResourceName     string
	ResourceType     string
	ResourceRegion   string
	ResourceOwnerID  string
	VPCID            string
	AvailabilityZone string
	ComplianceStatus string
	ManagementState  string
	OverlapStatus    string
	IPUsage          float64
	Tags             map[string]string
}

// IpamAddressHistoryRecord is one entry in the history of a CIDR within a scope.
type IpamAddressHistoryRecord struct {
	ResourceCIDR             string
	ResourceID               string
	ResourceName             string
	ResourceType             string
	ResourceRegion           string
	ResourceOwnerID          string
	VPCID                    string
	ResourceComplianceStatus string
	ResourceOverlapStatus    string
	SampledStartTime         time.Time
	SampledEndTime           time.Time
}

// IPAMResources is an OPTIONAL AWS capability exposing IPAM's view of the
// resource CIDRs (VPCs/subnets) it monitors, plus address history.
type IPAMResources interface {
	GetIpamResourceCidrs(ctx context.Context, scopeID, resourceID string) ([]IpamResourceCidr, error)
	ModifyIpamResourceCidr(ctx context.Context, resourceID, currentScopeID, destScopeID string, monitored bool) (*IpamResourceCidr, error)
	GetIpamAddressHistory(ctx context.Context, cidr, scopeID string) ([]IpamAddressHistoryRecord, error)
}
