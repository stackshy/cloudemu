// Package driver defines the interface and types for AWS Glue implementations.
// It models the Data Catalog (databases, tables, table versions, partitions,
// user-defined functions, connections, catalogs), crawlers, classifiers, ETL
// jobs and their runs, triggers, workflows, blueprints, security
// configurations, the schema registry (registries/schemas/versions), dev
// endpoints, and resource tags.
//
// Operations with real state (create/get/update/delete/list plus the
// job-run/crawler/workflow lifecycles) are modeled faithfully; read-only
// analytics, ML-transform, data-quality, integration, glossary, and
// column-statistics surfaces return plausible synthesized results because there
// is no real compute/data plane behind the emulator. Which is which is
// documented per method and in docs/services.md.
package driver

import "context"

// Glue is the interface an AWS Glue backend implements. Catalog IDs default to
// the account ID when a caller omits them. Names are validated before any
// lookup so a bad name is an InvalidInputException, an absent resource is an
// EntityNotFoundException, and a duplicate create is an AlreadyExistsException.
type Glue interface {
	catalogAPI
	crawlerAPI
	jobAPI
	triggerAPI
	workflowAPI
	registryAPI
	miscAPI
	synthesizedAPI
}

// catalogAPI covers the Data Catalog resource families with real state.
type catalogAPI interface {
	// Databases.
	CreateDatabase(ctx context.Context, catalogID string, db Database) error
	GetDatabase(ctx context.Context, catalogID, name string) (*Database, error)
	UpdateDatabase(ctx context.Context, catalogID, name string, db Database) error
	DeleteDatabase(ctx context.Context, catalogID, name string) error
	GetDatabases(ctx context.Context, catalogID string, page TablePagination) ([]Database, string, error)

	// Tables.
	CreateTable(ctx context.Context, catalogID, dbName string, tbl Table) error
	GetTable(ctx context.Context, catalogID, dbName, name string) (*Table, error)
	UpdateTable(ctx context.Context, catalogID, dbName string, tbl Table) error
	DeleteTable(ctx context.Context, catalogID, dbName, name string) error
	GetTables(ctx context.Context, catalogID, dbName string, page TablePagination) ([]Table, string, error)
	BatchDeleteTable(ctx context.Context, catalogID, dbName string, names []string) ([]BatchError, error)
	SearchTables(ctx context.Context, catalogID string, page TablePagination) ([]Table, string, error)

	// Table versions.
	GetTableVersion(ctx context.Context, catalogID, dbName, tblName, versionID string) (*TableVersion, error)
	GetTableVersions(ctx context.Context, catalogID, dbName, tblName string, page TablePagination) ([]TableVersion, string, error)
	DeleteTableVersion(ctx context.Context, catalogID, dbName, tblName, versionID string) error
	BatchDeleteTableVersion(ctx context.Context, catalogID, dbName, tblName string, ids []string) ([]BatchError, error)

	// Partitions.
	CreatePartition(ctx context.Context, catalogID, dbName, tblName string, p Partition) error
	GetPartition(ctx context.Context, catalogID, dbName, tblName string, values []string) (*Partition, error)
	UpdatePartition(ctx context.Context, catalogID, dbName, tblName string, oldValues []string, p Partition) error
	DeletePartition(ctx context.Context, catalogID, dbName, tblName string, values []string) error
	GetPartitions(ctx context.Context, catalogID, dbName, tblName string, page TablePagination) ([]Partition, string, error)
	BatchCreatePartition(ctx context.Context, catalogID, dbName, tblName string, ps []Partition) ([]BatchError, error)
	BatchDeletePartition(ctx context.Context, catalogID, dbName, tblName string, values [][]string) ([]BatchError, error)
	BatchUpdatePartition(ctx context.Context, catalogID, dbName, tblName string, entries []BatchUpdatePartitionEntry) ([]BatchError, error)
	BatchGetPartition(ctx context.Context, catalogID, dbName, tblName string, values [][]string) ([]Partition, [][]string, error)

	// User-defined functions.
	CreateUserDefinedFunction(ctx context.Context, catalogID, dbName string, fn UserDefinedFunction) error
	GetUserDefinedFunction(ctx context.Context, catalogID, dbName, name string) (*UserDefinedFunction, error)
	UpdateUserDefinedFunction(ctx context.Context, catalogID, dbName, name string, fn UserDefinedFunction) error
	DeleteUserDefinedFunction(ctx context.Context, catalogID, dbName, name string) error
	GetUserDefinedFunctions(ctx context.Context, catalogID, dbName string, page TablePagination) ([]UserDefinedFunction, string, error)

	// Connections.
	CreateConnection(ctx context.Context, catalogID string, c Connection) error
	GetConnection(ctx context.Context, catalogID, name string) (*Connection, error)
	UpdateConnection(ctx context.Context, catalogID, name string, c Connection) error
	DeleteConnection(ctx context.Context, catalogID, name string) error
	GetConnections(ctx context.Context, catalogID string, page TablePagination) ([]Connection, string, error)
	BatchDeleteConnection(ctx context.Context, catalogID string, names []string) (map[string]BatchError, error)
	TestConnection(ctx context.Context, name string) error

	// Catalogs.
	CreateCatalog(ctx context.Context, c Catalog) error
	GetCatalog(ctx context.Context, catalogID string) (*Catalog, error)
	UpdateCatalog(ctx context.Context, catalogID string, c Catalog) error
	DeleteCatalog(ctx context.Context, catalogID string) error
	GetCatalogs(ctx context.Context, page TablePagination) ([]Catalog, string, error)
}

// BatchUpdatePartitionEntry pairs the values that identify a partition with its
// new value in a BatchUpdatePartition request.
type BatchUpdatePartitionEntry struct {
	PartitionValueList []string
	Partition          Partition
}
