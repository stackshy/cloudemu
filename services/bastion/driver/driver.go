// Package driver defines the storage contract for Azure Bastion
// (Microsoft.Network/bastionHosts).
//
// Azure Bastion is an Azure-only ARM "echo-class" control-plane resource: a
// single PUT carries the whole host and ARM CreateOrUpdate replaces it
// wholesale. There is no cross-cloud equivalent, so — like AzureFirewalls and
// AzureLoadBalancers — the Azure provider stores the ARM body natively and
// exposes it through this dedicated, single-provider interface. AWS and GCP have
// no counterpart.
//
// The pieces the server-wide unmodeled-property echo cannot preserve are modeled
// explicitly: the top-level sku (a sibling of the properties object the echo
// reaches, so out of its range) and zones; the computed, stable dnsName; the
// scaleUnits count; the six feature toggles (Azure always returns them as
// explicit booleans, so an omitted one must still surface as false rather than
// being dropped); and ipConfigurations, which round-trip with stamped ARM child
// ids and a computed privateIPAllocationMethod. Every other property under
// "properties" is preserved verbatim in OtherProps so deferred sub-surfaces stay
// echo-through-safe. Real bastion session tunneling (the data plane) is out of
// scope.
package driver

import "context"

// BastionHostIPConfig is one item of a Bastion host's ipConfigurations
// collection. SubnetID and PublicIPAddressID are the ARM references supplied in
// the request (the subnet must be the reserved AzureBastionSubnet). Azure always
// allocates the private IP dynamically, so no allocation method is stored — the
// wire layer stamps the computed privateIPAllocationMethod=Dynamic on read.
type BastionHostIPConfig struct {
	Name              string
	SubnetID          string
	PublicIPAddressID string
}

// BastionHost is the natively-stored ARM Bastion host. SKUName, Zones, DNSName,
// ScaleUnits, the six feature toggles and IPConfigurations are modeled
// explicitly because the server-wide unmodeled-property echo cannot preserve
// them. OtherProps holds every other property under "properties" verbatim,
// keeping deferred sub-surfaces round-trip-safe.
//
// The feature toggles are *bool so an unset flag (nil) is distinguishable from
// an explicit false; on read both surface as an explicit false, matching Azure.
type BastionHost struct {
	Name                string
	ResourceGroup       string
	Location            string
	Zones               []string
	Tags                map[string]string
	SKUName             string
	DNSName             string
	ScaleUnits          int
	DisableCopyPaste    *bool
	EnableTunneling     *bool
	EnableIPConnect     *bool
	EnableShareableLink *bool
	EnableFileCopy      *bool
	EnableKerberos      *bool
	IPConfigurations    []BastionHostIPConfig
	OtherProps          map[string]any
	ETag                string
}

// BastionHosts is the Azure-only Bastion host store, keyed by (resourceGroup,
// name) to match ARM addressing. CreateOrUpdate is a full replace: a property
// absent from the payload is dropped, matching ARM's PUT semantics — except the
// computed dnsName, which is generated once on create and preserved across
// updates so it never drifts. An empty resourceGroup on List means
// subscription-wide.
type BastionHosts interface {
	// CreateOrUpdateBastionHost stores host as a full replace and reports whether
	// it did not previously exist (created==true → HTTP 201, else 200). The store
	// injects the sku (default Standard) and scaleUnits (default 2) defaults and
	// generates a stable dnsName on first create. The returned value is a
	// defensive copy.
	CreateOrUpdateBastionHost(
		ctx context.Context, rg, name string, host BastionHost,
	) (stored *BastionHost, created bool, err error)
	// GetBastionHost returns the host identified by (resourceGroup, name), or
	// NotFound.
	GetBastionHost(ctx context.Context, rg, name string) (*BastionHost, error)
	// DeleteBastionHost removes the host, returning NotFound if it does not exist.
	DeleteBastionHost(ctx context.Context, rg, name string) error
	// ListBastionHosts returns the hosts in rg, or all when rg is empty
	// (subscription-wide list), ordered by key.
	ListBastionHosts(ctx context.Context, rg string) ([]BastionHost, error)
}
