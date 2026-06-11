package driver

import "context"

// Instance pool state values.
const (
	PoolActive  = "ACTIVE"
	PoolStopped = "STOPPED"
	PoolDeleted = "DELETED"
)

// Cluster state values.
const (
	ClusterPending    = "PENDING"
	ClusterRunning    = "RUNNING"
	ClusterTerminated = "TERMINATED"
)

// InstancePoolConfig describes an instance pool to create or edit.
type InstancePoolConfig struct {
	Name             string
	NodeTypeID       string
	MinIdleInstances int32
	MaxCapacity      int32
}

// InstancePool describes an instance pool.
type InstancePool struct {
	ID               string
	Name             string
	NodeTypeID       string
	State            string
	MinIdleInstances int32
	MaxCapacity      int32
}

// ClusterConfig describes a cluster to create or edit.
type ClusterConfig struct {
	Name         string
	SparkVersion string
	NodeTypeID   string
	NumWorkers   int32
	AutoscaleMin int32
	AutoscaleMax int32
}

// Cluster describes a compute cluster.
type Cluster struct {
	ID           string
	Name         string
	SparkVersion string
	NodeTypeID   string
	State        string
	NumWorkers   int32
	AutoscaleMin int32
	AutoscaleMax int32
}

// JobConfig describes a job to create or update. SettingsJSON is the raw job
// settings object (name, tasks, …) echoed back on Get.
type JobConfig struct {
	Name         string
	SettingsJSON []byte
}

// Job describes a job.
type Job struct {
	ID              int64
	Name            string
	CreatorUserName string
	CreatedTime     int64
	SettingsJSON    []byte
}

// AccessControl is a single access-control entry. Exactly one principal field
// is set.
type AccessControl struct {
	UserName             string
	GroupName            string
	ServicePrincipalName string
	PermissionLevel      string
}

// ObjectPermissions is the permission set on a securable object.
type ObjectPermissions struct {
	ObjectID          string
	ObjectType        string
	AccessControlList []AccessControl
}

// DataPlane is the interface for the Databricks workspace data plane: compute
// (instance pools, clusters), jobs, and object permissions. It is separate
// from the ARM-level Databricks (workspace management) interface.
type DataPlane interface {
	CreateInstancePool(ctx context.Context, cfg InstancePoolConfig) (*InstancePool, error)
	GetInstancePool(ctx context.Context, id string) (*InstancePool, error)
	ListInstancePools(ctx context.Context) ([]InstancePool, error)
	EditInstancePool(ctx context.Context, id string, cfg InstancePoolConfig) error
	DeleteInstancePool(ctx context.Context, id string) error

	CreateCluster(ctx context.Context, cfg ClusterConfig) (*Cluster, error)
	GetCluster(ctx context.Context, id string) (*Cluster, error)
	ListClusters(ctx context.Context) ([]Cluster, error)
	EditCluster(ctx context.Context, id string, cfg ClusterConfig) error
	DeleteCluster(ctx context.Context, id string) error
	PermanentDeleteCluster(ctx context.Context, id string) error
	StartCluster(ctx context.Context, id string) error
	RestartCluster(ctx context.Context, id string) error

	CreateJob(ctx context.Context, cfg JobConfig) (int64, error)
	GetJob(ctx context.Context, id int64) (*Job, error)
	ListJobs(ctx context.Context) ([]Job, error)
	UpdateJob(ctx context.Context, id int64, cfg JobConfig) error
	ResetJob(ctx context.Context, id int64, cfg JobConfig) error
	DeleteJob(ctx context.Context, id int64) error
	RunJobNow(ctx context.Context, id int64) (int64, error)

	GetPermissions(ctx context.Context, objectType, objectID string) (*ObjectPermissions, error)
	SetPermissions(ctx context.Context, objectType, objectID string, acl []AccessControl) (*ObjectPermissions, error)
	UpdatePermissions(ctx context.Context, objectType, objectID string, acl []AccessControl) (*ObjectPermissions, error)
}
