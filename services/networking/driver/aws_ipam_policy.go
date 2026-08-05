package driver

import "context"

// IpamAllocationRule maps a resource type to a source IPAM pool. Mirrors the
// EC2 IpamPolicyAllocationRule, whose only field is the source pool id.
type IpamAllocationRule struct {
	SourceIpamPoolID string
}

// IpamPolicy is an IPAM allocation policy (Organizations-wide governance).
type IpamPolicy struct {
	ID           string
	ARN          string
	IpamID       string
	IpamRegion   string
	OwnerID      string
	State        string
	StateMessage string
	Enabled      bool
	// Locale and ResourceType scope the allocation-rule document; set by
	// ModifyIpamPolicyAllocationRules and echoed on the policy document.
	Locale          string
	ResourceType    string
	AllocationRules []IpamAllocationRule
	Tags            map[string]string
}

// IPAMPolicy is an OPTIONAL AWS capability for IPAM policies and the
// Organizations delegated-admin account.
//
//nolint:interfacebloat // mirrors the IPAM policy + org-admin API surface.
type IPAMPolicy interface {
	CreateIpamPolicy(ctx context.Context, ipamID string, tags map[string]string) (*IpamPolicy, error)
	DeleteIpamPolicy(ctx context.Context, id string) (*IpamPolicy, error)
	DescribeIpamPolicies(ctx context.Context, ids []string) ([]IpamPolicy, error)
	EnableIpamPolicy(ctx context.Context, id, organizationTargetID string) (string, error)
	DisableIpamPolicy(ctx context.Context, id string) error
	GetEnabledIpamPolicy(ctx context.Context) (policyID string, enabled bool, managedBy string, err error)
	ModifyIpamPolicyAllocationRules(ctx context.Context, id, locale, resourceType string, rules []IpamAllocationRule) (*IpamPolicy, error)
	GetIpamPolicyAllocationRules(ctx context.Context, id string) (*IpamPolicy, error)
	GetIpamPolicyOrganizationTargets(ctx context.Context, id string) ([]string, error)

	EnableIpamOrganizationAdminAccount(ctx context.Context, accountID string) (bool, error)
	DisableIpamOrganizationAdminAccount(ctx context.Context, accountID string) (bool, error)
}
