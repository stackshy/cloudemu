package driver

import "context"

// The Azure Load Balancer (Microsoft.Network/loadBalancers) is a full-replace
// nested resource whose frontend IP configurations, backend pools, load-
// balancing rules and health probes carry per-child names, ports and cross-
// references that the AWS-shaped LoadBalancer / TargetGroup / Listener model
// cannot represent. Rather than distort the cross-cloud interface, the Azure
// provider stores the ARM load balancer natively and exposes it through
// AzureLoadBalancers, an OPTIONAL capability discovered by type assertion (the
// same pattern as AzureNetworkInterfaces / AzureNetworkMetadata). AWS and GCP
// do not implement it.

// AzureLBFrontend is one frontendIPConfiguration on an Azure load balancer.
type AzureLBFrontend struct {
	Name             string
	PrivateIPAddress string
	AllocationMethod string // "Static" or "Dynamic"
	SubnetID         string
	PublicIPID       string
}

// AzureLBProbe is one health probe on an Azure load balancer.
type AzureLBProbe struct {
	Name              string
	Protocol          string // "Http", "Https", "Tcp"
	Port              int
	RequestPath       string
	IntervalInSeconds int
	NumberOfProbes    int
}

// AzureLBRule is one loadBalancingRule, keeping its own name and independent
// frontend/backend ports plus references to the frontend, backend pool and
// probe it binds (stored by child name, resolved to ids on read).
type AzureLBRule struct {
	Name                string
	Protocol            string // "Tcp" or "Udp"
	FrontendPort        int
	BackendPort         int
	FrontendName        string
	BackendPoolName     string
	ProbeName           string
	EnableFloatingIP    bool
	DisableOutboundSnat bool
	IdleTimeoutMin      int
	LoadDistribution    string
}

// AzureLBNatRule is one inboundNatRule: a load balancer sub-resource that
// forwards a single frontend port to a single backend port on one instance.
// Unlike AzureLBRule/AzureLBProbe, InboundNatRules is a real ARM operation
// group with its own standalone CreateOrUpdate/Delete/Get/List, so it is
// mutated independently rather than only through the whole-LB PUT.
type AzureLBNatRule struct {
	Name             string
	Protocol         string // "Tcp" or "Udp"
	FrontendPort     int
	BackendPort      int
	FrontendName     string
	EnableFloatingIP bool
	IdleTimeoutMin   int
}

// AzureLBNatPool is one inboundNatPool: a frontend port-range mapping used by
// VM Scale Set NAT (e.g. one RDP/SSH port per instance). ARM exposes it only
// nested inside the load balancer's properties; there is no standalone
// inboundNatPools operation group.
type AzureLBNatPool struct {
	Name                   string
	Protocol               string // "Tcp" or "Udp"
	FrontendPortRangeStart int
	FrontendPortRangeEnd   int
	BackendPort            int
	FrontendName           string
}

// AzureLBOutboundRule is one outboundRule: explicit SNAT configuration for a
// backend pool through one or more frontend IPs. ARM exposes it only nested
// inside the load balancer's properties; there is no standalone outboundRules
// operation group.
type AzureLBOutboundRule struct {
	Name                   string
	Protocol               string // "Tcp", "Udp", or "All"
	BackendPoolName        string
	FrontendNames          []string
	AllocatedOutboundPorts int
	IdleTimeoutMin         int
}

// AzureLoadBalancer is the natively-stored ARM load balancer.
type AzureLoadBalancer struct {
	Name              string
	ResourceGroup     string
	Location          string
	SKUName           string // "Basic", "Standard", "Gateway"
	SKUTier           string // "Regional", "Global"
	Frontends         []AzureLBFrontend
	BackendPools      []string // pool names
	Rules             []AzureLBRule
	Probes            []AzureLBProbe
	NatRules          []AzureLBNatRule
	NatPools          []AzureLBNatPool
	OutboundRules     []AzureLBOutboundRule
	Tags              map[string]string
	ProvisioningState string
	ETag              string
}

// AzureLoadBalancers is an OPTIONAL, type-asserted capability. The Azure
// provider stores ARM load balancers natively, keyed by (resourceGroup, name).
// CreateOrUpdate is a full replace: children absent from the payload are
// removed. Nil resource group on List means subscription-wide.
//
// backendAddressPools and inboundNatRules also have real standalone ARM
// operation groups (BeginCreateOrUpdate / BeginDelete addressing one named
// child), so the Upsert/Delete methods below let the wire handler mutate a
// single child in place without touching its siblings — the whole-LB
// CreateOrUpdate above would wipe every other child, and DeleteAzureLoadBalancer
// would remove the entire parent. probes, loadBalancingRules,
// frontendIPConfigurations, inboundNatPools and outboundRules have no
// standalone create/delete in real ARM (Get/List only), so they are mutated
// exclusively through CreateOrUpdateAzureLoadBalancer.
type AzureLoadBalancers interface {
	CreateOrUpdateAzureLoadBalancer(ctx context.Context, rg, name string, lb AzureLoadBalancer) (*AzureLoadBalancer, error)
	GetAzureLoadBalancer(ctx context.Context, rg, name string) (*AzureLoadBalancer, error)
	DeleteAzureLoadBalancer(ctx context.Context, rg, name string) error
	ListAzureLoadBalancers(ctx context.Context, rg string) ([]AzureLoadBalancer, error)

	// UpsertAzureLBBackendPool adds poolName to the load balancer's backend
	// pools if absent, leaving every other frontend/pool/rule/probe/NAT rule
	// untouched. Returns NotFound if the parent load balancer does not exist.
	UpsertAzureLBBackendPool(ctx context.Context, rg, name, poolName string) (*AzureLoadBalancer, error)
	// DeleteAzureLBBackendPool removes a single backend pool by name, leaving
	// every other child untouched. Returns NotFound if the parent load
	// balancer or the pool itself does not exist, and FailedPrecondition if a
	// load balancing rule or outbound rule on the load balancer still
	// references the pool.
	DeleteAzureLBBackendPool(ctx context.Context, rg, name, poolName string) error

	// UpsertAzureLBNatRule creates or replaces a single inbound NAT rule by
	// name, leaving every other child untouched. Returns InvalidArgument if
	// the rule references a frontend IP configuration that does not exist on
	// the load balancer, and NotFound if the parent load balancer does not
	// exist.
	UpsertAzureLBNatRule(ctx context.Context, rg, name, natRuleName string, rule AzureLBNatRule) (*AzureLoadBalancer, error)
	// DeleteAzureLBNatRule removes a single inbound NAT rule by name, leaving
	// every other child untouched. Returns NotFound if the parent load
	// balancer or the rule itself does not exist.
	DeleteAzureLBNatRule(ctx context.Context, rg, name, natRuleName string) error
}
