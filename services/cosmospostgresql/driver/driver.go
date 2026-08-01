// Package driver defines the portable interface for Azure Cosmos DB for
// PostgreSQL (Microsoft.DBforPostgreSQL/serverGroupsv2), the Citus-based
// distributed-Postgres offering. It is control-plane only — server-group
// clusters and their firewall rules, roles, nodes, configurations, and private
// endpoints — so it is independent of the relational/database drivers.
package driver

import "context"

// Provisioning states reported on resources.
const (
	ProvisioningSucceeded = "Succeeded"
	ProvisioningCanceled  = "Canceled"
	ProvisioningFailed    = "Failed"
)

// Server roles within a cluster.
const (
	RoleCoordinator = "Coordinator"
	RoleWorker      = "Worker"
)

// Cluster is a Cosmos DB for PostgreSQL server group (serverGroupsv2).
type Cluster struct {
	Name              string
	ResourceGroup     string
	Location          string
	Tags              map[string]string
	ProvisioningState string
	State             string

	AdministratorLogin              string
	CitusVersion                    string
	PostgresqlVersion               string
	CoordinatorServerEdition        string
	CoordinatorVCores               int
	CoordinatorStorageQuotaInMb     int
	CoordinatorEnablePublicIPAccess bool
	EnableShardsOnCoordinator       bool
	NodeServerEdition               string
	NodeCount                       int
	NodeVCores                      int
	NodeStorageQuotaInMb            int
	NodeEnablePublicIPAccess        bool
	EnableHa                        bool
	PreferredPrimaryZone            string
	MaintenanceWindow               *MaintenanceWindow

	// Read-replica linkage. SourceResourceID/SourceLocation are set on a replica;
	// ReadReplicas lists the replicas of a primary.
	SourceResourceID string
	SourceLocation   string
	ReadReplicas     []string
}

// MaintenanceWindow is the weekly maintenance schedule.
type MaintenanceWindow struct {
	CustomWindow string
	DayOfWeek    int
	StartHour    int
	StartMinute  int
}

// FirewallRule is an IP allow-list entry on a cluster.
type FirewallRule struct {
	Name              string
	ClusterName       string
	ResourceGroup     string
	ProvisioningState string
	StartIPAddress    string
	EndIPAddress      string
}

// Role is a Postgres role provisioned on a cluster.
type Role struct {
	Name              string
	ClusterName       string
	ResourceGroup     string
	ProvisioningState string
}

// Server is a node (coordinator or worker) within a cluster. Nodes are derived
// from the cluster's shape and are read-only.
type Server struct {
	Name                     string
	ClusterName              string
	ResourceGroup            string
	Role                     string
	State                    string
	HaState                  string
	FullyQualifiedDomainName string
	AdministratorLogin       string
	ServerEdition            string
	VCores                   int
	StorageQuotaInMb         int
	CitusVersion             string
	PostgresqlVersion        string
	EnableHa                 bool
	EnablePublicIPAccess     bool
	IsReadOnly               bool
}

// RoleGroupValue is one role's value for a cluster-wide configuration.
type RoleGroupValue struct {
	Role         string
	Value        string
	DefaultValue string
	Source       string
}

// Configuration is a cluster-wide server parameter with per-role values.
type Configuration struct {
	Name              string
	ClusterName       string
	ResourceGroup     string
	ProvisioningState string
	Description       string
	DataType          string
	AllowedValues     string
	RequiresRestart   bool
	RoleGroups        []RoleGroupValue
}

// ServerConfiguration is a single server-scoped parameter value (coordinator or
// node role group).
type ServerConfiguration struct {
	Name              string
	ClusterName       string
	ResourceGroup     string
	ServerName        string
	ProvisioningState string
	Value             string
	DefaultValue      string
	Description       string
	DataType          string
	AllowedValues     string
	Source            string
	RequiresRestart   bool
}

// PrivateEndpointConnection is a private-endpoint connection on a cluster.
type PrivateEndpointConnection struct {
	Name              string
	ClusterName       string
	ResourceGroup     string
	ProvisioningState string
	GroupIDs          []string
	PrivateEndpointID string
	ConnectionStatus  string
	ConnectionDesc    string
	ActionsRequired   string
}

// PrivateLinkResource is a private-link resource (group) exposed by a cluster.
type PrivateLinkResource struct {
	Name              string
	ClusterName       string
	ResourceGroup     string
	GroupID           string
	RequiredMembers   []string
	RequiredZoneNames []string
}

// NameAvailability is the result of a CheckNameAvailability call.
type NameAvailability struct {
	Name          string
	Type          string
	NameAvailable bool
	Message       string
}

// CreateClusterConfig is the input to CreateOrUpdateCluster.
type CreateClusterConfig struct {
	Name                            string
	ResourceGroup                   string
	Location                        string
	Tags                            map[string]string
	AdministratorLoginPassword      string
	CitusVersion                    string
	PostgresqlVersion               string
	CoordinatorServerEdition        string
	CoordinatorVCores               int
	CoordinatorStorageQuotaInMb     int
	CoordinatorEnablePublicIPAccess bool
	EnableShardsOnCoordinator       bool
	NodeServerEdition               string
	NodeCount                       int
	NodeVCores                      int
	NodeStorageQuotaInMb            int
	NodeEnablePublicIPAccess        bool
	EnableHa                        bool
	PreferredPrimaryZone            string
	MaintenanceWindow               *MaintenanceWindow
	SourceResourceID                string
	SourceLocation                  string
}

// ClusterPatch carries the mutable fields of an UpdateCluster (PATCH). Nil
// pointers leave the field unchanged.
type ClusterPatch struct {
	Tags                        map[string]string
	AdministratorLoginPassword  *string
	CitusVersion                *string
	PostgresqlVersion           *string
	CoordinatorServerEdition    *string
	CoordinatorVCores           *int
	CoordinatorStorageQuotaInMb *int
	NodeServerEdition           *string
	NodeCount                   *int
	NodeVCores                  *int
	NodeStorageQuotaInMb        *int
	EnableHa                    *bool
	PreferredPrimaryZone        *string
	MaintenanceWindow           *MaintenanceWindow
}

// CreateFirewallRuleConfig is the input to CreateOrUpdateFirewallRule.
type CreateFirewallRuleConfig struct {
	ResourceGroup  string
	ClusterName    string
	Name           string
	StartIPAddress string
	EndIPAddress   string
}

// CreateRoleConfig is the input to CreateRole.
type CreateRoleConfig struct {
	ResourceGroup string
	ClusterName   string
	Name          string
	Password      string
}

// CosmosPostgreSQL is the Azure Cosmos DB for PostgreSQL control plane.
//
//nolint:interfacebloat // mirrors the Microsoft.DBforPostgreSQL/serverGroupsv2 surface.
type CosmosPostgreSQL interface {
	// Clusters (server groups).
	CreateOrUpdateCluster(ctx context.Context, cfg CreateClusterConfig) (*Cluster, error)
	GetCluster(ctx context.Context, resourceGroup, name string) (*Cluster, error)
	ListClustersByResourceGroup(ctx context.Context, resourceGroup string) ([]Cluster, error)
	ListClustersBySubscription(ctx context.Context) ([]Cluster, error)
	UpdateCluster(ctx context.Context, resourceGroup, name string, patch ClusterPatch) (*Cluster, error)
	DeleteCluster(ctx context.Context, resourceGroup, name string) error
	RestartCluster(ctx context.Context, resourceGroup, name string) error
	StartCluster(ctx context.Context, resourceGroup, name string) error
	StopCluster(ctx context.Context, resourceGroup, name string) error
	PromoteReadReplica(ctx context.Context, resourceGroup, name string) error
	CheckNameAvailability(ctx context.Context, name, typ string) (*NameAvailability, error)

	// Firewall rules.
	CreateOrUpdateFirewallRule(ctx context.Context, cfg CreateFirewallRuleConfig) (*FirewallRule, error)
	GetFirewallRule(ctx context.Context, resourceGroup, cluster, name string) (*FirewallRule, error)
	ListFirewallRules(ctx context.Context, resourceGroup, cluster string) ([]FirewallRule, error)
	DeleteFirewallRule(ctx context.Context, resourceGroup, cluster, name string) error

	// Roles.
	CreateRole(ctx context.Context, cfg CreateRoleConfig) (*Role, error)
	GetRole(ctx context.Context, resourceGroup, cluster, name string) (*Role, error)
	ListRoles(ctx context.Context, resourceGroup, cluster string) ([]Role, error)
	DeleteRole(ctx context.Context, resourceGroup, cluster, name string) error

	// Servers (nodes, read-only).
	GetServer(ctx context.Context, resourceGroup, cluster, name string) (*Server, error)
	ListServers(ctx context.Context, resourceGroup, cluster string) ([]Server, error)

	// Configurations.
	ListConfigurations(ctx context.Context, resourceGroup, cluster string) ([]Configuration, error)
	GetConfiguration(ctx context.Context, resourceGroup, cluster, name string) (*Configuration, error)
	GetCoordinatorConfiguration(ctx context.Context, resourceGroup, cluster, name string) (*ServerConfiguration, error)
	GetNodeConfiguration(ctx context.Context, resourceGroup, cluster, name string) (*ServerConfiguration, error)
	ListServerConfigurations(ctx context.Context, resourceGroup, cluster, server string) ([]ServerConfiguration, error)
	UpdateCoordinatorConfiguration(ctx context.Context, resourceGroup, cluster, name, value string) (*ServerConfiguration, error)
	UpdateNodeConfiguration(ctx context.Context, resourceGroup, cluster, name, value string) (*ServerConfiguration, error)

	// Private endpoints / links.
	CreateOrUpdatePrivateEndpointConnection(
		ctx context.Context, resourceGroup, cluster, name, status, description string,
	) (*PrivateEndpointConnection, error)
	GetPrivateEndpointConnection(ctx context.Context, resourceGroup, cluster, name string) (*PrivateEndpointConnection, error)
	ListPrivateEndpointConnections(ctx context.Context, resourceGroup, cluster string) ([]PrivateEndpointConnection, error)
	DeletePrivateEndpointConnection(ctx context.Context, resourceGroup, cluster, name string) error
	GetPrivateLinkResource(ctx context.Context, resourceGroup, cluster, name string) (*PrivateLinkResource, error)
	ListPrivateLinkResources(ctx context.Context, resourceGroup, cluster string) ([]PrivateLinkResource, error)
}
