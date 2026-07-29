// Package driver defines the interface for relational-database service
// implementations (RDS, Cloud SQL, Azure SQL, …). The shape covers the lifecycle
// of a managed DB server (instance) plus the Aurora-style cluster grouping and
// snapshot/restore operations. It does not model the SQL surface itself —
// connection-string consumers are expected to point at a real engine.
package driver

import (
	"context"
	"time"
)

// Lifecycle states. Mirrors the AWS RDS terminology since RDS is the
// most-feature-rich provider; Cloud SQL / Azure SQL implementations map their
// native states onto this set.
const (
	StateCreating  = "creating"
	StateAvailable = "available"
	StateModifying = "modifying"
	StateStarting  = "starting"
	StateStopping  = "stopping"
	StateStopped   = "stopped"
	StateRebooting = "rebooting"
	StateDeleting  = "deleting"
	StateBackingUp = "backing-up"
)

// Snapshot states.
const (
	SnapshotCreating  = "creating"
	SnapshotAvailable = "available"
)

// InstanceConfig configures a managed database instance.
type InstanceConfig struct {
	ID                 string
	Engine             string // "mysql", "postgres", "aurora-mysql", "aurora-postgresql", …
	EngineVersion      string
	InstanceClass      string // "db.t3.micro", …
	AllocatedStorage   int    // GiB
	StorageType        string // "gp2", "io1", …
	MasterUsername     string
	MasterUserPassword string
	DBName             string // optional initial DB name
	Port               int
	MultiAZ            bool
	PubliclyAccessible bool
	VPCSecurityGroups  []string
	SubnetGroupName    string
	ClusterID          string // empty for standalone, set for Aurora cluster members
	AvailabilityZone   string
	Tags               map[string]string
}

// Instance describes a managed database instance.
type Instance struct {
	ID                 string
	ARN                string
	Engine             string
	EngineVersion      string
	InstanceClass      string
	AllocatedStorage   int
	StorageType        string
	MasterUsername     string
	DBName             string
	Endpoint           string
	Port               int
	State              string
	MultiAZ            bool
	PubliclyAccessible bool
	VPCSecurityGroups  []string
	SubnetGroupName    string
	ClusterID          string
	AvailabilityZone   string
	CreatedAt          time.Time
	Tags               map[string]string
	// ReadReplicaSource is the identifier of the primary this instance
	// replicates from; empty for a primary. ReadReplicaTargets lists the
	// replica identifiers reading from this instance.
	ReadReplicaSource  string
	ReadReplicaTargets []string
}

// ModifyInstanceInput holds modifiable instance attributes. Zero-valued fields
// mean "no change".
type ModifyInstanceInput struct {
	InstanceClass      string
	AllocatedStorage   int
	EngineVersion      string
	MasterUserPassword string
	MultiAZ            *bool
	Tags               map[string]string
}

// ClusterConfig configures an Aurora-style cluster. Members are added by
// calling CreateInstance with ClusterID set.
type ClusterConfig struct {
	ID                 string
	Engine             string // "aurora-mysql" or "aurora-postgresql"
	EngineVersion      string
	MasterUsername     string
	MasterUserPassword string
	DatabaseName       string
	Port               int
	VPCSecurityGroups  []string
	SubnetGroupName    string
	Tags               map[string]string
}

// Cluster describes an Aurora-style database cluster.
type Cluster struct {
	ID                string
	ARN               string
	Engine            string
	EngineVersion     string
	MasterUsername    string
	DatabaseName      string
	Endpoint          string
	ReaderEndpoint    string
	Port              int
	State             string
	Members           []string // instance IDs
	VPCSecurityGroups []string
	SubnetGroupName   string
	CreatedAt         time.Time
	Tags              map[string]string
}

// SnapshotConfig configures an instance snapshot.
type SnapshotConfig struct {
	ID         string
	InstanceID string
	Tags       map[string]string
}

// Snapshot describes an instance snapshot.
type Snapshot struct {
	ID               string
	ARN              string
	InstanceID       string
	Engine           string
	EngineVersion    string
	AllocatedStorage int
	State            string
	CreatedAt        time.Time
	Tags             map[string]string
}

// ClusterSnapshotConfig configures a cluster snapshot.
type ClusterSnapshotConfig struct {
	ID        string
	ClusterID string
	Tags      map[string]string
}

// ClusterSnapshot describes a cluster snapshot.
type ClusterSnapshot struct {
	ID            string
	ARN           string
	ClusterID     string
	Engine        string
	EngineVersion string
	State         string
	CreatedAt     time.Time
	Tags          map[string]string
}

// RestoreInstanceInput configures restoring an instance from a snapshot.
type RestoreInstanceInput struct {
	NewInstanceID string
	SnapshotID    string
	InstanceClass string
	Tags          map[string]string
}

// RestoreClusterInput configures restoring a cluster from a snapshot.
type RestoreClusterInput struct {
	NewClusterID string
	SnapshotID   string
	Tags         map[string]string
}

// RelationalDB is the interface that relational-database providers must satisfy.
type RelationalDB interface {
	// Instances
	CreateInstance(ctx context.Context, cfg InstanceConfig) (*Instance, error)
	DescribeInstances(ctx context.Context, ids []string) ([]Instance, error)
	ModifyInstance(ctx context.Context, id string, input ModifyInstanceInput) (*Instance, error)
	DeleteInstance(ctx context.Context, id string) error
	StartInstance(ctx context.Context, id string) error
	StopInstance(ctx context.Context, id string) error
	RebootInstance(ctx context.Context, id string) error

	// Clusters (Aurora-style)
	CreateCluster(ctx context.Context, cfg ClusterConfig) (*Cluster, error)
	DescribeClusters(ctx context.Context, ids []string) ([]Cluster, error)
	ModifyCluster(ctx context.Context, id string, input ModifyInstanceInput) (*Cluster, error)
	DeleteCluster(ctx context.Context, id string) error
	StartCluster(ctx context.Context, id string) error
	StopCluster(ctx context.Context, id string) error

	// Instance snapshots
	CreateSnapshot(ctx context.Context, cfg SnapshotConfig) (*Snapshot, error)
	DescribeSnapshots(ctx context.Context, ids []string, instanceID string) ([]Snapshot, error)
	DeleteSnapshot(ctx context.Context, id string) error
	RestoreInstanceFromSnapshot(ctx context.Context, input RestoreInstanceInput) (*Instance, error)

	// Cluster snapshots
	CreateClusterSnapshot(ctx context.Context, cfg ClusterSnapshotConfig) (*ClusterSnapshot, error)
	DescribeClusterSnapshots(ctx context.Context, ids []string, clusterID string) ([]ClusterSnapshot, error)
	DeleteClusterSnapshot(ctx context.Context, id string) error
	RestoreClusterFromSnapshot(ctx context.Context, input RestoreClusterInput) (*Cluster, error)
}

// SubnetGroup is a named set of subnets a managed database is placed into.
type SubnetGroup struct {
	Name        string
	Description string
	VPCID       string
	SubnetIDs   []string
	Status      string
	ARN         string
}

// SubnetGroupConfig describes a subnet group to create.
type SubnetGroupConfig struct {
	Name        string
	Description string
	SubnetIDs   []string
}

// SubnetGroups is an OPTIONAL capability. Subnet groups are an AWS concept —
// Azure and GCP place managed databases with vnet integration instead — so
// this is deliberately kept out of the RelationalDB interface and discovered
// by type assertion. Drivers that do not implement it answer InvalidAction,
// which is the truthful response for a cloud that has no such resource.
type SubnetGroups interface {
	CreateDBSubnetGroup(ctx context.Context, cfg SubnetGroupConfig) (*SubnetGroup, error)
	DescribeDBSubnetGroups(ctx context.Context, names []string) ([]SubnetGroup, error)
	DeleteDBSubnetGroup(ctx context.Context, name string) error
}

// Parameter is a single engine parameter within a parameter group. Only
// user-set parameters are modeled; the emulator does not fabricate the hundreds
// of engine defaults real AWS returns.
type Parameter struct {
	Name        string
	Value       string
	ApplyMethod string // "immediate" | "pending-reboot"
	Source      string // "user" | "engine-default"
	ApplyType   string
	DataType    string
	Description string
}

// ParameterGroupConfig configures a new DB (or DB cluster) parameter group.
type ParameterGroupConfig struct {
	Name        string
	Family      string // e.g. "mysql8.0", "aurora-postgresql15"
	Description string
	Tags        map[string]string
}

// ParameterGroup is a named set of engine parameters applied to instances.
type ParameterGroup struct {
	Name        string
	Family      string
	Description string
	ARN         string
	Parameters  map[string]string // user-set name -> value
}

// ClusterParameterGroup is the cluster-scoped analogue of ParameterGroup.
type ClusterParameterGroup struct {
	Name        string
	Family      string
	Description string
	ARN         string
	Parameters  map[string]string
}

// ParameterGroups is an OPTIONAL capability covering both DB parameter groups
// and DB cluster parameter groups, discovered by type assertion like
// SubnetGroups. Real AWS reuses the same DBParameterGroup fault codes for the
// cluster variants, so they share error mapping.
type ParameterGroups interface {
	CreateDBParameterGroup(ctx context.Context, cfg ParameterGroupConfig) (*ParameterGroup, error)
	DescribeDBParameterGroups(ctx context.Context, names []string) ([]ParameterGroup, error)
	ModifyDBParameterGroup(ctx context.Context, name string, params []Parameter) (*ParameterGroup, error)
	DeleteDBParameterGroup(ctx context.Context, name string) error
	DescribeDBParameters(ctx context.Context, name string) ([]Parameter, error)
	ResetDBParameterGroup(ctx context.Context, name string, params []string, resetAll bool) (*ParameterGroup, error)
	CopyDBParameterGroup(ctx context.Context, source, target, description string) (*ParameterGroup, error)

	CreateDBClusterParameterGroup(ctx context.Context, cfg ParameterGroupConfig) (*ClusterParameterGroup, error)
	DescribeDBClusterParameterGroups(ctx context.Context, names []string) ([]ClusterParameterGroup, error)
	ModifyDBClusterParameterGroup(ctx context.Context, name string, params []Parameter) (*ClusterParameterGroup, error)
	DeleteDBClusterParameterGroup(ctx context.Context, name string) error
	DescribeDBClusterParameters(ctx context.Context, name string) ([]Parameter, error)
	ResetDBClusterParameterGroup(ctx context.Context, name string, params []string, resetAll bool) (*ClusterParameterGroup, error)
	CopyDBClusterParameterGroup(ctx context.Context, source, target, description string) (*ClusterParameterGroup, error)
}

// OptionGroupConfig configures a new option group.
type OptionGroupConfig struct {
	Name               string
	EngineName         string
	MajorEngineVersion string
	Description        string
	Tags               map[string]string
}

// Option is an option included in an option group.
type Option struct {
	Name     string
	Port     int
	Version  string
	Settings map[string]string
}

// OptionGroup is a named set of engine options applied to instances.
type OptionGroup struct {
	Name               string
	EngineName         string
	MajorEngineVersion string
	Description        string
	ARN                string
	Options            []Option
}

// OptionGroupOption is an option available to include in an option group for a
// given engine (metadata, returned by DescribeOptionGroupOptions).
type OptionGroupOption struct {
	Name               string
	Description        string
	EngineName         string
	MajorEngineVersion string
	Persistent         bool
	Permanent          bool
}

// OptionGroups is an OPTIONAL capability. Option groups are an AWS-only
// concept, discovered by type assertion like SubnetGroups.
type OptionGroups interface {
	CreateOptionGroup(ctx context.Context, cfg OptionGroupConfig) (*OptionGroup, error)
	DescribeOptionGroups(ctx context.Context, names []string, engineName string) ([]OptionGroup, error)
	ModifyOptionGroup(ctx context.Context, name string, include []Option, remove []string) (*OptionGroup, error)
	DeleteOptionGroup(ctx context.Context, name string) error
	CopyOptionGroup(ctx context.Context, source, target, description string) (*OptionGroup, error)
	DescribeOptionGroupOptions(ctx context.Context, engineName, majorEngineVersion string) ([]OptionGroupOption, error)
}

// ReadReplicaConfig configures a new read replica.
type ReadReplicaConfig struct {
	ID                 string // new replica instance identifier
	SourceInstanceID   string
	InstanceClass      string
	AvailabilityZone   string
	Port               int
	PubliclyAccessible bool
	Tags               map[string]string
}

// ReadReplicas is an OPTIONAL capability for creating and promoting read
// replicas, discovered by type assertion.
type ReadReplicas interface {
	CreateDBInstanceReadReplica(ctx context.Context, cfg ReadReplicaConfig) (*Instance, error)
	PromoteReadReplica(ctx context.Context, id string) (*Instance, error)
}

// RestoreInstanceToPointInTimeInput configures a point-in-time instance restore.
type RestoreInstanceToPointInTimeInput struct {
	SourceInstanceID        string
	TargetInstanceID        string
	InstanceClass           string
	UseLatestRestorableTime bool
	RestoreTime             time.Time
	Tags                    map[string]string
}

// RestoreClusterToPointInTimeInput configures a point-in-time cluster restore.
type RestoreClusterToPointInTimeInput struct {
	SourceClusterID         string
	TargetClusterID         string
	UseLatestRestorableTime bool
	RestoreTime             time.Time
	Tags                    map[string]string
}

// AdvancedRestore is an OPTIONAL capability covering snapshot copy and
// point-in-time restore for instances and clusters, discovered by type
// assertion.
type AdvancedRestore interface {
	CopyDBSnapshot(ctx context.Context, source, target string, tags map[string]string) (*Snapshot, error)
	CopyDBClusterSnapshot(ctx context.Context, source, target string, tags map[string]string) (*ClusterSnapshot, error)
	RestoreDBInstanceToPointInTime(ctx context.Context, input RestoreInstanceToPointInTimeInput) (*Instance, error)
	RestoreDBClusterToPointInTime(ctx context.Context, input RestoreClusterToPointInTimeInput) (*Cluster, error)
}
