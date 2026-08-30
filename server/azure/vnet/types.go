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
	AddressPrefix        string    `json:"addressPrefix,omitempty"`
	NatGateway           *armIDRef `json:"natGateway,omitempty"`
	NetworkSecurityGroup *armIDRef `json:"networkSecurityGroup,omitempty"`
	RouteTable           *armIDRef `json:"routeTable,omitempty"`
}

type subnetResponse struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Etag       string              `json:"etag,omitempty"`
	Properties subnetResponseProps `json:"properties"`
}

type subnetResponseProps struct {
	ProvisioningState    string    `json:"provisioningState"`
	AddressPrefix        string    `json:"addressPrefix,omitempty"`
	NatGateway           *armIDRef `json:"natGateway,omitempty"`
	NetworkSecurityGroup *armIDRef `json:"networkSecurityGroup,omitempty"`
	RouteTable           *armIDRef `json:"routeTable,omitempty"`
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
	Description                          string     `json:"description,omitempty"`
	Protocol                             string     `json:"protocol,omitempty"`
	SourceAddressPrefix                  string     `json:"sourceAddressPrefix,omitempty"`
	DestinationAddressPrefix             string     `json:"destinationAddressPrefix,omitempty"`
	SourcePortRange                      string     `json:"sourcePortRange,omitempty"`
	DestinationPortRange                 string     `json:"destinationPortRange,omitempty"`
	SourceApplicationSecurityGroups      []armIDRef `json:"sourceApplicationSecurityGroups,omitempty"`
	DestinationApplicationSecurityGroups []armIDRef `json:"destinationApplicationSecurityGroups,omitempty"`
	Access                               string     `json:"access,omitempty"`
	Priority                             int        `json:"priority,omitempty"`
	Direction                            string     `json:"direction,omitempty"`
	ProvisioningState                    string     `json:"provisioningState,omitempty"`
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
	// Subnets and NetworkInterfaces are the read-only back-references real ARM
	// reports on a networkSecurityGroups GET once the NSG is associated with a
	// subnet or NIC (server-side scans, mirroring vnetResponse's subnet scan and
	// publicIPConfigurationRef's NIC scan).
	Subnets           []armIDRef `json:"subnets,omitempty"`
	NetworkInterfaces []armIDRef `json:"networkInterfaces,omitempty"`
}

// securityRuleListResponse is the collection envelope for the securityRules
// sub-resource List operation.
type securityRuleListResponse struct {
	Value []securityRule `json:"value"`
}

// Effective-security-rules (InterfacesClient.BeginListEffectiveNetworkSecurityGroups).

type effectiveNSGListResponse struct {
	Value []effectiveNSG `json:"value"`
}

type effectiveNSG struct {
	Association            effectiveNSGAssociation `json:"association"`
	EffectiveSecurityRules []effectiveSecurityRule `json:"effectiveSecurityRules"`
	NetworkSecurityGroup   armIDRef                `json:"networkSecurityGroup"`
}

type effectiveNSGAssociation struct {
	NetworkInterface *armIDRef `json:"networkInterface,omitempty"`
	Subnet           *armIDRef `json:"subnet,omitempty"`
}

type effectiveSecurityRule struct {
	Name                     string `json:"name"`
	Protocol                 string `json:"protocol"`
	SourcePortRange          string `json:"sourcePortRange"`
	DestinationPortRange     string `json:"destinationPortRange"`
	SourceAddressPrefix      string `json:"sourceAddressPrefix"`
	DestinationAddressPrefix string `json:"destinationAddressPrefix"`
	Access                   string `json:"access"`
	Priority                 int    `json:"priority"`
	Direction                string `json:"direction"`
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
	// PublicIPPrefix is the optional prefix a public IP is drawn from. The mock
	// only stores the reference (child-IP allocation from the prefix range is
	// deferred); the prefix's publicIPAddresses[] back-reference is rebuilt from it.
	PublicIPPrefix *armIDRef `json:"publicIPPrefix,omitempty"`
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
	// PublicIPPrefix echoes the prefix this public IP was created from, when one
	// was supplied on the create.
	PublicIPPrefix *armIDRef `json:"publicIPPrefix,omitempty"`
}

type publicIPDNSSettings struct {
	DomainNameLabel string `json:"domainNameLabel,omitempty"`
	FQDN            string `json:"fqdn,omitempty"`
}

type publicIPListResponse struct {
	Value []publicIPResponse `json:"value"`
}

// VirtualNetworkPeerings sub-resource (VirtualNetworkPeeringsClient).

type vnetPeeringRequest struct {
	Properties vnetPeeringRequestProps `json:"properties"`
}

type vnetPeeringRequestProps struct {
	RemoteVirtualNetwork      *armIDRef `json:"remoteVirtualNetwork,omitempty"`
	AllowVirtualNetworkAccess *bool     `json:"allowVirtualNetworkAccess,omitempty"`
	AllowForwardedTraffic     *bool     `json:"allowForwardedTraffic,omitempty"`
	AllowGatewayTransit       *bool     `json:"allowGatewayTransit,omitempty"`
	UseRemoteGateways         *bool     `json:"useRemoteGateways,omitempty"`
}

type vnetPeeringResponse struct {
	ID         string                   `json:"id"`
	Name       string                   `json:"name"`
	Etag       string                   `json:"etag,omitempty"`
	Properties vnetPeeringResponseProps `json:"properties"`
}

type vnetPeeringResponseProps struct {
	ProvisioningState         string        `json:"provisioningState"`
	PeeringState              string        `json:"peeringState"`
	RemoteVirtualNetwork      *armIDRef     `json:"remoteVirtualNetwork,omitempty"`
	RemoteAddressSpace        *addressSpace `json:"remoteAddressSpace,omitempty"`
	AllowVirtualNetworkAccess bool          `json:"allowVirtualNetworkAccess"`
	AllowForwardedTraffic     bool          `json:"allowForwardedTraffic"`
	AllowGatewayTransit       bool          `json:"allowGatewayTransit"`
	UseRemoteGateways         bool          `json:"useRemoteGateways"`
}

type vnetPeeringListResponse struct {
	Value []vnetPeeringResponse `json:"value"`
}

// RouteTables (RouteTablesClient / azurerm_route_table).

type routeTableRequest struct {
	Location   string                 `json:"location"`
	Tags       map[string]string      `json:"tags,omitempty"`
	Properties routeTableRequestProps `json:"properties,omitempty"`
}

type routeTableRequestProps struct {
	Routes []route `json:"routes,omitempty"`
}

type route struct {
	Name       string     `json:"name,omitempty"`
	ID         string     `json:"id,omitempty"`
	Properties routeProps `json:"properties,omitempty"`
}

type routeProps struct {
	AddressPrefix     string `json:"addressPrefix,omitempty"`
	NextHopType       string `json:"nextHopType,omitempty"`
	NextHopIPAddress  string `json:"nextHopIpAddress,omitempty"`
	ProvisioningState string `json:"provisioningState,omitempty"`
}

type routeTableResponse struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	Type       string                  `json:"type"`
	Location   string                  `json:"location"`
	Etag       string                  `json:"etag,omitempty"`
	Tags       map[string]string       `json:"tags,omitempty"`
	Properties routeTableResponseProps `json:"properties"`
}

type routeTableResponseProps struct {
	ProvisioningState string  `json:"provisioningState"`
	Routes            []route `json:"routes"`
	// Subnets is the read-only back-reference real ARM reports on a routeTables
	// GET once the route table is associated with a subnet (mirrors
	// nsgResponseProps.Subnets).
	Subnets []armIDRef `json:"subnets,omitempty"`
}

type routeTableListResponse struct {
	Value []routeTableResponse `json:"value"`
}
