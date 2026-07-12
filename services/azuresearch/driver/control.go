package driver

import "context"

// ServiceConfig describes a search service to create or update.
type ServiceConfig struct {
	Name           string
	ResourceGroup  string
	Location       string
	SKUName        string // free | basic | standard | standard2 | standard3 | storage_optimized_l1 | ...
	ReplicaCount   int
	PartitionCount int
	HostingMode    string // default | highDensity
	Tags           map[string]string
}

// Service is a Microsoft.Search/searchServices resource.
type Service struct {
	ID                string
	Name              string
	ResourceGroup     string
	Location          string
	SKUName           string
	ReplicaCount      int
	PartitionCount    int
	HostingMode       string
	Endpoint          string
	Status            string
	ProvisioningState string
	Tags              map[string]string
	CreatedAt         string
}

// AdminKeys holds the primary/secondary admin keys for a service.
type AdminKeys struct {
	Primary   string
	Secondary string
}

// QueryKey is a read-only query key for a service.
type QueryKey struct {
	Name string
	Key  string
}

// SharedPrivateLink is a shared private-link resource under a service.
type SharedPrivateLink struct {
	ID                string
	Name              string
	GroupID           string
	PrivateLinkID     string
	RequestMessage    string
	Status            string // Pending | Approved | Rejected | Disconnected
	ProvisioningState string
}

// PrivateEndpointConnection is a private-endpoint connection under a service.
type PrivateEndpointConnection struct {
	ID                string
	Name              string
	Status            string
	Description       string
	ProvisioningState string
}

// SearchControl is the Microsoft.Search ARM control-plane surface.
type SearchControl interface {
	CreateService(ctx context.Context, cfg ServiceConfig) (*Service, error)
	GetService(ctx context.Context, resourceGroup, name string) (*Service, error)
	DeleteService(ctx context.Context, resourceGroup, name string) error
	UpdateService(ctx context.Context, resourceGroup, name string, replicas, partitions int, tags map[string]string) (*Service, error)
	ListServicesByResourceGroup(ctx context.Context, resourceGroup string) ([]Service, error)
	ListServices(ctx context.Context) ([]Service, error)

	ListAdminKeys(ctx context.Context, resourceGroup, name string) (*AdminKeys, error)
	RegenerateAdminKey(ctx context.Context, resourceGroup, name, which string) (*AdminKeys, error)
	ListQueryKeys(ctx context.Context, resourceGroup, name string) ([]QueryKey, error)
	CreateQueryKey(ctx context.Context, resourceGroup, name, keyName string) (*QueryKey, error)
	DeleteQueryKey(ctx context.Context, resourceGroup, name, key string) error

	PutSharedPrivateLink(ctx context.Context, resourceGroup, name, linkName, groupID, privateLinkID string) (*SharedPrivateLink, error)
	GetSharedPrivateLink(ctx context.Context, resourceGroup, name, linkName string) (*SharedPrivateLink, error)
	DeleteSharedPrivateLink(ctx context.Context, resourceGroup, name, linkName string) error
	ListSharedPrivateLinks(ctx context.Context, resourceGroup, name string) ([]SharedPrivateLink, error)

	PutPrivateEndpointConnection(ctx context.Context, resourceGroup, name, connName, status string) (*PrivateEndpointConnection, error)
	GetPrivateEndpointConnection(ctx context.Context, resourceGroup, name, connName string) (*PrivateEndpointConnection, error)
	DeletePrivateEndpointConnection(ctx context.Context, resourceGroup, name, connName string) error
	ListPrivateEndpointConnections(ctx context.Context, resourceGroup, name string) ([]PrivateEndpointConnection, error)
}
