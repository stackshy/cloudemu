// Package driver defines the portable interface for Azure Managed Instance for
// Apache Cassandra (Microsoft.DocumentDB/cassandraClusters). It is control-plane
// only — clusters and their datacenters — so it is independent of the
// relational/database drivers.
package driver

import "context"

// Managed Cassandra provisioning states.
const (
	ProvisioningSucceeded = "Succeeded"
	ProvisioningCreating  = "Creating"
	ProvisioningUpdating  = "Updating"
	ProvisioningDeleting  = "Deleting"
	ProvisioningCanceled  = "Canceled"
	ProvisioningFailed    = "Failed"
)

// Cluster is a managed Cassandra cluster.
type Cluster struct {
	Name                         string
	ResourceGroup                string
	Location                     string
	Tags                         map[string]string
	ProvisioningState            string
	CassandraVersion             string
	ClusterNameOverride          string
	DelegatedManagementSubnetID  string
	AuthenticationMethod         string
	HoursBetweenBackups          int
	RepairEnabled                bool
	Deallocated                  bool
	CassandraAuditLoggingEnabled bool
	ExternalSeedNodes            []string
	SeedNodes                    []string
	ClientCertificates           []string
	ExternalGossipCertificates   []string
	GossipCertificates           []string
	PrometheusEndpoint           string
}

// DataCenter is a datacenter within a managed Cassandra cluster.
type DataCenter struct {
	Name                               string
	ClusterName                        string
	ResourceGroup                      string
	ProvisioningState                  string
	DataCenterLocation                 string
	DelegatedSubnetID                  string
	NodeCount                          int
	DiskCapacity                       int
	SKU                                string
	DiskSKU                            string
	AvailabilityZone                   bool
	Base64EncodedCassandraYamlFragment string
	BackupStorageCustomerKeyURI        string
	ManagedDiskCustomerKeyURI          string
	SeedNodes                          []string
	Deallocated                        bool
}

// NodeStatus is one node's health within a datacenter, surfaced by ClusterStatus.
type NodeStatus struct {
	DataCenter string
	Address    string
	State      string
	Rack       string
	Load       string
}

// ClusterStatus is the aggregated health of a cluster's nodes.
type ClusterStatus struct {
	ClusterName  string
	ReaperStatus bool
	Nodes        []NodeStatus
}

// CreateClusterConfig is the input to CreateOrUpdateCluster.
type CreateClusterConfig struct {
	Name                          string
	ResourceGroup                 string
	Location                      string
	Tags                          map[string]string
	CassandraVersion              string
	ClusterNameOverride           string
	DelegatedManagementSubnetID   string
	InitialCassandraAdminPassword string
	AuthenticationMethod          string
	HoursBetweenBackups           int
	RepairEnabled                 bool
	CassandraAuditLoggingEnabled  bool
	ClientCertificates            []string
	ExternalGossipCertificates    []string
	ExternalSeedNodes             []string
}

// ClusterPatch carries the mutable fields of an UpdateCluster (PATCH) call.
// Nil pointers / nil slices leave the field unchanged.
type ClusterPatch struct {
	Tags                 map[string]string
	RepairEnabled        *bool
	HoursBetweenBackups  *int
	AuthenticationMethod *string
	ExternalSeedNodes    []string
	ClientCertificates   []string
}

// CreateDataCenterConfig is the input to CreateOrUpdateDataCenter.
type CreateDataCenterConfig struct {
	ClusterName                        string
	ResourceGroup                      string
	Name                               string
	DataCenterLocation                 string
	DelegatedSubnetID                  string
	NodeCount                          int
	DiskCapacity                       int
	SKU                                string
	DiskSKU                            string
	AvailabilityZone                   bool
	Base64EncodedCassandraYamlFragment string
	BackupStorageCustomerKeyURI        string
	ManagedDiskCustomerKeyURI          string
}

// DataCenterPatch carries the mutable fields of an UpdateDataCenter (PATCH).
type DataCenterPatch struct {
	NodeCount                          *int
	DiskCapacity                       *int
	Base64EncodedCassandraYamlFragment *string
}

// ManagedCassandra is the Azure Managed Cassandra control plane.
//
//nolint:interfacebloat // mirrors the Microsoft.DocumentDB cassandraClusters surface.
type ManagedCassandra interface {
	CreateOrUpdateCluster(ctx context.Context, cfg CreateClusterConfig) (*Cluster, error)
	GetCluster(ctx context.Context, resourceGroup, name string) (*Cluster, error)
	ListClustersByResourceGroup(ctx context.Context, resourceGroup string) ([]Cluster, error)
	ListClustersBySubscription(ctx context.Context) ([]Cluster, error)
	UpdateCluster(ctx context.Context, resourceGroup, name string, patch ClusterPatch) (*Cluster, error)
	DeleteCluster(ctx context.Context, resourceGroup, name string) error
	DeallocateCluster(ctx context.Context, resourceGroup, name string) (*Cluster, error)
	StartCluster(ctx context.Context, resourceGroup, name string) (*Cluster, error)
	InvokeCommand(ctx context.Context, resourceGroup, name, command, host string) (string, error)
	ClusterStatus(ctx context.Context, resourceGroup, name string) (*ClusterStatus, error)

	CreateOrUpdateDataCenter(ctx context.Context, cfg CreateDataCenterConfig) (*DataCenter, error)
	GetDataCenter(ctx context.Context, resourceGroup, cluster, name string) (*DataCenter, error)
	ListDataCenters(ctx context.Context, resourceGroup, cluster string) ([]DataCenter, error)
	UpdateDataCenter(ctx context.Context, resourceGroup, cluster, name string, patch DataCenterPatch) (*DataCenter, error)
	DeleteDataCenter(ctx context.Context, resourceGroup, cluster, name string) error
}
