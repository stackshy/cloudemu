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
	PartitionVal interface{}
	SortOp       string // "=", "<", ">", "<=", ">=", "BETWEEN", "BEGINS_WITH"
	SortVal      interface{}
	SortValEnd   interface{} // for BETWEEN
}

// ScanFilter defines a scan filter.
type ScanFilter struct {
	Field string
	Op    string // "=", "!=", "<", ">", "<=", ">=", "CONTAINS", "BEGINS_WITH"
	Value interface{}
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
	Items         []map[string]interface{}
	Count         int
	NextPageToken string
}

// Database is the interface that database provider implementations must satisfy.
type Database interface {
	CreateTable(ctx context.Context, config TableConfig) error
	DeleteTable(ctx context.Context, name string) error
	DescribeTable(ctx context.Context, name string) (*TableConfig, error)
	ListTables(ctx context.Context) ([]string, error)

	PutItem(ctx context.Context, table string, item map[string]interface{}) error
	GetItem(ctx context.Context, table string, key map[string]interface{}) (map[string]interface{}, error)
	DeleteItem(ctx context.Context, table string, key map[string]interface{}) error
	Query(ctx context.Context, input QueryInput) (*QueryResult, error)
	Scan(ctx context.Context, input ScanInput) (*QueryResult, error)

	BatchPutItems(ctx context.Context, table string, items []map[string]interface{}) error
	BatchGetItems(ctx context.Context, table string, keys []map[string]interface{}) ([]map[string]interface{}, error)
}
