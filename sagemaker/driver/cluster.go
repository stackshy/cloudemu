package driver

import "context"

// HyperPod cluster status values.
const (
	ClusterCreating       = "Creating"
	ClusterInService      = "InService"
	ClusterUpdating       = "Updating"
	ClusterDeleting       = "Deleting"
	ClusterFailed         = "Failed"
	ClusterRollingBack    = "RollingBack"
	ClusterSystemUpdating = "SystemUpdating"
)

// ClusterInstanceGroupSpec describes one instance group in a cluster.
type ClusterInstanceGroupSpec struct {
	GroupName     string
	InstanceType  string
	InstanceCount int
	ExecutionRole string
}

// ClusterSpec describes a HyperPod cluster to create.
type ClusterSpec struct {
	ClusterName    string
	InstanceGroups []ClusterInstanceGroupSpec
	Tags           []Tag
}

// ClusterInstanceGroup is a materialized instance group.
type ClusterInstanceGroup struct {
	GroupName     string
	InstanceType  string
	InstanceCount int
	ExecutionRole string
}

// Cluster is a HyperPod cluster.
type Cluster struct {
	ClusterName    string
	ClusterARN     string
	Status         string
	InstanceGroups []ClusterInstanceGroup
	FailureMessage string
	CreationTime   string
	Tags           []Tag
}

// ClusterNode is a single node within a cluster instance group.
type ClusterNode struct {
	NodeID       string
	GroupName    string
	InstanceType string
	Status       string // Running, Pending, Failed, ShuttingDown
	LaunchTime   string
}

// clusterAPI covers HyperPod clusters and their nodes.
type clusterAPI interface {
	CreateCluster(ctx context.Context, cfg ClusterSpec) (*Cluster, error)
	DescribeCluster(ctx context.Context, name string) (*Cluster, error)
	ListClusters(ctx context.Context) ([]Cluster, error)
	UpdateCluster(ctx context.Context, name string, groups []ClusterInstanceGroupSpec) (*Cluster, error)
	DeleteCluster(ctx context.Context, name string) error
	ListClusterNodes(ctx context.Context, clusterName string) ([]ClusterNode, error)
	DescribeClusterNode(ctx context.Context, clusterName, nodeID string) (*ClusterNode, error)
}
