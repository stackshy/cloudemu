package applicationgateway

// Azure ARM JSON wire structures for Microsoft.Network/applicationGateways. Only
// the subset needed to model the gateway shell (location, zones, identity, sku,
// tags) is typed; the nested collections and every other top-level property are
// carried as generic JSON so their values — including explicit booleans and
// cross-collection {id} references — round-trip verbatim.

const (
	providerName = "Microsoft.Network"
	typeAppGws   = "applicationGateways"

	gwResourceType = "Microsoft.Network/applicationGateways"

	// provisioningStateSucceeded is the terminal state the SDK poller and
	// Terraform wait for, stamped gateway-wide and on every modeled child.
	provisioningStateSucceeded = "Succeeded"
	provisioningStateKey       = "provisioningState"

	defaultLocation = "eastus"
)

// modeledCollections lists the nested collection ARM segment names this handler
// models — the seven required collections plus the two optional ones — in a
// fixed order so responses are stable. Each modeled child gets an ARM id
// self-link and a provisioningState on read. Every other top-level property
// (WAF, urlPathMaps, rewriteRuleSets, redirectConfigurations,
// autoscaleConfiguration, sslProfiles, privateLinkConfigurations, ...) is
// deferred and preserved verbatim in AzureAppGateway.OtherProps.
//
//nolint:gochecknoglobals // fixed, read-only registry of modeled collection names.
var modeledCollections = []string{
	"gatewayIPConfigurations",
	"frontendIPConfigurations",
	"frontendPorts",
	"backendAddressPools",
	"backendHttpSettingsCollection",
	"httpListeners",
	"requestRoutingRules",
	"probes",
	"sslCertificates",
}

// appGwSKU is the gateway sku. Capacity is a pointer so an explicit fixed
// capacity survives while an omitted one (autoscale) stays absent.
type appGwSKU struct {
	Name     string `json:"name,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Capacity *int   `json:"capacity,omitempty"`
}

// appGwChildJSON is one nested-collection item on the wire: identity plus the
// verbatim properties object (with provisioningState injected on read).
type appGwChildJSON struct {
	ID         string         `json:"id,omitempty"`
	Name       string         `json:"name,omitempty"`
	Type       string         `json:"type,omitempty"`
	Etag       string         `json:"etag,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

// appGwJSON is the ARM Application Gateway wire body. Properties is generic so
// the modeled collections, provisioningState and every deferred top-level
// property coexist in one object.
type appGwJSON struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	Zones      []string          `json:"zones,omitempty"`
	Identity   map[string]any    `json:"identity,omitempty"`
	SKU        *appGwSKU         `json:"sku,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties map[string]any    `json:"properties,omitempty"`
}

// appGwListResult is the ARM list envelope.
type appGwListResult struct {
	Value []appGwJSON `json:"value"`
}

// subResourceListResult is the ARM list envelope shared by the read-only
// sub-resource collection reflections.
type subResourceListResult struct {
	Value any `json:"value"`
}
