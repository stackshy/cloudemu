package vnet

// ARM JSON shapes for Microsoft.Network resources we expose.

type vnetRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties vnetRequestProps  `json:"properties"`
}

type vnetRequestProps struct {
	AddressSpace *addressSpace   `json:"addressSpace,omitempty"`
	Subnets      []subnetRequest `json:"subnets,omitempty"`
}

type addressSpace struct {
	AddressPrefixes []string `json:"addressPrefixes"`
}

type vnetResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Etag       string            `json:"etag,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties vnetResponseProps `json:"properties"`
}

type vnetResponseProps struct {
	ProvisioningState string           `json:"provisioningState"`
	AddressSpace      *addressSpace    `json:"addressSpace,omitempty"`
	Subnets           []subnetResponse `json:"subnets,omitempty"`
}

type vnetListResponse struct {
	Value []vnetResponse `json:"value"`
}

type subnetRequest struct {
	Name       string             `json:"name,omitempty"`
	Properties subnetRequestProps `json:"properties"`
}

type subnetRequestProps struct {
	AddressPrefix string    `json:"addressPrefix,omitempty"`
	NatGateway    *armIDRef `json:"natGateway,omitempty"`
}

type subnetResponse struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Etag       string              `json:"etag,omitempty"`
	Properties subnetResponseProps `json:"properties"`
}

type subnetResponseProps struct {
	ProvisioningState string    `json:"provisioningState"`
	AddressPrefix     string    `json:"addressPrefix,omitempty"`
	NatGateway        *armIDRef `json:"natGateway,omitempty"`
}

type subnetListResponse struct {
	Value []subnetResponse `json:"value"`
}

type nsgRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties nsgRequestProps   `json:"properties,omitempty"`
}

type nsgRequestProps struct {
	SecurityRules []securityRule `json:"securityRules,omitempty"`
}

type securityRule struct {
	Name       string            `json:"name,omitempty"`
	ID         string            `json:"id,omitempty"`
	Properties securityRuleProps `json:"properties,omitempty"`
}

type securityRuleProps struct {
	Description              string `json:"description,omitempty"`
	Protocol                 string `json:"protocol,omitempty"`
	SourceAddressPrefix      string `json:"sourceAddressPrefix,omitempty"`
	DestinationAddressPrefix string `json:"destinationAddressPrefix,omitempty"`
	SourcePortRange          string `json:"sourcePortRange,omitempty"`
	DestinationPortRange     string `json:"destinationPortRange,omitempty"`
	Access                   string `json:"access,omitempty"`
	Priority                 int    `json:"priority,omitempty"`
	Direction                string `json:"direction,omitempty"`
	ProvisioningState        string `json:"provisioningState,omitempty"`
}

type nsgResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Etag       string            `json:"etag,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties nsgResponseProps  `json:"properties"`
}

type nsgResponseProps struct {
	ProvisioningState    string         `json:"provisioningState"`
	SecurityRules        []securityRule `json:"securityRules"`
	DefaultSecurityRules []securityRule `json:"defaultSecurityRules,omitempty"`
}

type nsgListResponse struct {
	Value []nsgResponse `json:"value"`
}

type publicIPRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *publicIPSKU      `json:"sku,omitempty"`
	Zones      []string          `json:"zones,omitempty"`
	Properties publicIPReqProps  `json:"properties"`
}

type publicIPSKU struct {
	Name string `json:"name,omitempty"`
}

type publicIPReqProps struct {
	PublicIPAllocationMethod string                  `json:"publicIPAllocationMethod,omitempty"`
	IdleTimeoutInMinutes     int                     `json:"idleTimeoutInMinutes,omitempty"`
	DNSSettings              *publicIPDNSSettingsReq `json:"dnsSettings,omitempty"`
}

type publicIPDNSSettingsReq struct {
	DomainNameLabel string `json:"domainNameLabel,omitempty"`
}

type publicIPResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *publicIPSKU      `json:"sku,omitempty"`
	Zones      []string          `json:"zones,omitempty"`
	Properties publicIPRespProps `json:"properties"`
}

type publicIPRespProps struct {
	ProvisioningState        string               `json:"provisioningState"`
	PublicIPAllocationMethod string               `json:"publicIPAllocationMethod,omitempty"`
	IPAddress                string               `json:"ipAddress,omitempty"`
	IdleTimeoutInMinutes     int                  `json:"idleTimeoutInMinutes,omitempty"`
	DNSSettings              *publicIPDNSSettings `json:"dnsSettings,omitempty"`
	// IPConfiguration is the back-reference to the NIC ipConfiguration a real
	// publicIPAddresses GET reports once a NIC attaches the address.
	IPConfiguration *armIDRef `json:"ipConfiguration,omitempty"`
}

type publicIPDNSSettings struct {
	DomainNameLabel string `json:"domainNameLabel,omitempty"`
	FQDN            string `json:"fqdn,omitempty"`
}

type publicIPListResponse struct {
	Value []publicIPResponse `json:"value"`
}
