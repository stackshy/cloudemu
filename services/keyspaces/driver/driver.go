// Package driver defines the portable interface for an Amazon Keyspaces
// (Apache Cassandra–compatible) control plane. Keyspaces is control-plane only
// here — CQL data operations are out of scope — so this driver is independent
// of the cache/relational drivers.
package driver

import (
	"context"
	"time"
)

// Resource lifecycle states.
const (
	StatusCreating  = "CREATING"
	StatusActive    = "ACTIVE"
	StatusUpdating  = "UPDATING"
	StatusDeleting  = "DELETING"
	StatusRestoring = "RESTORING"
)

// Replication strategies for a keyspace.
const (
	SingleRegion = "SINGLE_REGION"
	MultiRegion  = "MULTI_REGION"
)

// Throughput modes for a table's capacity specification.
const (
	ThroughputPayPerRequest = "PAY_PER_REQUEST"
	ThroughputProvisioned   = "PROVISIONED"
)

// Tag is a key/value label on a keyspace or table.
type Tag struct {
	Key   string
	Value string
}

// Keyspace is a Cassandra keyspace (a namespace of tables).
type Keyspace struct {
	Name                string
	ARN                 string
	ReplicationStrategy string
	ReplicationRegions  []string
	Tags                map[string]string
}

// PartitionKey names a column that is part of the partition key.
type PartitionKey struct{ Name string }

// ClusteringKey names a clustering column and its sort order (ASC/DESC).
type ClusteringKey struct {
	Name    string
	OrderBy string
}

// ColumnDefinition is a regular column name + Cassandra type.
type ColumnDefinition struct {
	Name string
	Type string
}

// StaticColumn names a static column (shared across a partition's rows).
type StaticColumn struct{ Name string }

// SchemaDefinition describes a table's columns and primary key.
type SchemaDefinition struct {
	AllColumns     []ColumnDefinition
	PartitionKeys  []PartitionKey
	ClusteringKeys []ClusteringKey
	StaticColumns  []StaticColumn
}

// CapacitySpecification is a table's read/write capacity mode + provisioned
// units (units apply only in PROVISIONED mode).
type CapacitySpecification struct {
	ThroughputMode     string
	ReadCapacityUnits  int64
	WriteCapacityUnits int64
}

// EncryptionSpecification is a table's encryption-at-rest configuration.
type EncryptionSpecification struct {
	Type             string
	KmsKeyIdentifier string
}

// AutoScalingSettings holds the target-tracking auto-scaling config for one
// capacity dimension (read or write) of a provisioned table.
type AutoScalingSettings struct {
	AutoScalingDisabled bool
	MinimumUnits        int64
	MaximumUnits        int64
	TargetValue         float64
	DisableScaleIn      bool
	ScaleInCooldown     int
	ScaleOutCooldown    int
}

// AutoScalingSpecification bundles the read/write auto-scaling settings.
type AutoScalingSpecification struct {
	Read  *AutoScalingSettings
	Write *AutoScalingSettings
}

// Table is a Cassandra table within a keyspace.
type Table struct {
	KeyspaceName              string
	Name                      string
	ARN                       string
	Status                    string
	SchemaDefinition          SchemaDefinition
	CapacitySpecification     CapacitySpecification
	EncryptionSpecification   EncryptionSpecification
	PointInTimeRecoveryStatus string
	TTLStatus                 string
	DefaultTimeToLive         int
	ClientSideTimestamps      string
	CdcStatus                 string
	Comment                   string
	ReplicaRegions            []string
	AutoScaling               *AutoScalingSpecification
	CreationTimestamp         time.Time
	Tags                      map[string]string
}

// FieldDefinition is a name/type pair inside a user-defined type.
type FieldDefinition struct {
	Name string
	Type string
}

// UDT is a user-defined type within a keyspace.
type UDT struct {
	KeyspaceName          string
	KeyspaceARN           string
	Name                  string
	Status                string
	FieldDefinitions      []FieldDefinition
	DirectParentTypes     []string
	DirectReferringTables []string
	MaxNestingDepth       int
	LastModified          time.Time
}

// CreateKeyspaceConfig is the input to CreateKeyspace.
type CreateKeyspaceConfig struct {
	Name                string
	ReplicationStrategy string
	ReplicationRegions  []string
	Tags                map[string]string
}

// CreateTableConfig is the input to CreateTable.
type CreateTableConfig struct {
	KeyspaceName            string
	Name                    string
	SchemaDefinition        SchemaDefinition
	CapacitySpecification   CapacitySpecification
	EncryptionSpecification EncryptionSpecification
	PointInTimeRecovery     string
	TTLStatus               string
	DefaultTimeToLive       int
	ClientSideTimestamps    string
	CdcStatus               string
	Comment                 string
	ReplicaRegions          []string
	AutoScaling             *AutoScalingSpecification
	Tags                    map[string]string
}

// UpdateTableConfig is the input to UpdateTable. Zero-valued fields are left
// unchanged; AddColumns are appended to the schema.
type UpdateTableConfig struct {
	KeyspaceName          string
	Name                  string
	AddColumns            []ColumnDefinition
	CapacitySpecification *CapacitySpecification
	PointInTimeRecovery   string
	TTLStatus             string
	DefaultTimeToLive     *int
	ClientSideTimestamps  string
	CdcStatus             string
	Comment               string
	AutoScaling           *AutoScalingSpecification
}

// RestoreTableConfig is the input to RestoreTable (point-in-time recovery).
type RestoreTableConfig struct {
	SourceKeyspace          string
	SourceTable             string
	TargetKeyspace          string
	TargetTable             string
	CapacitySpecification   *CapacitySpecification
	EncryptionSpecification *EncryptionSpecification
	PointInTimeRecovery     string
	RestoreTimestamp        time.Time
	Tags                    map[string]string
}

// Keyspaces is the core Amazon Keyspaces control plane.
//
//nolint:interfacebloat // mirrors the AWS Keyspaces control-plane surface.
type Keyspaces interface {
	CreateKeyspace(ctx context.Context, cfg CreateKeyspaceConfig) (*Keyspace, error)
	GetKeyspace(ctx context.Context, name string) (*Keyspace, error)
	ListKeyspaces(ctx context.Context) ([]Keyspace, error)
	UpdateKeyspace(ctx context.Context, name string, addRegions []string) (*Keyspace, error)
	DeleteKeyspace(ctx context.Context, name string) error

	CreateTable(ctx context.Context, cfg CreateTableConfig) (*Table, error)
	GetTable(ctx context.Context, keyspace, table string) (*Table, error)
	ListTables(ctx context.Context, keyspace string) ([]Table, error)
	UpdateTable(ctx context.Context, cfg UpdateTableConfig) (*Table, error)
	DeleteTable(ctx context.Context, keyspace, table string) error
	RestoreTable(ctx context.Context, cfg RestoreTableConfig) (*Table, error)

	CreateType(ctx context.Context, keyspace, name string, fields []FieldDefinition) (*UDT, error)
	GetType(ctx context.Context, keyspace, name string) (*UDT, error)
	ListTypes(ctx context.Context, keyspace string) ([]UDT, error)
	DeleteType(ctx context.Context, keyspace, name string) (*UDT, error)

	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, keys []string) error
	ListTagsForResource(ctx context.Context, arn string) ([]Tag, error)
}

// AutoScaling is an OPTIONAL capability (provisioned-throughput tables),
// discovered by type assertion.
type AutoScaling interface {
	GetTableAutoScalingSettings(ctx context.Context, keyspace, table string) (*Table, error)
}
