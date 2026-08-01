// Package driver defines the portable interface for the Google Cloud Bigtable
// Admin API (bigtableadmin.googleapis.com/v2). It is control-plane only —
// instances, clusters, tables, app profiles, backups, their IAM policies, and
// long-running operations — so it is independent of the other DB drivers.
package driver

import (
	"context"
	"time"
)

// Resource states.
const (
	StateReady    = "READY"
	StateCreating = "CREATING"
)

// Full resource names follow the GCP convention, e.g.
//
//	projects/{p}/instances/{i}
//	projects/{p}/instances/{i}/clusters/{c}
//	projects/{p}/instances/{i}/tables/{t}
//	projects/{p}/instances/{i}/appProfiles/{a}
//	projects/{p}/instances/{i}/clusters/{c}/backups/{b}

// Instance is a Bigtable instance.
type Instance struct {
	Name        string
	DisplayName string
	Type        string
	State       string
	Labels      map[string]string
	CreateTime  time.Time
}

// Autoscaling holds a cluster's autoscaling configuration.
type Autoscaling struct {
	MinServeNodes  int
	MaxServeNodes  int
	CPUTargetPct   int
	StorageTargetB int
}

// Cluster is a Bigtable cluster within an instance.
type Cluster struct {
	Name               string
	Location           string
	ServeNodes         int
	DefaultStorageType string
	State              string
	Autoscaling        *Autoscaling
}

// GCRule is a portable, recursive garbage-collection rule for a column family.
type GCRule struct {
	MaxNumVersions int
	MaxAgeSeconds  int64
	Union          []GCRule
	Intersection   []GCRule
}

// ColumnFamily is a table column family.
type ColumnFamily struct {
	GCRule *GCRule
}

// Table is a Bigtable table.
type Table struct {
	Name               string
	ColumnFamilies     map[string]ColumnFamily
	Granularity        string
	DeletionProtection bool
	SourceBackup       string
	Deleted            bool
}

// AppProfile is a Bigtable app profile (request-routing policy).
type AppProfile struct {
	Name                     string
	Description              string
	MultiClusterRoutingAny   bool
	MultiClusterClusterIDs   []string
	SingleClusterID          string
	AllowTransactionalWrites bool
	Priority                 string
	Etag                     string
}

// Backup is a table backup stored in a cluster. It snapshots the source
// table's column families so a restore can rebuild the schema.
type Backup struct {
	Name           string
	SourceTable    string
	SourceBackup   string
	ColumnFamilies map[string]ColumnFamily
	ExpireTime     time.Time
	StartTime      time.Time
	EndTime        time.Time
	SizeBytes      int64
	State          string
	BackupType     string
}

// Operation is a long-running operation. Done is always true in the mock; the
// resulting resource name is TargetName.
type Operation struct {
	Name       string
	Done       bool
	TargetName string
	Type       string
}

// Binding grants a role to members.
type Binding struct {
	Role    string
	Members []string
}

// Policy is an IAM policy on a resource.
type Policy struct {
	Version  int
	Bindings []Binding
	Etag     string
}

// CreateInstanceConfig is the input to CreateInstance. Clusters are the initial
// clusters supplied in the create request.
type CreateInstanceConfig struct {
	Name        string // full resource name
	DisplayName string
	Type        string
	Labels      map[string]string
	Clusters    []CreateClusterConfig
}

// CreateClusterConfig is the input to CreateCluster (and initial clusters).
type CreateClusterConfig struct {
	Name               string // full resource name
	Location           string
	ServeNodes         int
	DefaultStorageType string
	Autoscaling        *Autoscaling
}

// UpdateInstanceConfig carries the mutable instance fields.
type UpdateInstanceConfig struct {
	DisplayName string
	Type        string
	Labels      map[string]string
}

// CreateTableConfig is the input to CreateTable.
type CreateTableConfig struct {
	Parent             string // instance full name
	TableID            string
	ColumnFamilies     map[string]ColumnFamily
	Granularity        string
	DeletionProtection bool
}

// ColumnFamilyModification is one entry of a ModifyColumnFamilies request.
type ColumnFamilyModification struct {
	ID     string
	Create *ColumnFamily
	Update *ColumnFamily
	Drop   bool
}

// CreateAppProfileConfig is the input to CreateAppProfile.
type CreateAppProfileConfig struct {
	Parent                   string // instance full name
	AppProfileID             string
	Description              string
	MultiClusterRoutingAny   bool
	MultiClusterClusterIDs   []string
	SingleClusterID          string
	AllowTransactionalWrites bool
	Priority                 string
}

// CreateBackupConfig is the input to CreateBackup.
type CreateBackupConfig struct {
	Parent      string // cluster full name
	BackupID    string
	SourceTable string
	ExpireTime  time.Time
	BackupType  string
}

// CopyBackupConfig is the input to CopyBackup.
type CopyBackupConfig struct {
	Parent       string // target cluster full name
	BackupID     string
	SourceBackup string
	ExpireTime   time.Time
}

// Admin is the Google Cloud Bigtable Admin control plane.
//
//nolint:interfacebloat // mirrors the bigtableadmin/v2 control-plane surface.
type Admin interface {
	CreateInstance(ctx context.Context, cfg CreateInstanceConfig) (*Instance, *Operation, error)
	GetInstance(ctx context.Context, name string) (*Instance, error)
	ListInstances(ctx context.Context, project string) ([]Instance, error)
	UpdateInstance(ctx context.Context, name string, cfg UpdateInstanceConfig) (*Instance, error)
	PartialUpdateInstance(ctx context.Context, name string, cfg UpdateInstanceConfig) (*Instance, *Operation, error)
	DeleteInstance(ctx context.Context, name string) error

	CreateCluster(ctx context.Context, cfg CreateClusterConfig) (*Cluster, *Operation, error)
	GetCluster(ctx context.Context, name string) (*Cluster, error)
	ListClusters(ctx context.Context, instance string) ([]Cluster, error)
	UpdateCluster(ctx context.Context, name string, serveNodes int, autoscaling *Autoscaling) (*Cluster, *Operation, error)
	DeleteCluster(ctx context.Context, name string) error

	CreateTable(ctx context.Context, cfg CreateTableConfig) (*Table, error)
	GetTable(ctx context.Context, name string) (*Table, error)
	ListTables(ctx context.Context, instance string) ([]Table, error)
	UpdateTable(ctx context.Context, name string, deletionProtection *bool) (*Table, *Operation, error)
	DeleteTable(ctx context.Context, name string) error
	UndeleteTable(ctx context.Context, name string) (*Table, *Operation, error)
	ModifyColumnFamilies(ctx context.Context, name string, mods []ColumnFamilyModification) (*Table, error)
	DropRowRange(ctx context.Context, name string) error
	GenerateConsistencyToken(ctx context.Context, name string) (string, error)
	CheckConsistency(ctx context.Context, name, token string) (bool, error)
	RestoreTable(ctx context.Context, parent, tableID, backup string) (*Table, *Operation, error)

	CreateAppProfile(ctx context.Context, cfg CreateAppProfileConfig) (*AppProfile, error)
	GetAppProfile(ctx context.Context, name string) (*AppProfile, error)
	ListAppProfiles(ctx context.Context, instance string) ([]AppProfile, error)
	UpdateAppProfile(ctx context.Context, name string, cfg CreateAppProfileConfig) (*AppProfile, *Operation, error)
	DeleteAppProfile(ctx context.Context, name string) error

	CreateBackup(ctx context.Context, cfg CreateBackupConfig) (*Backup, *Operation, error)
	GetBackup(ctx context.Context, name string) (*Backup, error)
	ListBackups(ctx context.Context, cluster string) ([]Backup, error)
	UpdateBackup(ctx context.Context, name string, expireTime time.Time) (*Backup, error)
	DeleteBackup(ctx context.Context, name string) error
	CopyBackup(ctx context.Context, cfg CopyBackupConfig) (*Backup, *Operation, error)

	GetOperation(ctx context.Context, name string) (*Operation, error)

	GetIamPolicy(ctx context.Context, resource string) (*Policy, error)
	SetIamPolicy(ctx context.Context, resource string, policy Policy) (*Policy, error)
	TestIamPermissions(ctx context.Context, resource string, permissions []string) ([]string, error)

	GetClusterMemoryLayer(ctx context.Context, name string) error
}
