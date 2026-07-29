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

// DatabaseConfig describes a logical database to create inside a server.
type DatabaseConfig struct {
	Server    string
	Name      string
	Charset   string
	Collation string
}

// Database is a logical database hosted by a managed server (Azure MySQL /
// PostgreSQL Flexible Server, Cloud SQL, Azure SQL).
type Database struct {
	Server    string
	Name      string
	Charset   string
	Collation string
	ARN       string
}

// Databases is an OPTIONAL capability for managing the logical databases inside
// a server. It is discovered by type assertion; drivers that do not implement
// it answer InvalidAction.
type Databases interface {
	CreateDatabase(ctx context.Context, cfg DatabaseConfig) (*Database, error)
	GetDatabase(ctx context.Context, server, name string) (*Database, error)
	ListDatabases(ctx context.Context, server string) ([]Database, error)
	DeleteDatabase(ctx context.Context, server, name string) error
}

// FirewallRuleConfig describes a server firewall rule to create or replace.
type FirewallRuleConfig struct {
	Server         string
	Name           string
	StartIPAddress string
	EndIPAddress   string
}

// FirewallRule is a server-level IP allow rule.
type FirewallRule struct {
	Server         string
	Name           string
	StartIPAddress string
	EndIPAddress   string
	ARN            string
}

// FirewallRules is an OPTIONAL capability for managing server firewall rules,
// discovered by type assertion.
type FirewallRules interface {
	CreateFirewallRule(ctx context.Context, cfg FirewallRuleConfig) (*FirewallRule, error)
	GetFirewallRule(ctx context.Context, server, name string) (*FirewallRule, error)
	ListFirewallRules(ctx context.Context, server string) ([]FirewallRule, error)
	DeleteFirewallRule(ctx context.Context, server, name string) error
}

// ConfigurationConfig sets a single server parameter value.
type ConfigurationConfig struct {
	Server string
	Name   string
	Value  string
}

// Configuration is a server parameter (engine setting). DefaultValue,
// DataType and AllowedValues describe the parameter; Source records whether the
// current value is a user override or the system default.
type Configuration struct {
	Server        string
	Name          string
	Value         string
	Source        string
	DataType      string
	DefaultValue  string
	AllowedValues string
	ARN           string
}

// Configurations is an OPTIONAL capability for reading and setting server
// parameters, discovered by type assertion. Parameters have engine defaults, so
// there is no create/delete — only set (update), get and list.
type Configurations interface {
	SetConfiguration(ctx context.Context, cfg ConfigurationConfig) (*Configuration, error)
	GetConfiguration(ctx context.Context, server, name string) (*Configuration, error)
	ListConfigurations(ctx context.Context, server string) ([]Configuration, error)
}

// Failover is an OPTIONAL capability that triggers a server failover to its
// standby, discovered by type assertion.
type Failover interface {
	FailoverInstance(ctx context.Context, id string) error
}

// VNetRuleConfig describes a virtual-network rule to create (Azure SQL).
type VNetRuleConfig struct {
	Server                string
	Name                  string
	SubnetID              string
	IgnoreMissingEndpoint bool
}

// VNetRule allows traffic from a virtual-network subnet to a server.
type VNetRule struct {
	Server                string
	Name                  string
	SubnetID              string
	IgnoreMissingEndpoint bool
	State                 string
	ARN                   string
}

// VNetRules is an OPTIONAL Azure SQL capability, discovered by type assertion.
type VNetRules interface {
	CreateVNetRule(ctx context.Context, cfg VNetRuleConfig) (*VNetRule, error)
	GetVNetRule(ctx context.Context, server, name string) (*VNetRule, error)
	ListVNetRules(ctx context.Context, server string) ([]VNetRule, error)
	DeleteVNetRule(ctx context.Context, server, name string) error
}

// ElasticPoolConfig describes an elastic pool to create (Azure SQL).
type ElasticPoolConfig struct {
	Server       string
	Name         string
	Location     string
	SKUName      string
	SKUTier      string
	MaxSizeBytes int64
	MinCapacity  float64
	MaxCapacity  float64
}

// ElasticPool is a shared-resource pool that databases on a server draw from.
type ElasticPool struct {
	Server       string
	Name         string
	Location     string
	SKUName      string
	SKUTier      string
	MaxSizeBytes int64
	MinCapacity  float64
	MaxCapacity  float64
	State        string
	ARN          string
}

// ElasticPools is an OPTIONAL Azure SQL capability, discovered by type assertion.
type ElasticPools interface {
	CreateElasticPool(ctx context.Context, cfg ElasticPoolConfig) (*ElasticPool, error)
	GetElasticPool(ctx context.Context, server, name string) (*ElasticPool, error)
	ListElasticPools(ctx context.Context, server string) ([]ElasticPool, error)
	DeleteElasticPool(ctx context.Context, server, name string) error
}

// FailoverGroupConfig describes a failover group to create (Azure SQL).
type FailoverGroupConfig struct {
	Server             string
	Name               string
	FailoverPolicy     string
	GracePeriodMinutes int32
	PartnerServers     []string
	Databases          []string
}

// FailoverGroup groups databases that fail over together to a partner server.
type FailoverGroup struct {
	Server             string
	Name               string
	FailoverPolicy     string
	GracePeriodMinutes int32
	PartnerServers     []string
	Databases          []string
	ReplicationRole    string
	ARN                string
}

// FailoverGroups is an OPTIONAL Azure SQL capability, discovered by type
// assertion. Failover flips the local replication role between Primary and
// Secondary.
type FailoverGroups interface {
	CreateFailoverGroup(ctx context.Context, cfg FailoverGroupConfig) (*FailoverGroup, error)
	GetFailoverGroup(ctx context.Context, server, name string) (*FailoverGroup, error)
	ListFailoverGroups(ctx context.Context, server string) ([]FailoverGroup, error)
	DeleteFailoverGroup(ctx context.Context, server, name string) error
	FailoverFailoverGroup(ctx context.Context, server, name string) (*FailoverGroup, error)
}

// AADAdminConfig sets the Azure AD administrator on a server (Azure SQL).
type AADAdminConfig struct {
	Server   string
	Login    string
	SID      string
	TenantID string
}

// AADAdmin is a server's Azure Active Directory administrator. A server has at
// most one; Name is always "ActiveDirectory".
type AADAdmin struct {
	Server   string
	Name     string
	Login    string
	SID      string
	TenantID string
	ARN      string
}

// AADAdmins is an OPTIONAL Azure SQL capability, discovered by type assertion.
type AADAdmins interface {
	SetAADAdmin(ctx context.Context, cfg AADAdminConfig) (*AADAdmin, error)
	GetAADAdmin(ctx context.Context, server, name string) (*AADAdmin, error)
	ListAADAdmins(ctx context.Context, server string) ([]AADAdmin, error)
	DeleteAADAdmin(ctx context.Context, server, name string) error
}
