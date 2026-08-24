package loadbalancer

// Azure ARM JSON wire structures for Microsoft.Network/loadBalancers. Only the
// subset of fields the handler models is declared; the real armnetwork
// LoadBalancer type carries far more, but its JSON unmarshaler ignores unknown
// members and treats absent members as nil.

const (
	providerName = "Microsoft.Network"
	typeLBs      = "loadBalancers"

	lbResourceType    = "Microsoft.Network/loadBalancers"
	poolResourceType  = "Microsoft.Network/loadBalancers/backendAddressPools"
	ruleResourceType  = "Microsoft.Network/loadBalancers/loadBalancingRules"
	feResourceType    = "Microsoft.Network/loadBalancers/frontendIPConfigurations"
	probeResourceType = "Microsoft.Network/loadBalancers/probes"

	defaultLBLocation = "eastus"

	// provisioningStateSucceeded is the terminal state the SDK poller waits for.
	provisioningStateSucceeded = "Succeeded"

	// SKU defaults Azure applies when a load balancer omits its sku.
	defaultSKUName = "Standard"
	defaultSKUTier = "Regional"

	protocolTCP = "Tcp"
)

// subResource is Azure's {"id": "..."} reference shape.
type subResource struct {
	ID string `json:"id,omitempty"`
}

// --- backend address pools (→ pool names) ---

type backendPoolProps struct {
	ProvisioningState string `json:"provisioningState,omitempty"`
}

type backendPoolJSON struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	Properties *backendPoolProps `json:"properties,omitempty"`
}

// --- frontend IP configurations ---

type frontendIPProps struct {
	PrivateIPAddress          string       `json:"privateIPAddress,omitempty"`
	PrivateIPAllocationMethod string       `json:"privateIPAllocationMethod,omitempty"`
	Subnet                    *subResource `json:"subnet,omitempty"`
	PublicIPAddress           *subResource `json:"publicIPAddress,omitempty"`
	ProvisioningState         string       `json:"provisioningState,omitempty"`
}

type frontendIPJSON struct {
	ID         string           `json:"id,omitempty"`
	Name       string           `json:"name,omitempty"`
	Type       string           `json:"type,omitempty"`
	Etag       string           `json:"etag,omitempty"`
	Properties *frontendIPProps `json:"properties,omitempty"`
}

// --- load balancing rules ---

type loadBalancingRuleProps struct {
	Protocol                string       `json:"protocol,omitempty"`
	FrontendPort            int32        `json:"frontendPort,omitempty"`
	BackendPort             int32        `json:"backendPort,omitempty"`
	FrontendIPConfiguration *subResource `json:"frontendIPConfiguration,omitempty"`
	BackendAddressPool      *subResource `json:"backendAddressPool,omitempty"`
	Probe                   *subResource `json:"probe,omitempty"`
	EnableFloatingIP        *bool        `json:"enableFloatingIP,omitempty"`
	IdleTimeoutInMinutes    int32        `json:"idleTimeoutInMinutes,omitempty"`
	LoadDistribution        string       `json:"loadDistribution,omitempty"`
	ProvisioningState       string       `json:"provisioningState,omitempty"`
}

type loadBalancingRuleJSON struct {
	ID         string                  `json:"id,omitempty"`
	Name       string                  `json:"name,omitempty"`
	Type       string                  `json:"type,omitempty"`
	Etag       string                  `json:"etag,omitempty"`
	Properties *loadBalancingRuleProps `json:"properties,omitempty"`
}

// --- probes ---

type probeProps struct {
	Protocol          string `json:"protocol,omitempty"`
	Port              int32  `json:"port,omitempty"`
	RequestPath       string `json:"requestPath,omitempty"`
	IntervalInSeconds int32  `json:"intervalInSeconds,omitempty"`
	NumberOfProbes    int32  `json:"numberOfProbes,omitempty"`
	ProvisioningState string `json:"provisioningState,omitempty"`
}

type probeJSON struct {
	ID         string      `json:"id,omitempty"`
	Name       string      `json:"name,omitempty"`
	Type       string      `json:"type,omitempty"`
	Etag       string      `json:"etag,omitempty"`
	Properties *probeProps `json:"properties,omitempty"`
}

// --- load balancer ---

type loadBalancerProps struct {
	BackendAddressPools      []backendPoolJSON       `json:"backendAddressPools,omitempty"`
	FrontendIPConfigurations []frontendIPJSON        `json:"frontendIPConfigurations,omitempty"`
	LoadBalancingRules       []loadBalancingRuleJSON `json:"loadBalancingRules,omitempty"`
	Probes                   []probeJSON             `json:"probes,omitempty"`
	ProvisioningState        string                  `json:"provisioningState,omitempty"`
}

type lbSKU struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

type loadBalancerJSON struct {
	ID         string             `json:"id,omitempty"`
	Name       string             `json:"name,omitempty"`
	Type       string             `json:"type,omitempty"`
	Location   string             `json:"location,omitempty"`
	Etag       string             `json:"etag,omitempty"`
	SKU        *lbSKU             `json:"sku,omitempty"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Properties *loadBalancerProps `json:"properties,omitempty"`
}

type lbListResult struct {
	Value []loadBalancerJSON `json:"value"`
}
