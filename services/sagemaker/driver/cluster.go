package driver

import (
	"context"
	"encoding/json"
)

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

// Instance-group status values (ClusterInstanceGroupDetails.Status).
const (
	GroupInService = "InService"
	GroupCreating  = "Creating"
	GroupUpdating  = "Updating"
	GroupDeleting  = "Deleting"
)

// Cluster limits and defaults.
const (
	MaxClusterInstanceGroups    = 100
	MaxClusterInstancesPerGroup = 6758
	// MaxClusterInstances bounds the total instances of one cluster so a node
	// listing never allocates without limit.
	MaxClusterInstances = 10000
	// DefaultThreadsPerCore is used when a group does not set ThreadsPerCore.
	DefaultThreadsPerCore = 1
)

// LifeCycleConfig is the lifecycle script configuration of an instance group.
type LifeCycleConfig struct {
	SourceS3URI string
	OnCreate    string
}

// ClusterInstanceGroupSpec describes one instance group in a cluster.
type ClusterInstanceGroupSpec struct {
	GroupName        string
	InstanceType     string
	InstanceCount    int
	MinInstanceCount int
	ThreadsPerCore   int // 1 or 2; 0 means the default (1)
	ExecutionRole    string
	LifeCycleConfig  *LifeCycleConfig
}

// ClusterSpec describes a HyperPod cluster to create.
type ClusterSpec struct {
	ClusterName    string
	InstanceGroups []ClusterInstanceGroupSpec
	Tags           []Tag
}

// ClusterUpdate describes an UpdateCluster request. Groups named in
// InstanceGroups are updated (or added when new); InstanceGroupsToDelete are
// removed; other groups are left as they are.
type ClusterUpdate struct {
	ClusterName            string
	InstanceGroups         []ClusterInstanceGroupSpec
	InstanceGroupsToDelete []string
}

// ClusterInstanceGroup is a materialized instance group: what is running now
// (CurrentCount) versus what was asked for (TargetCount).
type ClusterInstanceGroup struct {
	GroupName       string
	InstanceType    string
	CurrentCount    int
	TargetCount     int
	MinCount        int
	ThreadsPerCore  int
	ExecutionRole   string
	LifeCycleConfig *LifeCycleConfig
	Status          string
}

// UnmarshalJSON reads a group persisted before CurrentCount/TargetCount
// existed (its single "InstanceCount" becomes both).
func (g *ClusterInstanceGroup) UnmarshalJSON(data []byte) error {
	type plain ClusterInstanceGroup

	aux := struct {
		*plain
		InstanceCount int
	}{plain: (*plain)(g)}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.InstanceCount > 0 && g.TargetCount == 0 && g.CurrentCount == 0 {
		g.TargetCount, g.CurrentCount = aux.InstanceCount, aux.InstanceCount
	}

	return nil
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
	ListClusters(ctx context.Context, opts ListOptions) (ListPage[Cluster], error)
	UpdateCluster(ctx context.Context, update ClusterUpdate) (*Cluster, error)
	DeleteCluster(ctx context.Context, name string) error
	ListClusterNodes(ctx context.Context, clusterName string) ([]ClusterNode, error)
	DescribeClusterNode(ctx context.Context, clusterName, nodeID string) (*ClusterNode, error)
}
