// Package driver defines the storage contract for the Azure Application Gateway
// (Microsoft.Network/applicationGateways).
//
// Application Gateway is an Azure-only, deeply-nested "echo-class" resource: a
// single PUT carries the whole gateway — SKU, zones, identity and seven required
// nested collections (gatewayIPConfigurations, frontendIPConfigurations,
// frontendPorts, backendAddressPools, backendHttpSettingsCollection,
// httpListeners, requestRoutingRules) plus optional probes and sslCertificates —
// whose items cross-reference each other by ARM sub-resource id. The cross-cloud
// LoadBalancer / networking models cannot represent this shape, so — like
// AzureLoadBalancers and AzureApplicationSecurityGroups — the Azure provider
// stores the ARM gateway natively and exposes it through this dedicated,
// single-provider interface. AWS and GCP have no equivalent.
//
// The item-level property values (ports, protocols, cookieBasedAffinity,
// requireServerNameIndication and every cross-collection {id} reference) are kept
// verbatim as generic JSON, so an explicit false or a rarely-modeled field
// round-trips exactly; the wire handler stamps each item's ARM id and
// provisioningState on read. Top-level properties beyond the modeled collections
// (WAF, urlPathMaps, rewriteRuleSets, autoscaleConfiguration, ...) are preserved
// verbatim in OtherProperties so deferred sub-surfaces are echo-through-safe.
package driver

import "context"

// AzureAppGatewayChild is one item of a nested Application Gateway collection
// (a backend pool, listener, routing rule, ...). Name is the ARM child name;
// Properties is the item's "properties" object stored verbatim as generic JSON
// so every value — including explicit booleans and cross-collection {id}
// references — survives a round-trip unchanged. The wire handler stamps the
// item's ARM id/type/etag and injects provisioningState on read.
type AzureAppGatewayChild struct {
	Name       string
	Properties map[string]any
}

// AzureAppGateway is the natively-stored ARM Application Gateway. Zones,
// Identity and the SKU fields are modeled explicitly because the server-wide
// unmodeled-property echo only reaches the top-level "properties" object and so
// cannot preserve them. Collections holds the modeled nested collections keyed
// by their ARM segment name (backendAddressPools, httpListeners, ...), each an
// ordered slice so item order is preserved. OtherProperties holds every other
// top-level property verbatim, keeping deferred sub-surfaces round-trip-safe.
type AzureAppGateway struct {
	Name          string
	ResourceGroup string
	Location      string
	Zones         []string
	Identity      map[string]any
	SKUName       string
	SKUTier       string
	SKUCapacity   int
	Tags          map[string]string
	Collections   map[string][]AzureAppGatewayChild
	OtherProps    map[string]any
	ETag          string
}

// AzureApplicationGateways is the Azure-only Application Gateway store, keyed by
// (resourceGroup, name) to match ARM addressing. CreateOrUpdate is a full
// replace: a child or top-level property absent from the payload is dropped,
// matching ARM's PUT semantics. An empty resourceGroup on List means
// subscription-wide.
type AzureApplicationGateways interface {
	// CreateOrUpdateAzureApplicationGateway stores gw as a full replace and
	// reports whether it did not previously exist (created==true → HTTP 201, else
	// 200). The returned value is a defensive copy.
	CreateOrUpdateAzureApplicationGateway(
		ctx context.Context, rg, name string, gw AzureAppGateway,
	) (stored *AzureAppGateway, created bool, err error)
	// GetAzureApplicationGateway returns the gateway identified by
	// (resourceGroup, name), or NotFound.
	GetAzureApplicationGateway(ctx context.Context, rg, name string) (*AzureAppGateway, error)
	// DeleteAzureApplicationGateway removes the gateway, returning NotFound if it
	// does not exist.
	DeleteAzureApplicationGateway(ctx context.Context, rg, name string) error
	// ListAzureApplicationGateways returns the gateways in rg, or all when rg is
	// empty (subscription-wide list), ordered by key.
	ListAzureApplicationGateways(ctx context.Context, rg string) ([]AzureAppGateway, error)
}
