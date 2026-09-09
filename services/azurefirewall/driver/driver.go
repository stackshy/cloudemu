// Package driver defines the storage contract for Azure Firewall
// (Microsoft.Network/azureFirewalls) and Azure Firewall Policy
// (Microsoft.Network/firewallPolicies).
//
// Both are Azure-only ARM "echo-class" resources: a single PUT carries the whole
// resource, and ARM CreateOrUpdate replaces it wholesale. The cross-cloud
// networking models have no equivalent, so — like AzureLoadBalancers and
// AzureApplicationGateways — the Azure provider stores the ARM body natively and
// exposes it through this dedicated, single-provider interface. AWS and GCP have
// no counterpart.
//
// The pieces the server-wide unmodeled-property echo cannot preserve are modeled
// explicitly: the firewall's sku (name/tier) and threatIntelMode live nested
// under properties.sku / properties.threatIntelMode (the echo only round-trips
// whole top-level property keys, not their internals, and the handler must inject
// defaults and the terminal provisioningState); zones is top-level (outside the
// properties object the echo reaches at all); ipConfigurations round-trip with a
// computed privateIPAddress plus stamped ARM ids; firewallPolicy is the
// association back-reference. Every other property (the deprecated classic rule
// collections, hubIPAddresses, IDPS/TLS/DNS settings, ...) is preserved verbatim
// in OtherProps so deferred sub-surfaces stay echo-through-safe.
package driver

import "context"

// AzureFirewallIPConfig is one item of an Azure Firewall's ipConfigurations
// collection. SubnetID and PublicIPAddressID are the ARM references supplied in
// the request; PrivateIPAddress is the read-only IP the emulator computes for a
// config that carries a subnet (real Azure allocates it from AzureFirewallSubnet).
type AzureFirewallIPConfig struct {
	Name              string
	SubnetID          string
	PublicIPAddressID string
	PrivateIPAddress  string
}

// AzureFirewall is the natively-stored ARM Azure Firewall. SKUName/SKUTier,
// ThreatIntelMode, Zones, IPConfigurations and FirewallPolicyID are modeled
// explicitly because the server-wide unmodeled-property echo cannot preserve
// them. OtherProps holds every other top-level property under "properties"
// verbatim, keeping deferred sub-surfaces round-trip-safe.
type AzureFirewall struct {
	Name             string
	ResourceGroup    string
	Location         string
	Zones            []string
	Tags             map[string]string
	SKUName          string
	SKUTier          string
	ThreatIntelMode  string
	FirewallPolicyID string
	IPConfigurations []AzureFirewallIPConfig
	OtherProps       map[string]any
	ETag             string
}

// FirewallPolicy is the natively-stored ARM Firewall Policy. SKUTier and
// ThreatIntelMode are modeled explicitly (nested under properties, so the echo
// cannot reach them); OtherProps preserves every other property (dnsSettings,
// threatIntelWhitelist, intrusionDetection, ...) verbatim.
type FirewallPolicy struct {
	Name            string
	ResourceGroup   string
	Location        string
	Tags            map[string]string
	SKUTier         string
	ThreatIntelMode string
	OtherProps      map[string]any
	ETag            string
}

// AzureFirewalls is the Azure-only Firewall + Firewall Policy store, keyed by
// (resourceGroup, name) to match ARM addressing. CreateOrUpdate is a full
// replace: a property absent from the payload is dropped, matching ARM's PUT
// semantics. An empty resourceGroup on List means subscription-wide.
type AzureFirewalls interface {
	// CreateOrUpdateAzureFirewall stores fw as a full replace and reports whether
	// it did not previously exist (created==true → HTTP 201, else 200). The
	// returned value is a defensive copy.
	CreateOrUpdateAzureFirewall(
		ctx context.Context, rg, name string, fw AzureFirewall,
	) (stored *AzureFirewall, created bool, err error)
	// GetAzureFirewall returns the firewall identified by (resourceGroup, name),
	// or NotFound.
	GetAzureFirewall(ctx context.Context, rg, name string) (*AzureFirewall, error)
	// DeleteAzureFirewall removes the firewall, returning NotFound if it does not
	// exist.
	DeleteAzureFirewall(ctx context.Context, rg, name string) error
	// ListAzureFirewalls returns the firewalls in rg, or all when rg is empty
	// (subscription-wide list), ordered by key.
	ListAzureFirewalls(ctx context.Context, rg string) ([]AzureFirewall, error)

	// CreateOrUpdateFirewallPolicy stores pol as a full replace and reports
	// whether it did not previously exist.
	CreateOrUpdateFirewallPolicy(
		ctx context.Context, rg, name string, pol FirewallPolicy,
	) (stored *FirewallPolicy, created bool, err error)
	// GetFirewallPolicy returns the policy identified by (resourceGroup, name),
	// or NotFound.
	GetFirewallPolicy(ctx context.Context, rg, name string) (*FirewallPolicy, error)
	// DeleteFirewallPolicy removes the policy, returning NotFound if it does not
	// exist.
	DeleteFirewallPolicy(ctx context.Context, rg, name string) error
	// ListFirewallPolicies returns the policies in rg, or all when rg is empty
	// (subscription-wide list), ordered by key.
	ListFirewallPolicies(ctx context.Context, rg string) ([]FirewallPolicy, error)
}
