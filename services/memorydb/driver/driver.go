// Package driver defines the interface for AWS MemoryDB, a Redis/Valkey-
// compatible in-memory database. MemoryDB is control-plane only (there is no
// Set/Get data plane in its API), so this driver models clusters and their
// shards/nodes/endpoints plus the ACL, user, parameter-group, subnet-group,
// snapshot and multi-region resources and their cross-references.
package driver

import (
	"context"
	"time"
)

// Cluster lifecycle statuses (a representative subset of MemoryDB's).
const (
	StatusCreating     = "creating"
	StatusAvailable    = "available"
	StatusUpdating     = "updating"
	StatusDeleting     = "deleting"
	StatusSnapshotting = "snapshotting"
)

// Endpoint is an address:port a client connects to.
type Endpoint struct {
	Address string
	Port    int
}

// Node is one node within a shard.
type Node struct {
	Name             string
	Status           string
	AvailabilityZone string
	CreateTime       time.Time
	Endpoint         Endpoint
}

// Shard is a collection of nodes (one primary + replicas) owning a slot range.
type Shard struct {
	Name          string
	Status        string
	Slots         string
	NumberOfNodes int
	Nodes         []Node
}

// SecurityGroupMembership is a security group attached to a cluster.
type SecurityGroupMembership struct {
	SecurityGroupID string
	Status          string
}

// Cluster is a MemoryDB cluster.
type Cluster struct {
	Name                    string
	ARN                     string
	Description             string
	Status                  string
	NodeType                string
	Engine                  string
	EngineVersion           string
	EnginePatchVersion      string
	NumberOfShards          int
	ACLName                 string
	ParameterGroupName      string
	ParameterGroupStatus    string
	SubnetGroupName         string
	SecurityGroups          []SecurityGroupMembership
	Shards                  []Shard
	ClusterEndpoint         Endpoint
	TLSEnabled              bool
	KmsKeyID                string
	MaintenanceWindow       string
	SnapshotWindow          string
	SnapshotRetentionLimit  int
	SnsTopicARN             string
	SnsTopicStatus          string
	AutoMinorVersionUpgrade bool
	DataTiering             bool
	AvailabilityMode        string
	NetworkType             string
	IPDiscovery             string
	MultiRegionClusterName  string
	Tags                    map[string]string
	CreatedAt               time.Time
}

// CreateClusterConfig configures a new cluster.
type CreateClusterConfig struct {
	Name                    string
	Description             string
	NodeType                string
	Engine                  string
	EngineVersion           string
	NumShards               int
	NumReplicasPerShard     int
	ACLName                 string
	ParameterGroupName      string
	SubnetGroupName         string
	SecurityGroupIDs        []string
	Port                    int
	TLSEnabled              bool
	AutoMinorVersionUpgrade bool
	DataTiering             bool
	KmsKeyID                string
	MaintenanceWindow       string
	SnapshotWindow          string
	SnapshotRetentionLimit  int
	SnsTopicARN             string
	SnapshotName            string // restore source
	MultiRegionClusterName  string
	NetworkType             string
	IPDiscovery             string
	Tags                    map[string]string
}

// UpdateClusterConfig holds the mutable cluster fields; zero values mean "no
// change". ShardCount/ReplicaCount are pointers so 0 is expressible.
type UpdateClusterConfig struct {
	Name                   string
	Description            string
	NodeType               string
	EngineVersion          string
	ACLName                string
	ParameterGroupName     string
	MaintenanceWindow      string
	SnapshotWindow         string
	SnsTopicARN            string
	SnsTopicStatus         string
	SecurityGroupIDs       []string
	ShardCount             *int
	ReplicaCount           *int
	SnapshotRetentionLimit *int
}

// ACL is an Access Control List linking users to clusters.
type ACL struct {
	Name                 string
	ARN                  string
	Status               string
	MinimumEngineVersion string
	UserNames            []string
	Clusters             []string
	Tags                 map[string]string
}

// Authentication describes a user's auth configuration.
type Authentication struct {
	Type          string // password | iam | no-password-required
	PasswordCount int
}

// User is a MemoryDB user.
type User struct {
	Name                 string
	ARN                  string
	Status               string
	AccessString         string
	Authentication       Authentication
	MinimumEngineVersion string
	ACLNames             []string
	Tags                 map[string]string
}

// CreateUserConfig configures a new user.
type CreateUserConfig struct {
	Name               string
	AccessString       string
	AuthenticationType string
	Passwords          []string
	Tags               map[string]string
}

// UpdateUserConfig holds mutable user fields.
type UpdateUserConfig struct {
	Name               string
	AccessString       string
	AuthenticationType string
	Passwords          []string
}

// Parameter is one server parameter.
type Parameter struct {
	Name                 string
	Value                string
	Description          string
	DataType             string
	AllowedValues        string
	MinimumEngineVersion string
}

// ParameterGroup is a named set of parameters.
type ParameterGroup struct {
	Name        string
	ARN         string
	Family      string
	Description string
	Tags        map[string]string
}

// ParameterNameValue is a parameter override in an update request.
type ParameterNameValue struct {
	Name  string
	Value string
}

// Subnet is a subnet in a subnet group.
type Subnet struct {
	Identifier            string
	AvailabilityZone      string
	SupportedNetworkTypes []string
}

// SubnetGroup is a set of subnets a cluster is placed into.
type SubnetGroup struct {
	Name                  string
	ARN                   string
	Description           string
	VpcID                 string
	Subnets               []Subnet
	SupportedNetworkTypes []string
	Tags                  map[string]string
}

// CreateSubnetGroupConfig configures a subnet group.
type CreateSubnetGroupConfig struct {
	Name        string
	Description string
	SubnetIDs   []string
	Tags        map[string]string
}

// UpdateSubnetGroupConfig holds mutable subnet-group fields.
type UpdateSubnetGroupConfig struct {
	Name        string
	Description string
	SubnetIDs   []string
}

// ClusterConfiguration captures a cluster's shape at snapshot time.
type ClusterConfiguration struct {
	Name                   string
	NodeType               string
	Engine                 string
	EngineVersion          string
	ParameterGroupName     string
	SubnetGroupName        string
	VpcID                  string
	MaintenanceWindow      string
	SnapshotWindow         string
	TopicARN               string
	NumShards              int
	ReplicasPerShard       int
	Port                   int
	SnapshotRetentionLimit int
	TLSEnabled             bool
}

// Snapshot is a point-in-time copy of a cluster.
type Snapshot struct {
	Name                 string
	ARN                  string
	Status               string
	Source               string
	KmsKeyID             string
	DataTiering          bool
	ClusterConfiguration ClusterConfiguration
	Tags                 map[string]string
	CreatedAt            time.Time
}

// CreateSnapshotConfig configures a snapshot.
type CreateSnapshotConfig struct {
	Name        string
	ClusterName string
	KmsKeyID    string
	Tags        map[string]string
}

// CopySnapshotConfig configures a snapshot copy.
type CopySnapshotConfig struct {
	SourceName string
	TargetName string
	KmsKeyID   string
	Tags       map[string]string
}

// EngineVersionInfo is a supported engine version.
type EngineVersionInfo struct {
	Engine               string
	EngineVersion        string
	EnginePatchVersion   string
	ParameterGroupFamily string
}

// Event is a lifecycle event.
type Event struct {
	SourceName string
	SourceType string
	Message    string
	Date       time.Time
}

// MemoryDB is the core capability every MemoryDB provider implements.
//
//nolint:interfacebloat // MemoryDB genuinely exposes this many control-plane operations.
type MemoryDB interface {
	// Clusters
	CreateCluster(ctx context.Context, cfg CreateClusterConfig) (*Cluster, error)
	DescribeClusters(ctx context.Context, names []string) ([]Cluster, error)
	UpdateCluster(ctx context.Context, cfg UpdateClusterConfig) (*Cluster, error)
	DeleteCluster(ctx context.Context, name, finalSnapshotName string) (*Cluster, error)
	FailoverShard(ctx context.Context, clusterName, shardName string) (*Cluster, error)
	ListAllowedNodeTypeUpdates(ctx context.Context, clusterName string) (scaleUp, scaleDown []string, err error)

	// ACLs
	CreateACL(ctx context.Context, name string, userNames []string, tags map[string]string) (*ACL, error)
	DescribeACLs(ctx context.Context, names []string) ([]ACL, error)
	UpdateACL(ctx context.Context, name string, add, remove []string) (*ACL, error)
	DeleteACL(ctx context.Context, name string) (*ACL, error)

	// Users
	CreateUser(ctx context.Context, cfg CreateUserConfig) (*User, error)
	DescribeUsers(ctx context.Context, names []string) ([]User, error)
	UpdateUser(ctx context.Context, cfg UpdateUserConfig) (*User, error)
	DeleteUser(ctx context.Context, name string) (*User, error)

	// Parameter groups
	CreateParameterGroup(ctx context.Context, name, family, description string, tags map[string]string) (*ParameterGroup, error)
	DescribeParameterGroups(ctx context.Context, names []string) ([]ParameterGroup, error)
	UpdateParameterGroup(ctx context.Context, name string, params []ParameterNameValue) (*ParameterGroup, error)
	ResetParameterGroup(ctx context.Context, name string, all bool, names []string) (*ParameterGroup, error)
	DeleteParameterGroup(ctx context.Context, name string) (*ParameterGroup, error)
	DescribeParameters(ctx context.Context, groupName string) ([]Parameter, error)

	// Subnet groups
	CreateSubnetGroup(ctx context.Context, cfg CreateSubnetGroupConfig) (*SubnetGroup, error)
	DescribeSubnetGroups(ctx context.Context, names []string) ([]SubnetGroup, error)
	UpdateSubnetGroup(ctx context.Context, cfg UpdateSubnetGroupConfig) (*SubnetGroup, error)
	DeleteSubnetGroup(ctx context.Context, name string) (*SubnetGroup, error)

	// Snapshots
	CreateSnapshot(ctx context.Context, cfg CreateSnapshotConfig) (*Snapshot, error)
	DescribeSnapshots(ctx context.Context, names []string, clusterName string) ([]Snapshot, error)
	CopySnapshot(ctx context.Context, cfg CopySnapshotConfig) (*Snapshot, error)
	DeleteSnapshot(ctx context.Context, name string) (*Snapshot, error)

	// Tags (resource addressed by ARN)
	TagResource(ctx context.Context, arn string, tags map[string]string) ([]Tag, error)
	UntagResource(ctx context.Context, arn string, keys []string) ([]Tag, error)
	ListTags(ctx context.Context, arn string) ([]Tag, error)

	// Catalogs
	DescribeEngineVersions(ctx context.Context, engine, version string) ([]EngineVersionInfo, error)
	DescribeEvents(ctx context.Context) ([]Event, error)
}

// Tag is a resource tag.
type Tag struct {
	Key   string
	Value string
}

// MultiRegionCluster is a cross-region cluster grouping regional clusters.
type MultiRegionCluster struct {
	Name                          string
	ARN                           string
	Status                        string
	NodeType                      string
	Engine                        string
	EngineVersion                 string
	NumberOfShards                int
	TLSEnabled                    bool
	MultiRegionParameterGroupName string
	Members                       []RegionalCluster
	Tags                          map[string]string
}

// RegionalCluster is one region's membership in a multi-region cluster.
type RegionalCluster struct {
	ClusterName string
	Region      string
	Status      string
	ARN         string
}

// MultiRegionParameterGroup is a parameter group for multi-region clusters.
type MultiRegionParameterGroup struct {
	Name        string
	ARN         string
	Family      string
	Description string
}

// CreateMultiRegionClusterConfig configures a multi-region cluster.
type CreateMultiRegionClusterConfig struct {
	NameSuffix                    string
	Description                   string
	NodeType                      string
	Engine                        string
	EngineVersion                 string
	NumShards                     int
	TLSEnabled                    bool
	MultiRegionParameterGroupName string
	Tags                          map[string]string
}

// MultiRegion is an OPTIONAL capability (cross-region clusters), discovered by
// type assertion.
type MultiRegion interface {
	CreateMultiRegionCluster(ctx context.Context, cfg CreateMultiRegionClusterConfig) (*MultiRegionCluster, error)
	DescribeMultiRegionClusters(ctx context.Context, names []string) ([]MultiRegionCluster, error)
	UpdateMultiRegionCluster(ctx context.Context, name, nodeType, engineVersion string, shardCount *int) (*MultiRegionCluster, error)
	DeleteMultiRegionCluster(ctx context.Context, name string) (*MultiRegionCluster, error)
	ListAllowedMultiRegionClusterUpdates(ctx context.Context, name string) (nodeTypes []string, err error)
	DescribeMultiRegionParameterGroups(ctx context.Context, names []string) ([]MultiRegionParameterGroup, error)
	DescribeMultiRegionParameters(ctx context.Context, groupName string) ([]Parameter, error)
}

// ReservedNode is a purchased reserved node.
type ReservedNode struct {
	ReservationID           string
	OfferingID              string
	NodeType                string
	NodeCount               int
	Duration                int
	FixedPrice              float64
	OfferingType            string
	State                   string
	StartTime               time.Time
	ReservedNodesOfferingID string
}

// ReservedNodesOffering is a purchasable reserved-node offering.
type ReservedNodesOffering struct {
	OfferingID   string
	NodeType     string
	Duration     int
	FixedPrice   float64
	OfferingType string
}

// ReservedNodes is an OPTIONAL capability (billing), discovered by type
// assertion.
type ReservedNodes interface {
	DescribeReservedNodes(ctx context.Context) ([]ReservedNode, error)
	DescribeReservedNodesOfferings(ctx context.Context) ([]ReservedNodesOffering, error)
	PurchaseReservedNodesOffering(ctx context.Context, offeringID, reservationID string, nodeCount int) (*ReservedNode, error)
}

// ServiceUpdate is a maintenance/security update available for clusters.
type ServiceUpdate struct {
	ClusterName         string
	ServiceUpdateName   string
	ReleaseDate         time.Time
	Description         string
	Status              string
	Type                string
	Engine              string
	NodesUpdated        string
	AutoUpdateStartDate time.Time
}

// UnprocessedCluster reports a cluster a batch update could not be applied to.
type UnprocessedCluster struct {
	ClusterName  string
	ErrorType    string
	ErrorMessage string
}

// ServiceUpdates is an OPTIONAL capability (fleet maintenance), discovered by
// type assertion.
type ServiceUpdates interface {
	DescribeServiceUpdates(ctx context.Context, serviceUpdateName string, clusterNames, status []string) ([]ServiceUpdate, error)
	BatchUpdateCluster(
		ctx context.Context, clusterNames []string, serviceUpdateName string,
	) (processed []Cluster, unprocessed []UnprocessedCluster, err error)
}
