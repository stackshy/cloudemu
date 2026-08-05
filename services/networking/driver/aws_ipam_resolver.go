package driver

import (
	"context"
	"time"
)

// IpamPrefixListResolver auto-syncs IPAM CIDRs into managed prefix lists.
type IpamPrefixListResolver struct {
	ID                               string
	ARN                              string
	IpamID                           string
	IpamARN                          string
	IpamRegion                       string
	OwnerID                          string
	AddressFamily                    string
	Description                      string
	State                            string
	LastVersionCreationStatus        string
	LastVersionCreationStatusMessage string
	Tags                             map[string]string
}

// IpamPrefixListResolverTarget is a managed prefix list a resolver syncs into.
type IpamPrefixListResolverTarget struct {
	ID                 string
	ARN                string
	ResolverID         string
	OwnerID            string
	PrefixListID       string
	PrefixListRegion   string
	DesiredVersion     int
	LastSyncedVersion  int
	TrackLatestVersion bool
	State              string
	StateMessage       string
	Tags               map[string]string
}

// IpamPrefixListResolverVersion is a published version of a resolver's rules.
type IpamPrefixListResolverVersion struct {
	Version   int
	CreatedAt time.Time
}

// IpamPrefixListResolverRule is one rule evaluated by a resolver.
type IpamPrefixListResolverRule struct {
	IpamPoolID string
	Cidr       string
}

// IpamExternalResourceVerificationToken authorizes external (on-prem) CIDRs.
type IpamExternalResourceVerificationToken struct {
	ID         string
	ARN        string
	IpamID     string
	IpamARN    string
	IpamRegion string
	OwnerID    string
	TokenName  string
	TokenValue string
	NotAfter   time.Time
	State      string
	Status     string
	Tags       map[string]string
}

// IPAMPrefixListResolver is an OPTIONAL AWS capability for IPAM prefix-list
// resolvers, their targets, and published versions.
//
//nolint:interfacebloat // mirrors the prefix-list-resolver API surface.
type IPAMPrefixListResolver interface {
	CreateIpamPrefixListResolver(
		ctx context.Context, ipamID, addressFamily, description string, tags map[string]string,
	) (*IpamPrefixListResolver, error)
	DescribeIpamPrefixListResolvers(ctx context.Context, ids []string) ([]IpamPrefixListResolver, error)
	ModifyIpamPrefixListResolver(ctx context.Context, id, description string) (*IpamPrefixListResolver, error)
	DeleteIpamPrefixListResolver(ctx context.Context, id string) (*IpamPrefixListResolver, error)

	CreateIpamPrefixListResolverTarget(
		ctx context.Context, resolverID, prefixListID, prefixListRegion string,
		desiredVersion int, trackLatest bool, tags map[string]string,
	) (*IpamPrefixListResolverTarget, error)
	DescribeIpamPrefixListResolverTargets(ctx context.Context, ids []string) ([]IpamPrefixListResolverTarget, error)
	ModifyIpamPrefixListResolverTarget(
		ctx context.Context, id string, desiredVersion int, trackLatest bool,
	) (*IpamPrefixListResolverTarget, error)
	DeleteIpamPrefixListResolverTarget(ctx context.Context, id string) (*IpamPrefixListResolverTarget, error)

	GetIpamPrefixListResolverRules(ctx context.Context, resolverID string) ([]IpamPrefixListResolverRule, error)
	GetIpamPrefixListResolverVersions(ctx context.Context, resolverID string) ([]IpamPrefixListResolverVersion, error)
	GetIpamPrefixListResolverVersionEntries(
		ctx context.Context, resolverID string, version int,
	) ([]PrefixListEntry, error)
}

// IPAMExternalToken is an OPTIONAL AWS capability for external-resource
// verification tokens.
type IPAMExternalToken interface {
	CreateIpamExternalResourceVerificationToken(
		ctx context.Context, ipamID, tokenName string, tags map[string]string,
	) (*IpamExternalResourceVerificationToken, error)
	DeleteIpamExternalResourceVerificationToken(ctx context.Context, id string) (*IpamExternalResourceVerificationToken, error)
	DescribeIpamExternalResourceVerificationTokens(ctx context.Context, ids []string) ([]IpamExternalResourceVerificationToken, error)
}
