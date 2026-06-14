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
	Pinned       bool
}

// NodeType describes an available compute node type.
type NodeType struct {
	NodeTypeID  string
	Description string
	NumCores    float64
	MemoryMB    int32
}

// SparkVersion describes an available runtime version.
type SparkVersion struct {
	Key  string
	Name string
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

// Run life-cycle and result state values.
const (
	RunRunning     = "RUNNING"
	RunTerminated  = "TERMINATED"
	ResultSuccess  = "SUCCESS"
	ResultCanceled = "CANCELED"
)

// Library install status values.
const (
	LibraryInstalled          = "INSTALLED"
	LibraryUninstallOnRestart = "UNINSTALL_ON_RESTART"
)

// Run describes a single job run.
type Run struct {
	RunID          int64
	JobID          int64
	RunName        string
	LifeCycleState string
	ResultState    string
	StateMessage   string
	StartTime      int64
	EndTime        int64
}

// RunOutput is the output of a completed run.
type RunOutput struct {
	Run            Run
	NotebookResult string
}

// ClusterPolicyConfig describes a cluster policy to create or edit.
type ClusterPolicyConfig struct {
	Name               string
	Definition         string
	Description        string
	MaxClustersPerUser int64
}

// ClusterPolicy describes a cluster policy.
type ClusterPolicy struct {
	PolicyID           string
	Name               string
	Definition         string
	Description        string
	MaxClustersPerUser int64
	CreatorUserName    string
	CreatedAt          int64
}

// LibrarySpec identifies a library to install on a cluster. Exactly one source
// field is typically set.
type LibrarySpec struct {
	Jar              string
	Egg              string
	Whl              string
	PypiPackage      string
	MavenCoordinates string
	Cran             string
}

// LibraryStatus is the install status of a library on a cluster.
type LibraryStatus struct {
	Library LibrarySpec
	Status  string
}

// ClusterLibraryStatuses bundles a cluster's library statuses.
type ClusterLibraryStatuses struct {
	ClusterID string
	Statuses  []LibraryStatus
}

// DataPlane is the interface for the Databricks workspace data plane: compute
// (instance pools, clusters, cluster policies, libraries), jobs and runs, and
// object permissions. It is separate from the ARM-level Databricks (workspace
// management) interface.
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
	ResizeCluster(ctx context.Context, id string, numWorkers, autoscaleMin, autoscaleMax int32) error
	PinCluster(ctx context.Context, id string) error
	UnpinCluster(ctx context.Context, id string) error
	ListNodeTypes(ctx context.Context) ([]NodeType, error)
	ListSparkVersions(ctx context.Context) ([]SparkVersion, error)
	ListZones(ctx context.Context) (zones []string, defaultZone string, err error)

	CreateJob(ctx context.Context, cfg JobConfig) (int64, error)
	GetJob(ctx context.Context, id int64) (*Job, error)
	ListJobs(ctx context.Context) ([]Job, error)
	UpdateJob(ctx context.Context, id int64, cfg JobConfig) error
	ResetJob(ctx context.Context, id int64, cfg JobConfig) error
	DeleteJob(ctx context.Context, id int64) error
	RunJobNow(ctx context.Context, id int64) (int64, error)

	SubmitRun(ctx context.Context, runName string) (int64, error)
	GetRun(ctx context.Context, runID int64) (*Run, error)
	ListRuns(ctx context.Context, jobID int64) ([]Run, error)
	CancelRun(ctx context.Context, runID int64) error
	CancelAllRuns(ctx context.Context, jobID int64) error
	DeleteRun(ctx context.Context, runID int64) error
	RepairRun(ctx context.Context, runID int64) (int64, error)
	GetRunOutput(ctx context.Context, runID int64) (*RunOutput, error)

	CreateClusterPolicy(ctx context.Context, cfg ClusterPolicyConfig) (*ClusterPolicy, error)
	GetClusterPolicy(ctx context.Context, policyID string) (*ClusterPolicy, error)
	EditClusterPolicy(ctx context.Context, policyID string, cfg ClusterPolicyConfig) error
	DeleteClusterPolicy(ctx context.Context, policyID string) error
	ListClusterPolicies(ctx context.Context) ([]ClusterPolicy, error)

	InstallLibraries(ctx context.Context, clusterID string, libs []LibrarySpec) error
	UninstallLibraries(ctx context.Context, clusterID string, libs []LibrarySpec) error
	ClusterLibraryStatuses(ctx context.Context, clusterID string) ([]LibraryStatus, error)
	AllClusterLibraryStatuses(ctx context.Context) ([]ClusterLibraryStatuses, error)

	GetPermissions(ctx context.Context, objectType, objectID string) (*ObjectPermissions, error)
	SetPermissions(ctx context.Context, objectType, objectID string, acl []AccessControl) (*ObjectPermissions, error)
	UpdatePermissions(ctx context.Context, objectType, objectID string, acl []AccessControl) (*ObjectPermissions, error)
}
