package driver

import "context"

// Global table status constants. A global table is synchronous in the emulator,
// so CreateGlobalTable lands ACTIVE immediately.
const (
	GlobalTableStatusActive = "ACTIVE"
)

// GlobalTableInfo describes a version-2017 global table: its name, ARN, status,
// creation time (Unix seconds, minted once) and the ordered list of replica
// region names in its replication group.
type GlobalTableInfo struct {
	Name        string
	Arn         string
	Status      string  // GlobalTableStatusActive
	CreatedUnix float64 // creation time (Unix seconds), minted once
	Regions     []string
}

// GlobalTabler is an OPTIONAL capability, discovered by type assertion (like
// Backuper): a provider that models version-2017 DynamoDB global tables
// implements it. Only the AWS DynamoDB mock does; Cosmos DB / Firestore don't,
// so it stays off the cross-cloud Database interface.
type GlobalTabler interface {
	// CreateGlobalTable creates a global table named after an existing table with
	// the given replica regions.
	CreateGlobalTable(ctx context.Context, name string, regions []string) (GlobalTableInfo, error)
	// DescribeGlobalTable returns the global table identified by name.
	DescribeGlobalTable(ctx context.Context, name string) (GlobalTableInfo, error)
	// UpdateGlobalTable adds and/or removes replica regions, returning the
	// updated description.
	UpdateGlobalTable(ctx context.Context, name string, addRegions, removeRegions []string) (GlobalTableInfo, error)
	// ListGlobalTables returns every global table ordered by name, optionally
	// filtered to those with a replica in regionFilter when it is non-empty.
	ListGlobalTables(ctx context.Context, regionFilter string) ([]GlobalTableInfo, error)
}
