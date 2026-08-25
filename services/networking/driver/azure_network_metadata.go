package driver

import "context"

// Azure virtual networks and network security groups carry ARM-specific fields
// the cross-cloud VPC / SecurityGroup model cannot represent: a region, the
// full multi-entry address-prefix list, and security rules with an Azure
// priority / access / direction / service-tag shape. Rather than widen the
// AWS-shaped structs (or force AWS/GCP to carry fields they never populate),
// the Azure provider stores these alongside the cross-cloud resource and
// exposes them through AzureNetworkMetadata, an OPTIONAL capability discovered
// by type assertion — the same pattern as AzureNetworkInterfaces.

// AzureVNetMetadata holds the Azure-only virtual-network fields (region and the
// full address-prefix list). The cross-cloud VPCInfo keeps a single CIDR; this
// records every prefix and the creation region so a Get round-trips faithfully.
type AzureVNetMetadata struct {
	Location        string
	AddressPrefixes []string
}

// AzureNSGRule is one Azure network-security-group security rule, with the
// priority / access / direction / service-tag fields the cross-cloud
// SecurityRule model omits. Ports and address prefixes are kept verbatim as the
// ARM strings ("*", "VirtualNetwork", "10.0.0.0/24", "80", "0-65535").
type AzureNSGRule struct {
	Name                     string
	Description              string
	Protocol                 string
	SourceAddressPrefix      string
	DestinationAddressPrefix string
	SourcePortRange          string
	DestinationPortRange     string
	Access                   string
	Direction                string
	Priority                 int
}

// AzureNSGMetadata holds the Azure-only network-security-group fields (region
// and the caller's custom security rules). The built-in default rules are
// synthesized on read and are not stored here.
type AzureNSGMetadata struct {
	Location      string
	SecurityRules []AzureNSGRule
}

// AzureNetworkMetadata is an OPTIONAL, type-asserted capability. The Azure
// provider stores the ARM-specific virtual-network and network-security-group
// fields the cross-cloud Networking interface cannot represent, keyed by the
// driver resource id (the VPC / security-group id). AWS and GCP do not
// implement it.
type AzureNetworkMetadata interface {
	PutAzureVNetMetadata(ctx context.Context, id string, meta AzureVNetMetadata) error
	GetAzureVNetMetadata(ctx context.Context, id string) (AzureVNetMetadata, bool)
	DeleteAzureVNetMetadata(ctx context.Context, id string)

	PutAzureNSGMetadata(ctx context.Context, id string, meta AzureNSGMetadata) error
	GetAzureNSGMetadata(ctx context.Context, id string) (AzureNSGMetadata, bool)
	DeleteAzureNSGMetadata(ctx context.Context, id string)

	// UpsertAzureNSGRule creates or replaces a single custom security rule by
	// name, leaving every sibling rule untouched — the atomic read-modify-write
	// the SecurityRules sub-resource CRUD (securityRules/{ruleName}) needs.
	// Returns NotFound when the network security group itself doesn't exist.
	UpsertAzureNSGRule(ctx context.Context, id string, rule AzureNSGRule) (AzureNSGMetadata, error)
	// DeleteAzureNSGRule removes a single custom security rule by name, leaving
	// every sibling rule untouched. Returns NotFound when either the network
	// security group or the named rule doesn't exist.
	DeleteAzureNSGRule(ctx context.Context, id string, ruleName string) error
}
