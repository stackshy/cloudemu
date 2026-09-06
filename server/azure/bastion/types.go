package bastion

// Azure ARM JSON wire structures for Microsoft.Network/bastionHosts. Only the
// subset needed to model the shell (location, zones, tags, top-level sku) plus
// the explicitly-modeled properties (dnsName, scaleUnits, the six feature
// toggles, ipConfigurations) is typed; the remaining properties are carried as
// generic JSON so their values round-trip verbatim.

const (
	providerName = "Microsoft.Network"

	typeBastionHosts    = "bastionHosts"
	bastionResourceType = "Microsoft.Network/bastionHosts"

	// ipConfigChildType is the ARM child segment stamped onto each
	// ipConfiguration's id (.../bastionHosts/{name}/bastionHostIpConfigurations/{cfg}).
	ipConfigChildType = "bastionHostIpConfigurations"

	// provisioningStateSucceeded is the terminal state the SDK poller and
	// Terraform wait for, stamped on the host and each ipConfiguration.
	provisioningStateSucceeded = "Succeeded"
	provisioningStateKey       = "provisioningState"

	// privateIPAllocationDynamic is the read-only allocation method Azure reports
	// for every bastion ipConfiguration (the private IP is always dynamic).
	privateIPAllocationDynamic = "Dynamic"

	// skuStandard is the sku echoed when a request omits it (Azure's default).
	skuStandard = "Standard"

	// defaultScaleUnits is the scale-unit count echoed when scaleUnits is unset.
	defaultScaleUnits = 2

	defaultLocation = "eastus"

	// Modeled properties keys, extracted from the request and re-stamped on read.
	ipConfigsKey           = "ipConfigurations"
	dnsNameKey             = "dnsName"
	scaleUnitsKey          = "scaleUnits"
	disableCopyPasteKey    = "disableCopyPaste"
	enableTunnelingKey     = "enableTunneling"
	enableIPConnectKey     = "enableIpConnect"
	enableShareableLinkKey = "enableShareableLink"
	enableFileCopyKey      = "enableFileCopy"
	enableKerberosKey      = "enableKerberos"
)

// subResource is an ARM {id} reference (subnet, publicIPAddress).
type subResource struct {
	ID string `json:"id,omitempty"`
}

// skuJSON is the bastion sku, a top-level sibling of the properties object.
type skuJSON struct {
	Name string `json:"name,omitempty"`
}

// ipConfigJSON is one bastionHosts ipConfigurations item on the wire.
type ipConfigJSON struct {
	ID         string             `json:"id,omitempty"`
	Name       string             `json:"name,omitempty"`
	Etag       string             `json:"etag,omitempty"`
	Properties *ipConfigPropsJSON `json:"properties,omitempty"`
}

// ipConfigPropsJSON is the properties object of one ipConfiguration. Subnet and
// publicIPAddress are request inputs; privateIPAllocationMethod and
// provisioningState are computed read-only outputs.
type ipConfigPropsJSON struct {
	Subnet                    *subResource `json:"subnet,omitempty"`
	PublicIPAddress           *subResource `json:"publicIPAddress,omitempty"`
	PrivateIPAllocationMethod string       `json:"privateIPAllocationMethod,omitempty"`
	ProvisioningState         string       `json:"provisioningState,omitempty"`
}

// bastionHostJSON is the ARM bastionHosts wire body. sku and zones are top-level
// siblings of properties (outside the echo's reach); Properties is generic so the
// modeled dnsName/scaleUnits/toggles/ipConfigurations and every deferred property
// coexist in one object.
type bastionHostJSON struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	Zones      []string          `json:"zones,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *skuJSON          `json:"sku,omitempty"`
	Properties map[string]any    `json:"properties,omitempty"`
}

// bastionListResult is the ARM list envelope for bastionHosts.
type bastionListResult struct {
	Value []bastionHostJSON `json:"value"`
}
