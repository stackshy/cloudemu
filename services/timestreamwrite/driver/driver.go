// Package driver defines the interface and types for the Amazon Timestream
// Write control-plane API (AWS JSON 1.0, X-Amz-Target prefix
// "Timestream_20181101."). It models Timestream databases and the tables that
// belong to them, plus resource tags.
//
// This is a control-plane-only surface: the emulator never ingests time-series
// records and never runs a query engine (WriteRecords and the Query API are out
// of scope). A database and a table are created directly in a stable, active
// shape so an IaC apply completes without a provisioning wait.
//
// The computed fields clients and IaC read back are minted once at create and
// stored, so repeated Describe/List reads and a later Update never drift: a
// database's Arn (arn:aws:timestream:<region>:<acct>:database/<name>), its
// KmsKeyID and CreationTime; a table's Arn
// (.../database/<db>/table/<name>), TableStatus and CreationTime. TableCount is
// derived live from the tables that belong to a database, so it always reflects
// reality. RetentionProperties round-trip verbatim, and the richer
// MagneticStoreWriteProperties and Schema blocks round-trip as raw JSON so a
// nested block cannot drift an IaC plan through a lossy re-marshal.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// TableStatusActive is the only status the emulator reports for a table. Real
// Timestream activates a table synchronously, so returning ACTIVE immediately
// lets an IaC waiter complete without a provisioning wait.
const TableStatusActive = "ACTIVE"

// DatabaseStatus is not modeled by the real API (a database has no status
// field), so only the table carries one.

// Tag is a resource tag (key/value pair). Timestream models tags as an array of
// {Key,Value} objects rather than a map.
type Tag struct {
	Key   string
	Value string
}

// RetentionProperties is the memory-store and magnetic-store retention of a
// table. Both members are required together on the wire and round-trip verbatim.
type RetentionProperties struct {
	MemoryStoreRetentionPeriodInHours  int64
	MagneticStoreRetentionPeriodInDays int64
}

// Database is a Timestream database. Arn, KmsKeyID and CreationTime are computed
// once at create and never regenerated, so repeated reads never drift;
// LastUpdatedTime is bumped on every mutation. TableCount is not stored — it is
// derived from the tables that belong to the database at read time.
type Database struct {
	Arn             string
	DatabaseName    string
	KmsKeyID        string
	TableCount      int64
	CreationTime    time.Time
	LastUpdatedTime time.Time
	Tags            []Tag
}

// Table is a Timestream table that belongs to a database. Arn, TableStatus and
// CreationTime are computed once at create; LastUpdatedTime is bumped on every
// mutation. RetentionProperties round-trip verbatim, and the
// MagneticStoreWriteProperties and Schema blocks round-trip as raw JSON.
type Table struct {
	Arn                          string
	TableName                    string
	DatabaseName                 string
	TableStatus                  string
	RetentionProperties          *RetentionProperties
	MagneticStoreWriteProperties json.RawMessage
	Schema                       json.RawMessage
	CreationTime                 time.Time
	LastUpdatedTime              time.Time
	Tags                         []Tag
}

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// CreateDatabaseInput is the input to CreateDatabase.
type CreateDatabaseInput struct {
	DatabaseName string
	KmsKeyID     string
	Tags         []Tag
}

// UpdateDatabaseInput is the input to UpdateDatabase. Only the KMS key is
// mutable on a database.
type UpdateDatabaseInput struct {
	DatabaseName string
	KmsKeyID     string
}

// CreateTableInput is the input to CreateTable.
type CreateTableInput struct {
	DatabaseName                 string
	TableName                    string
	RetentionProperties          *RetentionProperties
	MagneticStoreWriteProperties json.RawMessage
	Schema                       json.RawMessage
	Tags                         []Tag
}

// UpdateTableInput is the input to UpdateTable. A nil pointer/raw means the
// parameter was omitted and the stored value is left unchanged.
type UpdateTableInput struct {
	DatabaseName                 string
	TableName                    string
	RetentionProperties          *RetentionProperties
	MagneticStoreWriteProperties json.RawMessage
	Schema                       json.RawMessage
}

// Timestream is the Amazon Timestream Write control-plane surface: databases,
// the tables that belong to them, and resource tags.
type Timestream interface {
	CreateDatabase(ctx context.Context, in *CreateDatabaseInput) (*Database, error)
	DescribeDatabase(ctx context.Context, name string) (*Database, error)
	UpdateDatabase(ctx context.Context, in *UpdateDatabaseInput) (*Database, error)
	DeleteDatabase(ctx context.Context, name string) error
	ListDatabases(ctx context.Context, page Page) (databases []Database, nextToken string, err error)

	CreateTable(ctx context.Context, in *CreateTableInput) (*Table, error)
	DescribeTable(ctx context.Context, databaseName, tableName string) (*Table, error)
	UpdateTable(ctx context.Context, in *UpdateTableInput) (*Table, error)
	DeleteTable(ctx context.Context, databaseName, tableName string) error
	ListTables(ctx context.Context, databaseName string, page Page) (tables []Table, nextToken string, err error)

	TagResource(ctx context.Context, resourceARN string, tags []Tag) error
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceARN string) ([]Tag, error)
}
