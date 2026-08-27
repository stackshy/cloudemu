// Package driver defines the interface for Azure Table Storage-style
// key/value entity stores. Entities live inside named tables and are addressed
// by a (PartitionKey, RowKey) pair; each entity is a flat bag of named
// properties.
//
// The interface is intentionally small — it mirrors the operations the Azure
// Table data-plane REST API (aztables) exercises, nothing more.
package driver

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// UpdateMode selects how UpdateEntity combines the supplied properties with an
// existing entity.
type UpdateMode int

const (
	// UpdateModeMerge merges the supplied properties into the existing entity,
	// leaving unmentioned properties untouched.
	UpdateModeMerge UpdateMode = iota
	// UpdateModeReplace replaces the entity wholesale with the supplied
	// properties.
	UpdateModeReplace
)

// Entity is a single Table Storage row: a flat map of property name to value.
// PartitionKey and RowKey are stored as ordinary properties (keys
// "PartitionKey" and "RowKey") so callers get them back verbatim. On read, the
// backend also injects the system properties "Timestamp" (Edm.DateTime string)
// and "odata.etag" so responses match the real service.
type Entity map[string]any

// MatchAny is the wildcard If-Match value that matches any current ETag, making
// a conditional operation unconditional (mirrors HTTP If-Match: *).
const MatchAny = "*"

// QueryOptions filters a QueryEntities call.
type QueryOptions struct {
	// PartitionKey, when non-empty, restricts results to a single partition.
	PartitionKey string
	// Filter is an OData $filter expression. The full comparison/logical
	// grammar (eq/ne/gt/ge/lt/le joined by and/or/not with parentheses over
	// string, numeric, boolean, datetime and guid literals) is supported.
	Filter string
	// Top caps the number of entities returned in this page (0 = no cap).
	Top int
	// Select is a raw comma-separated $select property-name list restricting
	// which properties each returned entity carries. Empty means no
	// projection: every stored property is returned. PartitionKey, RowKey,
	// Timestamp and odata.etag are always included regardless of Select,
	// matching the real Table service.
	Select string
	// NextPartitionKey/NextRowKey are the continuation position from a prior
	// page; results resume strictly after this (PartitionKey, RowKey).
	NextPartitionKey string
	NextRowKey       string
}

// QueryResult is a single page of a QueryEntities call. NextPartitionKey and
// NextRowKey are set when more entities remain; both empty means the last page.
type QueryResult struct {
	Entities         []Entity
	NextPartitionKey string
	NextRowKey       string
}

// BatchOpType is the operation an entity-group-transaction sub-request performs.
type BatchOpType int

const (
	// BatchInsert inserts a new entity; fails if it already exists.
	BatchInsert BatchOpType = iota
	// BatchUpsertReplace inserts or fully replaces an entity (no If-Match).
	BatchUpsertReplace
	// BatchUpsertMerge inserts or merges an entity (no If-Match).
	BatchUpsertMerge
	// BatchUpdateReplace replaces an existing entity; honors If-Match.
	BatchUpdateReplace
	// BatchUpdateMerge merges into an existing entity; honors If-Match.
	BatchUpdateMerge
	// BatchDelete deletes an existing entity; honors If-Match.
	BatchDelete
)

// BatchOp is one sub-operation of an entity group transaction. All ops in a
// transaction share a PartitionKey.
type BatchOp struct {
	Type         BatchOpType
	PartitionKey string
	RowKey       string
	Entity       Entity
	IfMatch      string
}

// BatchResult is the per-op outcome of a successful ApplyBatch, carrying the
// resulting ETag (empty for delete).
type BatchResult struct {
	ETag string
}

// ErrTableNotFound is returned by TableStorage methods when the referenced
// table itself does not exist. It is still a cerrors.NotFound error (so
// cerrors.IsNotFound(err) keeps classifying it correctly), but wire handlers
// can errors.Is(err, ErrTableNotFound) to tell it apart from a missing entity
// inside an existing table (also cerrors.NotFound) and report the real Table
// Storage "TableNotFound" error code instead of the generic "EntityNotFound".
var ErrTableNotFound = cerrors.New(cerrors.NotFound, "the table specified does not exist")

// TableStorage is the interface a Table Storage backend implements.
type TableStorage interface {
	CreateTable(ctx context.Context, name string) error
	DeleteTable(ctx context.Context, name string) error
	ListTables(ctx context.Context) ([]string, error)

	InsertEntity(ctx context.Context, table, partitionKey, rowKey string, entity Entity) (etag string, err error)
	GetEntity(ctx context.Context, table, partitionKey, rowKey string) (Entity, error)
	UpdateEntity(
		ctx context.Context, table, partitionKey, rowKey string, entity Entity, mode UpdateMode, ifMatch string,
	) (etag string, err error)
	DeleteEntity(ctx context.Context, table, partitionKey, rowKey, ifMatch string) error
	QueryEntities(ctx context.Context, table string, opts QueryOptions) (QueryResult, error)

	// ApplyBatch atomically applies an entity group transaction: either every
	// op succeeds or none is applied. On failure it returns a BatchError naming
	// the failed op's index.
	ApplyBatch(ctx context.Context, table string, ops []BatchOp) ([]BatchResult, error)
}

// BatchError reports which op in an entity group transaction failed. The Table
// service echoes the zero-based index at the front of the error message and
// rolls the whole change set back.
type BatchError struct {
	Index int
	Err   error
}

// Error implements error.
func (e *BatchError) Error() string {
	return e.Err.Error()
}

// Unwrap exposes the underlying cause so cerrors classification still works.
func (e *BatchError) Unwrap() error {
	return e.Err
}
