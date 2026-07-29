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
	ID                   string
	Engine               string // "mysql", "postgres", "aurora-mysql", "aurora-postgresql", …
	EngineVersion        string
	InstanceClass        string // "db.t3.micro", …
	AllocatedStorage     int    // GiB
	StorageType          string // "gp2", "io1", …
	MasterUsername       string
	MasterUserPassword   string
	DBName               string // optional initial DB name
	Port                 int
	MultiAZ              bool
	PubliclyAccessible   bool
	VPCSecurityGroups    []string
	SubnetGroupName      string
	DBParameterGroupName string
	OptionGroupName      string
	ClusterID            string // empty for standalone, set for Aurora cluster members
	AvailabilityZone     string
	Tags                 map[string]string
}

// Instance describes a managed database instance.
type Instance struct {
	ID                   string
	ARN                  string
	Engine               string
	EngineVersion        string
	InstanceClass        string
	AllocatedStorage     int
	StorageType          string
	MasterUsername       string
	DBName               string
	Endpoint             string
	Port                 int
	State                string
	MultiAZ              bool
	PubliclyAccessible   bool
	VPCSecurityGroups    []string
	SubnetGroupName      string
	DBParameterGroupName string
	OptionGroupName      string
	ClusterID            string
	AvailabilityZone     string
	CreatedAt            time.Time
	Tags                 map[string]string
	// ReadReplicaSource is the identifier of the primary this instance
	// replicates from; empty for a primary. ReadReplicaTargets lists the
	// replica identifiers reading from this instance.
	ReadReplicaSource  string
	ReadReplicaTargets []string
}

// ModifyInstanceInput holds modifiable instance (and cluster) attributes.
// Zero-valued fields mean "no change". DBParameterGroupName/OptionGroupName
// apply to instances; DBClusterParameterGroupName applies to clusters (the
// same input type backs ModifyInstance and ModifyCluster).
type ModifyInstanceInput struct {
	InstanceClass               string
	AllocatedStorage            int
	EngineVersion               string
	MasterUserPassword          string
	MultiAZ                     *bool
	DBParameterGroupName        string
	OptionGroupName             string
	DBClusterParameterGroupName string
	Tags                        map[string]string
}

// ClusterConfig configures an Aurora-style cluster. Members are added by
// calling CreateInstance with ClusterID set.
type ClusterConfig struct {
	ID                          string
	Engine                      string // "aurora-mysql" or "aurora-postgresql"
	EngineVersion               string
	MasterUsername              string
	MasterUserPassword          string
	DatabaseName                string
	Port                        int
	VPCSecurityGroups           []string
	SubnetGroupName             string
	DBClusterParameterGroupName string
	Tags                        map[string]string
}

// Cluster describes an Aurora-style database cluster.
type Cluster struct {
	ID                          string
	ARN                         string
	Engine                      string
	EngineVersion               string
	MasterUsername              string
	DatabaseName                string
	Endpoint                    string
	ReaderEndpoint              string
	Port                        int
	State                       string
	Members                     []string // instance IDs
	VPCSecurityGroups           []string
	SubnetGroupName             string
	DBClusterParameterGroupName string
	CreatedAt                   time.Time
	Tags                        map[string]string
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
	UpdateElasticPool(ctx context.Context, cfg ElasticPoolConfig) (*ElasticPool, error)
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
	UpdateFailoverGroup(ctx context.Context, cfg FailoverGroupConfig) (*FailoverGroup, error)
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

// ManagedInstanceConfig describes an Azure SQL Managed Instance to create.
type ManagedInstanceConfig struct {
	Name        string
	Location    string
	AdminLogin  string
	SKUName     string
	SKUTier     string
	LicenseType string
	SubnetID    string
	VCores      int
	StorageGB   int
	Tags        map[string]string
}

// ManagedInstance is a SQL Managed Instance — a fully-managed instance that
// hosts managed databases, distinct from the single-database logical server.
type ManagedInstance struct {
	Name        string
	Location    string
	AdminLogin  string
	SKUName     string
	SKUTier     string
	LicenseType string
	SubnetID    string
	VCores      int
	StorageGB   int
	State       string
	FQDN        string
	ARN         string
	Tags        map[string]string
}

// ManagedDatabaseConfig describes a database on a managed instance.
type ManagedDatabaseConfig struct {
	Instance  string
	Name      string
	Collation string
}

// ManagedDatabase is a database hosted on a managed instance.
type ManagedDatabase struct {
	Instance  string
	Name      string
	Collation string
	Status    string
	ARN       string
}

// ManagedInstances is an OPTIONAL Azure SQL capability covering SQL Managed
// Instances and their managed databases, discovered by type assertion.
type ManagedInstances interface {
	CreateManagedInstance(ctx context.Context, cfg ManagedInstanceConfig) (*ManagedInstance, error)
	UpdateManagedInstance(ctx context.Context, cfg ManagedInstanceConfig) (*ManagedInstance, error)
	GetManagedInstance(ctx context.Context, name string) (*ManagedInstance, error)
	ListManagedInstances(ctx context.Context) ([]ManagedInstance, error)
	DeleteManagedInstance(ctx context.Context, name string) error
	StartManagedInstance(ctx context.Context, name string) error
	StopManagedInstance(ctx context.Context, name string) error
	FailoverManagedInstance(ctx context.Context, name string) error

	CreateManagedDatabase(ctx context.Context, cfg ManagedDatabaseConfig) (*ManagedDatabase, error)
	GetManagedDatabase(ctx context.Context, instance, name string) (*ManagedDatabase, error)
	ListManagedDatabases(ctx context.Context, instance string) ([]ManagedDatabase, error)
	DeleteManagedDatabase(ctx context.Context, instance, name string) error
}

// UserConfig describes a database user to create or update (Cloud SQL).
type UserConfig struct {
	Instance string
	Name     string
	Host     string
	Password string
}

// User is a database user account on a server/instance.
type User struct {
	Instance string
	Name     string
	Host     string
}

// Users is an OPTIONAL capability for managing database user accounts,
// discovered by type assertion.
type Users interface {
	CreateUser(ctx context.Context, cfg UserConfig) (*User, error)
	GetUser(ctx context.Context, instance, name string) (*User, error)
	ListUsers(ctx context.Context, instance string) ([]User, error)
	UpdateUser(ctx context.Context, cfg UserConfig) (*User, error)
	DeleteUser(ctx context.Context, instance, name string) error
}

// SslCertConfig describes a client SSL certificate to create (Cloud SQL).
type SslCertConfig struct {
	Instance   string
	CommonName string
}

// SslCert is a client SSL certificate for connecting to an instance. The mock
// derives a deterministic fingerprint from the common name and returns a
// placeholder PEM so SDK round-trips carry a well-formed shape.
type SslCert struct {
	Instance        string
	CommonName      string
	Sha1Fingerprint string
	Cert            string
	SerialNumber    string
}

// SslCerts is an OPTIONAL capability for managing client SSL certificates,
// discovered by type assertion.
type SslCerts interface {
	CreateSslCert(ctx context.Context, cfg SslCertConfig) (*SslCert, error)
	GetSslCert(ctx context.Context, instance, sha1 string) (*SslCert, error)
	ListSslCerts(ctx context.Context, instance string) ([]SslCert, error)
	DeleteSslCert(ctx context.Context, instance, sha1 string) error
}

// Clonable is an OPTIONAL capability that copies an instance to a new one
// (Cloud SQL), discovered by type assertion.
type Clonable interface {
	CloneInstance(ctx context.Context, sourceID, destID string) (*Instance, error)
}

// ReplicaPromotion is an OPTIONAL capability that promotes a read replica to a
// standalone primary (Cloud SQL), discovered by type assertion.
type ReplicaPromotion interface {
	PromoteReplica(ctx context.Context, id string) error
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
// Parameters is keyed by parameter name and retains each parameter's apply
// method.
type ParameterGroup struct {
	Name        string
	Family      string
	Description string
	ARN         string
	Parameters  map[string]Parameter
}

// ClusterParameterGroup is the cluster-scoped analog of ParameterGroup.
type ClusterParameterGroup struct {
	Name        string
	Family      string
	Description string
	ARN         string
	Parameters  map[string]Parameter
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

// ProxyAuth is one authentication config entry on a DB proxy.
type ProxyAuth struct {
	AuthScheme             string // "SECRETS"
	SecretARN              string
	IAMAuth                string // "DISABLED" | "REQUIRED"
	Description            string
	ClientPasswordAuthType string
}

// DBProxyConfig configures a new DB proxy.
type DBProxyConfig struct {
	Name                string
	EngineFamily        string // "MYSQL" | "POSTGRESQL" | "SQLSERVER"
	RoleARN             string
	VPCSubnetIDs        []string
	VPCSecurityGroupIDs []string
	RequireTLS          bool
	IdleClientTimeout   int
	DebugLogging        bool
	Auth                []ProxyAuth
	Tags                map[string]string
}

// ModifyDBProxyInput holds modifiable proxy attributes; nil pointers mean "no
// change".
type ModifyDBProxyInput struct {
	RequireTLS        *bool
	IdleClientTimeout *int
	DebugLogging      *bool
	RoleARN           string
}

// ProxyTarget is an instance or cluster registered behind a proxy.
type ProxyTarget struct {
	Type          string // "RDS_INSTANCE" | "TRACKED_CLUSTER"
	RDSResourceID string
	Endpoint      string
	Port          int
}

// ProxyTargetGroup is a proxy's connection-pool target group. Each proxy has a
// single implicit "default" group.
type ProxyTargetGroup struct {
	Name      string
	ProxyName string
	ARN       string
	IsDefault bool
}

// DBProxy is an RDS Proxy in front of one or more instances/clusters.
type DBProxy struct {
	Name                string
	ARN                 string
	Status              string
	EngineFamily        string
	RoleARN             string
	Endpoint            string
	VPCSubnetIDs        []string
	VPCSecurityGroupIDs []string
	RequireTLS          bool
	IdleClientTimeout   int
	DebugLogging        bool
	Auth                []ProxyAuth
	CreatedAt           time.Time
	Targets             []ProxyTarget
}

// EventSubscriptionConfig configures a new RDS event subscription.
type EventSubscriptionConfig struct {
	Name            string
	SnsTopicARN     string
	SourceType      string // "db-instance", "db-cluster", "db-snapshot", …
	EventCategories []string
	SourceIDs       []string
	Enabled         bool
	Tags            map[string]string
}

// EventSubscription is an RDS event notification subscription.
type EventSubscription struct {
	Name            string
	ARN             string
	CustomerAWSID   string
	SnsTopicARN     string
	SourceType      string
	Status          string
	EventCategories []string
	SourceIDs       []string
	Enabled         bool
	CreatedAt       time.Time
}

// ModifyEventSubscriptionInput holds modifiable subscription attributes; nil /
// zero fields mean "no change".
type ModifyEventSubscriptionInput struct {
	SnsTopicARN     string
	SourceType      string
	EventCategories []string
	Enabled         *bool
}

// Event is a single RDS event.
type Event struct {
	SourceIdentifier string
	SourceType       string
	Message          string
	EventCategories  []string
	Date             time.Time
}

// EventCategoryGroup lists the event categories available for a source type.
type EventCategoryGroup struct {
	SourceType      string
	EventCategories []string
}

// EventSubscriptions is an OPTIONAL capability for RDS event subscriptions and
// event queries, discovered by type assertion.
type EventSubscriptions interface {
	CreateEventSubscription(ctx context.Context, cfg EventSubscriptionConfig) (*EventSubscription, error)
	DescribeEventSubscriptions(ctx context.Context, names []string) ([]EventSubscription, error)
	ModifyEventSubscription(ctx context.Context, name string, input ModifyEventSubscriptionInput) (*EventSubscription, error)
	DeleteEventSubscription(ctx context.Context, name string) (*EventSubscription, error)
	DescribeEvents(ctx context.Context, sourceType, sourceIdentifier string, categories []string) ([]Event, error)
	DescribeEventCategories(ctx context.Context, sourceType string) ([]EventCategoryGroup, error)
}

// DBProxies is an OPTIONAL capability for RDS Proxy, discovered by type
// assertion. A proxy has a single implicit "default" target group.
type DBProxies interface {
	CreateDBProxy(ctx context.Context, cfg DBProxyConfig) (*DBProxy, error)
	DescribeDBProxies(ctx context.Context, names []string) ([]DBProxy, error)
	ModifyDBProxy(ctx context.Context, name string, input ModifyDBProxyInput) (*DBProxy, error)
	DeleteDBProxy(ctx context.Context, name string) (*DBProxy, error)
	RegisterDBProxyTargets(ctx context.Context, name, targetGroup string, instanceIDs, clusterIDs []string) ([]ProxyTarget, error)
	DeregisterDBProxyTargets(ctx context.Context, name, targetGroup string, instanceIDs, clusterIDs []string) error
	DescribeDBProxyTargets(ctx context.Context, name, targetGroup string) ([]ProxyTarget, error)
	DescribeDBProxyTargetGroups(ctx context.Context, name string) ([]ProxyTargetGroup, error)
}

// ClusterEndpointConfig configures a custom Aurora cluster endpoint.
type ClusterEndpointConfig struct {
	EndpointID      string
	ClusterID       string
	EndpointType    string // "READER" | "ANY"
	StaticMembers   []string
	ExcludedMembers []string
}

// ModifyClusterEndpointInput holds modifiable custom-endpoint attributes.
type ModifyClusterEndpointInput struct {
	EndpointType    string
	StaticMembers   []string
	ExcludedMembers []string
}

// ClusterEndpoint is a custom Aurora cluster endpoint.
type ClusterEndpoint struct {
	EndpointID         string
	ClusterID          string
	ARN                string
	Endpoint           string
	Status             string
	EndpointType       string // always "CUSTOM" for user-created endpoints
	CustomEndpointType string // "READER" | "ANY"
	StaticMembers      []string
	ExcludedMembers    []string
}

// ClusterEndpoints is an OPTIONAL capability for Aurora custom cluster
// endpoints, discovered by type assertion.
type ClusterEndpoints interface {
	CreateDBClusterEndpoint(ctx context.Context, cfg ClusterEndpointConfig) (*ClusterEndpoint, error)
	DescribeDBClusterEndpoints(ctx context.Context, clusterID, endpointID string) ([]ClusterEndpoint, error)
	ModifyDBClusterEndpoint(ctx context.Context, endpointID string, input ModifyClusterEndpointInput) (*ClusterEndpoint, error)
	DeleteDBClusterEndpoint(ctx context.Context, endpointID string) (*ClusterEndpoint, error)
}

// ClusterFailover is an OPTIONAL capability to fail a cluster over to a
// specific member, discovered by type assertion.
type ClusterFailover interface {
	FailoverDBCluster(ctx context.Context, clusterID, targetInstanceID string) (*Cluster, error)
}

// GlobalClusterMember is a cluster participating in an Aurora global cluster.
type GlobalClusterMember struct {
	DBClusterARN string
	IsWriter     bool
}

// GlobalClusterConfig configures a new Aurora global cluster.
type GlobalClusterConfig struct {
	ID                string
	Engine            string
	EngineVersion     string
	SourceDBClusterID string // optional primary cluster
	Tags              map[string]string
}

// GlobalCluster is an Aurora global (multi-region) cluster.
type GlobalCluster struct {
	ID            string
	ARN           string
	Engine        string
	EngineVersion string
	Status        string
	Members       []GlobalClusterMember
}

// GlobalClusters is an OPTIONAL capability for Aurora global clusters,
// discovered by type assertion.
type GlobalClusters interface {
	CreateGlobalCluster(ctx context.Context, cfg GlobalClusterConfig) (*GlobalCluster, error)
	DescribeGlobalClusters(ctx context.Context, ids []string) ([]GlobalCluster, error)
	ModifyGlobalCluster(ctx context.Context, id, newID, engineVersion string) (*GlobalCluster, error)
	DeleteGlobalCluster(ctx context.Context, id string) (*GlobalCluster, error)
	RemoveFromGlobalCluster(ctx context.Context, id, clusterARN string) (*GlobalCluster, error)
}

// DBEngineVersion describes an available engine version.
type DBEngineVersion struct {
	Engine                 string
	EngineVersion          string
	DBEngineDescription    string
	DBParameterGroupFamily string
}

// OrderableDBInstanceOption describes an orderable instance configuration.
type OrderableDBInstanceOption struct {
	Engine          string
	EngineVersion   string
	DBInstanceClass string
	StorageType     string
	MultiAZCapable  bool
}

// Metadata is an OPTIONAL capability exposing the engine-version and
// orderable-instance-option catalogs, discovered by type assertion.
type Metadata interface {
	DescribeDBEngineVersions(ctx context.Context, engine, engineVersion string) ([]DBEngineVersion, error)
	DescribeOrderableDBInstanceOptions(ctx context.Context, engine, engineVersion string) ([]OrderableDBInstanceOption, error)
}

// Tagging is an OPTIONAL capability for resource-level tag operations,
// discovered by type assertion. Tags are addressed by resource ARN.
type Tagging interface {
	AddTagsToResource(ctx context.Context, resourceARN string, tags map[string]string) error
	RemoveTagsFromResource(ctx context.Context, resourceARN string, keys []string) error
	ListTagsForResource(ctx context.Context, resourceARN string) (map[string]string, error)
}
