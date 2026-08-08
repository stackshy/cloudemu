package vcn

// OCI Core Networking REST shapes.

// definedTags is OCI's namespaced tag map. CloudEmu does not model tag
// namespaces, so it is echoed back empty.
type definedTags map[string]map[string]any

// resourceRequest carries the fields every Core Networking create and update
// body shares.
type resourceRequest struct {
	CompartmentID string            `json:"compartmentId,omitempty"`
	DisplayName   string            `json:"displayName,omitempty"`
	FreeformTags  map[string]string `json:"freeformTags,omitempty"`
	DefinedTags   definedTags       `json:"definedTags,omitempty"`
}

type changeCompartmentRequest struct {
	CompartmentID string `json:"compartmentId"`
}

type vcnRequest struct {
	resourceRequest

	CIDRBlock  string   `json:"cidrBlock,omitempty"`
	CIDRBlocks []string `json:"cidrBlocks,omitempty"`
	DNSLabel   string   `json:"dnsLabel,omitempty"`
}

type vcnResponse struct {
	ID                    string            `json:"id"`
	CompartmentID         string            `json:"compartmentId"`
	CIDRBlock             string            `json:"cidrBlock"`
	CIDRBlocks            []string          `json:"cidrBlocks"`
	DisplayName           string            `json:"displayName,omitempty"`
	DNSLabel              string            `json:"dnsLabel,omitempty"`
	VCNDomainName         string            `json:"vcnDomainName,omitempty"`
	DefaultRouteTableID   string            `json:"defaultRouteTableId,omitempty"`
	DefaultSecurityListID string            `json:"defaultSecurityListId,omitempty"`
	DefaultDHCPOptionsID  string            `json:"defaultDhcpOptionsId,omitempty"`
	LifecycleState        string            `json:"lifecycleState"`
	TimeCreated           string            `json:"timeCreated,omitempty"`
	FreeformTags          map[string]string `json:"freeformTags"`
	DefinedTags           definedTags       `json:"definedTags"`
}

type subnetRequest struct {
	resourceRequest

	VCNID                  string   `json:"vcnId,omitempty"`
	CIDRBlock              string   `json:"cidrBlock,omitempty"`
	AvailabilityDomain     string   `json:"availabilityDomain,omitempty"`
	DNSLabel               string   `json:"dnsLabel,omitempty"`
	RouteTableID           string   `json:"routeTableId,omitempty"`
	SecurityListIDs        []string `json:"securityListIds,omitempty"`
	DHCPOptionsID          string   `json:"dhcpOptionsId,omitempty"`
	ProhibitPublicIPOnVnic *bool    `json:"prohibitPublicIpOnVnic,omitempty"`
}

type subnetResponse struct {
	ID                     string            `json:"id"`
	CompartmentID          string            `json:"compartmentId"`
	VCNID                  string            `json:"vcnId"`
	CIDRBlock              string            `json:"cidrBlock"`
	DisplayName            string            `json:"displayName,omitempty"`
	DNSLabel               string            `json:"dnsLabel,omitempty"`
	SubnetDomainName       string            `json:"subnetDomainName,omitempty"`
	AvailabilityDomain     string            `json:"availabilityDomain,omitempty"`
	RouteTableID           string            `json:"routeTableId,omitempty"`
	SecurityListIDs        []string          `json:"securityListIds"`
	DHCPOptionsID          string            `json:"dhcpOptionsId,omitempty"`
	ProhibitPublicIPOnVnic bool              `json:"prohibitPublicIpOnVnic"`
	VirtualRouterIP        string            `json:"virtualRouterIp,omitempty"`
	LifecycleState         string            `json:"lifecycleState"`
	TimeCreated            string            `json:"timeCreated,omitempty"`
	FreeformTags           map[string]string `json:"freeformTags"`
	DefinedTags            definedTags       `json:"definedTags"`
}

type nsgRequest struct {
	resourceRequest

	VCNID string `json:"vcnId,omitempty"`
}

type nsgResponse struct {
	ID             string            `json:"id"`
	CompartmentID  string            `json:"compartmentId"`
	VCNID          string            `json:"vcnId"`
	DisplayName    string            `json:"displayName,omitempty"`
	LifecycleState string            `json:"lifecycleState"`
	TimeCreated    string            `json:"timeCreated,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags"`
	DefinedTags    definedTags       `json:"definedTags"`
}

// portRange is OCI's inclusive port window.
type portRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// protocolOptions carries the port windows a TCP or UDP rule applies to.
type protocolOptions struct {
	DestinationPortRange *portRange `json:"destinationPortRange,omitempty"`
	SourcePortRange      *portRange `json:"sourcePortRange,omitempty"`
}

// securityRule serves both NSG rules, which name a direction, and security
// list rules, which are split into ingress and egress arrays.
type securityRule struct {
	ID              string           `json:"id,omitempty"`
	Direction       string           `json:"direction,omitempty"`
	Protocol        string           `json:"protocol"`
	Source          string           `json:"source,omitempty"`
	SourceType      string           `json:"sourceType,omitempty"`
	Destination     string           `json:"destination,omitempty"`
	DestinationType string           `json:"destinationType,omitempty"`
	IsStateless     bool             `json:"isStateless"`
	Description     string           `json:"description,omitempty"`
	TCPOptions      *protocolOptions `json:"tcpOptions,omitempty"`
	UDPOptions      *protocolOptions `json:"udpOptions,omitempty"`
}

type securityRulesRequest struct {
	SecurityRules []securityRule `json:"securityRules"`
}

type removeSecurityRulesRequest struct {
	SecurityRuleIDs []string `json:"securityRuleIds"`
}

type securityRulesResponse struct {
	SecurityRules []securityRule `json:"securityRules"`
}

type securityListRequest struct {
	resourceRequest

	VCNID                string         `json:"vcnId,omitempty"`
	IngressSecurityRules []securityRule `json:"ingressSecurityRules,omitempty"`
	EgressSecurityRules  []securityRule `json:"egressSecurityRules,omitempty"`
}

type securityListResponse struct {
	ID                   string            `json:"id"`
	CompartmentID        string            `json:"compartmentId"`
	VCNID                string            `json:"vcnId"`
	DisplayName          string            `json:"displayName,omitempty"`
	IngressSecurityRules []securityRule    `json:"ingressSecurityRules"`
	EgressSecurityRules  []securityRule    `json:"egressSecurityRules"`
	LifecycleState       string            `json:"lifecycleState"`
	TimeCreated          string            `json:"timeCreated,omitempty"`
	FreeformTags         map[string]string `json:"freeformTags"`
	DefinedTags          definedTags       `json:"definedTags"`
}

// routeRule is one entry in a route table.
type routeRule struct {
	Destination     string `json:"destination,omitempty"`
	CIDRBlock       string `json:"cidrBlock,omitempty"`
	DestinationType string `json:"destinationType,omitempty"`
	NetworkEntityID string `json:"networkEntityId"`
	Description     string `json:"description,omitempty"`
	RouteType       string `json:"routeType,omitempty"`
}

type routeTableRequest struct {
	resourceRequest

	VCNID      string      `json:"vcnId,omitempty"`
	RouteRules []routeRule `json:"routeRules,omitempty"`
}

type routeTableResponse struct {
	ID             string            `json:"id"`
	CompartmentID  string            `json:"compartmentId"`
	VCNID          string            `json:"vcnId"`
	DisplayName    string            `json:"displayName,omitempty"`
	RouteRules     []routeRule       `json:"routeRules"`
	LifecycleState string            `json:"lifecycleState"`
	TimeCreated    string            `json:"timeCreated,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags"`
	DefinedTags    definedTags       `json:"definedTags"`
}

type gatewayRequest struct {
	resourceRequest

	VCNID        string `json:"vcnId,omitempty"`
	IsEnabled    *bool  `json:"isEnabled,omitempty"`
	BlockTraffic *bool  `json:"blockTraffic,omitempty"`
	RouteTableID string `json:"routeTableId,omitempty"`
	PublicIPID   string `json:"publicIpId,omitempty"`
}

type internetGatewayResponse struct {
	ID             string            `json:"id"`
	CompartmentID  string            `json:"compartmentId"`
	VCNID          string            `json:"vcnId"`
	DisplayName    string            `json:"displayName,omitempty"`
	IsEnabled      bool              `json:"isEnabled"`
	LifecycleState string            `json:"lifecycleState"`
	TimeCreated    string            `json:"timeCreated,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags"`
	DefinedTags    definedTags       `json:"definedTags"`
}

type natGatewayResponse struct {
	ID             string            `json:"id"`
	CompartmentID  string            `json:"compartmentId"`
	VCNID          string            `json:"vcnId"`
	DisplayName    string            `json:"displayName,omitempty"`
	NatIP          string            `json:"natIp"`
	BlockTraffic   bool              `json:"blockTraffic"`
	LifecycleState string            `json:"lifecycleState"`
	TimeCreated    string            `json:"timeCreated,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags"`
	DefinedTags    definedTags       `json:"definedTags"`
}

// gatewayService is the Oracle Services Network entry a service gateway fronts.
type gatewayService struct {
	ServiceID   string `json:"serviceId"`
	ServiceName string `json:"serviceName,omitempty"`
}

type serviceGatewayRequest struct {
	resourceRequest

	VCNID        string           `json:"vcnId,omitempty"`
	Services     []gatewayService `json:"services,omitempty"`
	RouteTableID string           `json:"routeTableId,omitempty"`
	BlockTraffic *bool            `json:"blockTraffic,omitempty"`
}

type serviceGatewayResponse struct {
	ID             string            `json:"id"`
	CompartmentID  string            `json:"compartmentId"`
	VCNID          string            `json:"vcnId"`
	DisplayName    string            `json:"displayName,omitempty"`
	Services       []gatewayService  `json:"services"`
	RouteTableID   string            `json:"routeTableId,omitempty"`
	BlockTraffic   bool              `json:"blockTraffic"`
	LifecycleState string            `json:"lifecycleState"`
	TimeCreated    string            `json:"timeCreated,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags"`
	DefinedTags    definedTags       `json:"definedTags"`
}

// dhcpOption is one entry in a DHCP options set. OCI discriminates on type.
type dhcpOption struct {
	Type              string   `json:"type"`
	ServerType        string   `json:"serverType,omitempty"`
	CustomDNSServers  []string `json:"customDnsServers,omitempty"`
	SearchDomainNames []string `json:"searchDomainNames,omitempty"`
}

type dhcpRequest struct {
	resourceRequest

	VCNID   string       `json:"vcnId,omitempty"`
	Options []dhcpOption `json:"options,omitempty"`
}

type dhcpResponse struct {
	ID             string            `json:"id"`
	CompartmentID  string            `json:"compartmentId"`
	VCNID          string            `json:"vcnId"`
	DisplayName    string            `json:"displayName,omitempty"`
	Options        []dhcpOption      `json:"options"`
	LifecycleState string            `json:"lifecycleState"`
	TimeCreated    string            `json:"timeCreated,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags"`
	DefinedTags    definedTags       `json:"definedTags"`
}

type vnicRequest struct {
	DisplayName   *string  `json:"displayName,omitempty"`
	HostnameLabel *string  `json:"hostnameLabel,omitempty"`
	NSGIDs        []string `json:"nsgIds,omitempty"`
}

type vnicResponse struct {
	ID                  string   `json:"id"`
	CompartmentID       string   `json:"compartmentId"`
	SubnetID            string   `json:"subnetId"`
	DisplayName         string   `json:"displayName,omitempty"`
	HostnameLabel       string   `json:"hostnameLabel,omitempty"`
	PrivateIP           string   `json:"privateIp,omitempty"`
	PublicIP            string   `json:"publicIp,omitempty"`
	MacAddress          string   `json:"macAddress,omitempty"`
	IsPrimary           bool     `json:"isPrimary"`
	SkipSourceDestCheck bool     `json:"skipSourceDestCheck"`
	NSGIDs              []string `json:"nsgIds"`
	LifecycleState      string   `json:"lifecycleState"`
	TimeCreated         string   `json:"timeCreated,omitempty"`
}

type privateIPRequest struct {
	VNICID        string  `json:"vnicId,omitempty"`
	IPAddress     string  `json:"ipAddress,omitempty"`
	DisplayName   *string `json:"displayName,omitempty"`
	HostnameLabel *string `json:"hostnameLabel,omitempty"`
}

type privateIPResponse struct {
	ID            string `json:"id"`
	CompartmentID string `json:"compartmentId"`
	SubnetID      string `json:"subnetId"`
	VNICID        string `json:"vnicId"`
	IPAddress     string `json:"ipAddress"`
	DisplayName   string `json:"displayName,omitempty"`
	HostnameLabel string `json:"hostnameLabel,omitempty"`
	IsPrimary     bool   `json:"isPrimary"`
	TimeCreated   string `json:"timeCreated,omitempty"`
}

type publicIPRequest struct {
	resourceRequest

	Lifetime    string `json:"lifetime,omitempty"`
	PrivateIPID string `json:"privateIpId,omitempty"`
}

type publicIPResponse struct {
	ID                 string            `json:"id"`
	CompartmentID      string            `json:"compartmentId"`
	IPAddress          string            `json:"ipAddress"`
	DisplayName        string            `json:"displayName,omitempty"`
	Lifetime           string            `json:"lifetime"`
	Scope              string            `json:"scope"`
	AssignedEntityID   string            `json:"assignedEntityId,omitempty"`
	AssignedEntityType string            `json:"assignedEntityType,omitempty"`
	PrivateIPID        string            `json:"privateIpId,omitempty"`
	LifecycleState     string            `json:"lifecycleState"`
	TimeCreated        string            `json:"timeCreated,omitempty"`
	FreeformTags       map[string]string `json:"freeformTags"`
	DefinedTags        definedTags       `json:"definedTags"`
}
