// Package driver defines the interface and types for AWS Athena
// implementations. It models the interactive-query control plane: workgroups
// (with their result/engine configuration), saved (named) queries, query
// executions, and the Data Catalog (databases, tables and registered data
// catalogs). AwsDataCatalog is backed by the Glue Data Catalog.
//
// There is no real Presto/Trino compute plane behind the emulator, so a started
// query execution settles to SUCCEEDED synchronously and DDL statements
// (CREATE/DROP DATABASE) mutate the Glue Data Catalog directly. Statement types
// are classified from the query text (DDL/DML/UTILITY) so callers that branch on
// StatementType behave as they would against real Athena.
package driver

import "context"

// Athena is the interface an AWS Athena backend implements. Workgroup and
// data-catalog names default to "primary" and "AwsDataCatalog" respectively when
// a caller omits them, matching real Athena.
type Athena interface {
	workGroupAPI
	namedQueryAPI
	queryExecutionAPI
	catalogAPI
	tagAPI
}

// workGroupAPI covers the workgroup control plane.
type workGroupAPI interface {
	// CreateWorkGroup creates a workgroup, materializing the effective engine
	// version and the default booleans (enforce=true, publish=false,
	// requester=false) that real Athena reports back from GetWorkGroup.
	CreateWorkGroup(ctx context.Context, wg WorkGroup) error
	// GetWorkGroup returns a deep copy of a workgroup, or an error tagged
	// InvalidRequestException when it does not exist.
	GetWorkGroup(ctx context.Context, name string) (*WorkGroup, error)
	// UpdateWorkGroup applies a delta: the top-level Description/State plus the
	// ConfigurationUpdates (which carry Remove* flags), never a full replace.
	UpdateWorkGroup(ctx context.Context, name string, upd WorkGroupUpdate) error
	// DeleteWorkGroup removes a workgroup. recursive deletes its named queries
	// and query executions too; without it a non-empty workgroup is rejected.
	DeleteWorkGroup(ctx context.Context, name string, recursive bool) error
	// ListWorkGroups returns workgroup summaries in a deterministic order.
	ListWorkGroups(ctx context.Context, page Pagination) ([]WorkGroupSummary, string, error)
}

// namedQueryAPI covers saved queries.
type namedQueryAPI interface {
	// CreateNamedQuery saves a query and returns its generated id. The
	// QueryString is stored byte-exact.
	CreateNamedQuery(ctx context.Context, nq NamedQuery) (string, error)
	GetNamedQuery(ctx context.Context, id string) (*NamedQuery, error)
	DeleteNamedQuery(ctx context.Context, id string) error
	// ListNamedQueries returns the ids of saved queries in the given workgroup
	// (default "primary") in a deterministic order.
	ListNamedQueries(ctx context.Context, workGroup string, page Pagination) ([]string, string, error)
}

// queryExecutionAPI covers query executions. Executions settle to SUCCEEDED
// synchronously; a CREATE/DROP DATABASE DDL statement mutates the catalog.
type queryExecutionAPI interface {
	// StartQueryExecution runs a query and returns its id. Re-issuing with the
	// same non-empty ClientRequestToken returns the original id (idempotency).
	StartQueryExecution(ctx context.Context, in StartQueryExecutionInput) (string, error)
	GetQueryExecution(ctx context.Context, id string) (*QueryExecution, error)
	// GetQueryResults returns the result rows for a SUCCEEDED execution. DDL and
	// most emulated statements have no rows, so the result set is empty; a FAILED
	// execution is rejected.
	GetQueryResults(ctx context.Context, id string, page Pagination) (*QueryResults, error)
	// StopQueryExecution is a no-op on an already-terminal execution; it errors
	// only when the id is unknown.
	StopQueryExecution(ctx context.Context, id string) error
	// ListQueryExecutions returns execution ids in the given workgroup
	// (default "primary"), most-recent first.
	ListQueryExecutions(ctx context.Context, workGroup string, page Pagination) ([]string, string, error)
}

// catalogAPI covers the Data Catalog: databases, tables and data catalogs. AwsDataCatalog is the
// AWS Glue Data Catalog, so databases and tables made through Glue show here
// and the ones Athena DDL makes show in Glue.
type catalogAPI interface {
	// GetDatabase returns a database in a data catalog, or an error tagged
	// MetadataException when absent.
	GetDatabase(ctx context.Context, catalogName, databaseName string) (*Database, error)
	ListDatabases(ctx context.Context, catalogName string, page Pagination) ([]Database, string, error)
	// GetTableMetadata returns a table's metadata, or an error tagged
	// MetadataException when the database or table is absent.
	GetTableMetadata(ctx context.Context, catalogName, databaseName, tableName string) (*TableMetadata, error)
	// ListTableMetadata lists the tables of a database. expression is a regex
	// filter on table names where "*" means ".*"; empty lists all.
	ListTableMetadata(ctx context.Context, catalogName, databaseName, expression string, page Pagination) ([]TableMetadata, string, error)
	// CreateDataCatalog registers a LAMBDA, GLUE, HIVE or FEDERATED catalog
	// after checking the parameters its type needs.
	CreateDataCatalog(ctx context.Context, in CreateDataCatalogInput) (*DataCatalog, error)
	// GetDataCatalog returns a data catalog, or an InvalidRequestException
	// "was not found" error when absent.
	GetDataCatalog(ctx context.Context, name string) (*DataCatalog, error)
	// ListDataCatalogs lists every catalog, AwsDataCatalog included, by name.
	ListDataCatalogs(ctx context.Context, page Pagination) ([]DataCatalogSummary, string, error)
	// UpdateDataCatalog changes a catalog's type, description or parameters.
	// AwsDataCatalog cannot be updated.
	UpdateDataCatalog(ctx context.Context, in UpdateDataCatalogInput) error
	// DeleteDataCatalog removes a catalog and returns it. AwsDataCatalog cannot
	// be deleted. deleteCatalogOnly is only valid for FEDERATED catalogs.
	DeleteDataCatalog(ctx context.Context, name string, deleteCatalogOnly bool) (*DataCatalog, error)
}

// tagAPI covers resource tagging, keyed by the resource ARN the caller supplies.
type tagAPI interface {
	TagResource(ctx context.Context, resourceARN string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceARN string) (map[string]string, error)
}
