package firewall

// Azure ARM JSON wire structures for Microsoft.Network/azureFirewalls and
// Microsoft.Network/firewallPolicies. Only the subset needed to model the shells
// (location, zones, tags) plus the explicitly-modeled nested pieces is typed; the
// remaining properties are carried as generic JSON so their values — including
// explicit booleans and cross-resource {id} references — round-trip verbatim.

const (
	providerName = "Microsoft.Network"

	typeAzureFirewalls   = "azureFirewalls"
	typeFirewallPolicies = "firewallPolicies"

	firewallResourceType = "Microsoft.Network/azureFirewalls"
	policyResourceType   = "Microsoft.Network/firewallPolicies"

	// provisioningStateSucceeded is the terminal state the SDK poller and
	// Terraform wait for, stamped on the resource and each modeled ipConfiguration.
	provisioningStateSucceeded = "Succeeded"
	provisioningStateKey       = "provisioningState"

	// threatIntelModeDefault is the operation mode Azure applies when a request
	// omits threatIntelMode.
	threatIntelModeDefault = "Alert"
	threatIntelModeKey     = "threatIntelMode"

	skuKey            = "sku"
	ipConfigsKey      = "ipConfigurations"
	firewallPolicyKey = "firewallPolicy"

	defaultLocation = "eastus"

	// privateIPBase is the deterministic private IP assigned to the first
	// ipConfiguration that carries a subnet (real Azure allocates from
	// AzureFirewallSubnet, conventionally x.x.x.4). Stable across updates so
	// Terraform's computed private_ip_address never drifts.
	privateIPPrefix = "10.0.1."
	privateIPFirst  = 4
)

// subResource is an ARM {id} reference (subnet, publicIPAddress, firewallPolicy).
type subResource struct {
	ID string `json:"id,omitempty"`
}

// skuJSON is the firewall sku (name+tier) or policy sku (tier only), nested under
// properties in the armnetwork SDK / Terraform wire shape.
type skuJSON struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

// ipConfigJSON is one azureFirewalls ipConfigurations item on the wire.
type ipConfigJSON struct {
	ID         string             `json:"id,omitempty"`
	Name       string             `json:"name,omitempty"`
	Etag       string             `json:"etag,omitempty"`
	Properties *ipConfigPropsJSON `json:"properties,omitempty"`
}

// ipConfigPropsJSON is the properties object of one ipConfiguration. Subnet and
// publicIPAddress are request inputs; privateIPAddress and provisioningState are
// computed read-only outputs.
type ipConfigPropsJSON struct {
	Subnet            *subResource `json:"subnet,omitempty"`
	PublicIPAddress   *subResource `json:"publicIPAddress,omitempty"`
	PrivateIPAddress  string       `json:"privateIPAddress,omitempty"`
	ProvisioningState string       `json:"provisioningState,omitempty"`
}

// firewallJSON is the ARM azureFirewalls wire body. Properties is generic so the
// modeled sku/threatIntelMode/ipConfigurations/firewallPolicy and every deferred
// property coexist in one object.
type firewallJSON struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	Zones      []string          `json:"zones,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties map[string]any    `json:"properties,omitempty"`
}

// policyJSON is the ARM firewallPolicies wire body.
type policyJSON struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties map[string]any    `json:"properties,omitempty"`
}

// firewallListResult is the ARM list envelope for azureFirewalls.
type firewallListResult struct {
	Value []firewallJSON `json:"value"`
}

// policyListResult is the ARM list envelope for firewallPolicies.
type policyListResult struct {
	Value []policyJSON `json:"value"`
}
