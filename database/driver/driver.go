// Package driver defines the interface for database service implementations.
package driver

import "context"

// TableConfig describes a table to create.
type TableConfig struct {
	Name         string
	PartitionKey string
	SortKey      string
	GSIs         []GSIConfig
}

// GSIConfig describes a Global Secondary Index.
type GSIConfig struct {
	Name         string
	PartitionKey string
	SortKey      string
}

// KeyCondition defines a key condition for queries.
type KeyCondition struct {
	PartitionKey string
	PartitionVal any
	SortOp       string // "=", "<", ">", "<=", ">=", "BETWEEN", "BEGINS_WITH"
	SortVal      any
	SortValEnd   any // for BETWEEN
}

// ScanFilter defines a scan filter.
type ScanFilter struct {
	Field string
	Op    string // "=", "!=", "<", ">", "<=", ">=", "CONTAINS", "BEGINS_WITH"
	Value any
}

// QueryInput configures a query operation.
type QueryInput struct {
	Table        string
	IndexName    string
	KeyCondition KeyCondition
	Limit        int
	PageToken    string
	ScanForward  bool
}

// ScanInput configures a scan operation.
type ScanInput struct {
	Table     string
	Filters   []ScanFilter
	Limit     int
	PageToken string
}

// QueryResult is the result of a query or scan.
type QueryResult struct {
	Items         []map[string]any
	Count         int
	NextPageToken string
}

// Database is the interface that database provider implementations must satisfy.
type Database interface {
	CreateTable(ctx context.Context, config TableConfig) error
	DeleteTable(ctx context.Context, name string) error
	DescribeTable(ctx context.Context, name string) (*TableConfig, error)
	ListTables(ctx context.Context) ([]string, error)

	PutItem(ctx context.Context, table string, item map[string]any) error
	GetItem(ctx context.Context, table string, key map[string]any) (map[string]any, error)
	DeleteItem(ctx context.Context, table string, key map[string]any) error
	Query(ctx context.Context, input QueryInput) (*QueryResult, error)
	Scan(ctx context.Context, input ScanInput) (*QueryResult, error)

	BatchPutItems(ctx context.Context, table string, items []map[string]any) error
	BatchGetItems(ctx context.Context, table string, keys []map[string]any) ([]map[string]any, error)
}
