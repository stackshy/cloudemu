package driver

import "context"

// Microsoft.Network/publicIPPrefixes reserves a contiguous CIDR range from which
// standard public IP addresses can be drawn. The cross-cloud Networking model has
// no equivalent, so — like AzureNetworkMetadata and AzureApplicationSecurityGroups
// — the Azure provider stores it through this OPTIONAL, type-asserted capability.
// AWS and GCP do not implement it.

// AzurePublicIPPrefix is one Microsoft.Network/publicIPPrefixes resource,
// addressed by (resourceGroup, name) to match ARM. IPPrefix is the CIDR the
// provider synthesizes at create time (of size PrefixLength) and is immutable for
// the life of the prefix, so a later createOrUpdate PUT preserves it.
type AzurePublicIPPrefix struct {
	Name          string
	ResourceGroup string
	Location      string
	Tags          map[string]string
	Zones         []string
	// PrefixLength is the CIDR mask length requested at create (e.g. 28). It is
	// immutable after create, matching real Azure.
	PrefixLength int32
	// IPPrefix is the synthesized CIDR (e.g. "10.0.0.0/28"), allocated by the
	// provider from a deterministic private pool. Empty on the value handed to
	// Put; the provider fills it in.
	IPPrefix string
	// SKUName / SKUTier echo the top-level sku the caller submitted (Standard or
	// StandardV2, tier Regional). They are not part of the cross-cloud model, so
	// they live here on the Azure-only record.
	SKUName string
	SKUTier string
}

// AzurePublicIPPrefixes is the Azure-only public-IP-prefix surface. Keyed by
// (resourceGroup, name) for idempotent createOrUpdate; an empty resourceGroup on
// List means subscription-wide.
type AzurePublicIPPrefixes interface {
	// PutAzurePublicIPPrefix creates or replaces a prefix in place (a repeat
	// createOrUpdate PUT updates rather than duplicating). On create it synthesizes
	// and stores the IPPrefix CIDR; on an idempotent re-PUT it preserves the
	// existing IPPrefix and PrefixLength (both immutable in real Azure). It returns
	// the stored value with IPPrefix populated.
	PutAzurePublicIPPrefix(ctx context.Context, prefix AzurePublicIPPrefix) AzurePublicIPPrefix
	// GetAzurePublicIPPrefix returns the prefix identified by (resourceGroup, name).
	GetAzurePublicIPPrefix(ctx context.Context, resourceGroup, name string) (AzurePublicIPPrefix, bool)
	// DeleteAzurePublicIPPrefix removes the prefix, reporting whether it existed.
	DeleteAzurePublicIPPrefix(ctx context.Context, resourceGroup, name string) bool
	// ListAzurePublicIPPrefixes returns the prefixes in a resource group, or all
	// when resourceGroup is empty (subscription-wide list), ordered by key.
	ListAzurePublicIPPrefixes(ctx context.Context, resourceGroup string) []AzurePublicIPPrefix
}
