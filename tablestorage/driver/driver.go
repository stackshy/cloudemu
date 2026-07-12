// Package driver defines the interface for Azure Table Storage-style
// key/value entity stores. Entities live inside named tables and are addressed
// by a (PartitionKey, RowKey) pair; each entity is a flat bag of named
// properties.
//
// The interface is intentionally small — it mirrors the operations the Azure
// Table data-plane REST API (aztables) exercises, nothing more.
package driver

import "context"

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
// "PartitionKey" and "RowKey") so callers get them back verbatim.
type Entity map[string]any

// QueryOptions filters a QueryEntities call.
type QueryOptions struct {
	// PartitionKey, when non-empty, restricts results to a single partition.
	PartitionKey string
	// Filter is an OData $filter expression. Only a small, common subset is
	// supported (PartitionKey/RowKey/property eq comparisons joined by "and");
	// an unsupported filter is ignored rather than rejected.
	Filter string
}

// TableStorage is the interface a Table Storage backend implements.
type TableStorage interface {
	CreateTable(ctx context.Context, name string) error
	DeleteTable(ctx context.Context, name string) error
	ListTables(ctx context.Context) ([]string, error)

	InsertEntity(ctx context.Context, table, partitionKey, rowKey string, entity Entity) error
	GetEntity(ctx context.Context, table, partitionKey, rowKey string) (Entity, error)
	UpdateEntity(ctx context.Context, table, partitionKey, rowKey string, entity Entity, mode UpdateMode) error
	DeleteEntity(ctx context.Context, table, partitionKey, rowKey string) error
	QueryEntities(ctx context.Context, table string, opts QueryOptions) ([]Entity, error)
}
