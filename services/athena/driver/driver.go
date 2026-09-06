// Package driver defines the interface and types for AWS Athena
// implementations. It models the interactive-query control plane: workgroups
// (with their result/engine configuration), saved (named) queries, query
// executions, and the read side of the Data Catalog (databases and data
// catalogs) that the query-execution DDL path populates.
//
// There is no real Presto/Trino compute plane behind the emulator, so a started
// query execution settles to SUCCEEDED synchronously and DDL statements
// (CREATE/DROP DATABASE) mutate the in-memory catalog directly. Statement types
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

// catalogAPI covers the read side of the Data Catalog that the DDL path fills.
type catalogAPI interface {
	// GetDatabase returns a database in a data catalog, or an error tagged
	// ResourceNotFoundException when absent.
	GetDatabase(ctx context.Context, catalogName, databaseName string) (*Database, error)
	ListDatabases(ctx context.Context, catalogName string, page Pagination) ([]Database, string, error)
	GetDataCatalog(ctx context.Context, name string) (*DataCatalog, error)
	ListDataCatalogs(ctx context.Context, page Pagination) ([]DataCatalogSummary, string, error)
}

// tagAPI covers resource tagging, keyed by the resource ARN the caller supplies.
type tagAPI interface {
	TagResource(ctx context.Context, resourceARN string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceARN string) (map[string]string, error)
}
