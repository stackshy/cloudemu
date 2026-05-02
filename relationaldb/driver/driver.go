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
