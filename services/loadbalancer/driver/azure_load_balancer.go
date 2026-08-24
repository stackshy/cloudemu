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
	Name             string
	Protocol         string // "Tcp" or "Udp"
	FrontendPort     int
	BackendPort      int
	FrontendName     string
	BackendPoolName  string
	ProbeName        string
	EnableFloatingIP bool
	IdleTimeoutMin   int
	LoadDistribution string
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
	Tags              map[string]string
	ProvisioningState string
	ETag              string
}

// AzureLoadBalancers is an OPTIONAL, type-asserted capability. The Azure
// provider stores ARM load balancers natively, keyed by (resourceGroup, name).
// CreateOrUpdate is a full replace: children absent from the payload are
// removed. Nil resource group on List means subscription-wide.
type AzureLoadBalancers interface {
	CreateOrUpdateAzureLoadBalancer(ctx context.Context, rg, name string, lb AzureLoadBalancer) (*AzureLoadBalancer, error)
	GetAzureLoadBalancer(ctx context.Context, rg, name string) (*AzureLoadBalancer, error)
	DeleteAzureLoadBalancer(ctx context.Context, rg, name string) error
	ListAzureLoadBalancers(ctx context.Context, rg string) ([]AzureLoadBalancer, error)
}
