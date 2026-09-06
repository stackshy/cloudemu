// Package driver defines the portable interface for the Google Cloud Spanner
// admin control plane (spanner.googleapis.com/v1). It is control-plane only —
// instances and databases, plus the long-running operations their mutating RPCs
// return — so it is independent of the other database drivers. The SQL data
// plane (sessions/read/executeSql/commit), backups, custom instance configs,
// IAM, and database roles are deliberately out of scope.
//
// Spanner is a global service: instances live directly under a project
// (projects/{p}/instances/{i}), not under a location. Full resource names follow
// the GCP convention:
//
//	projects/{p}/instances/{i}
//	projects/{p}/instances/{i}/databases/{d}
//	projects/{p}/instances/{i}/operations/{op}
package driver

import (
	"context"
	"time"
)

// Resource lifecycle states. CloudEmu completes every mutation synchronously, so
// a created instance or database reports READY immediately (real Spanner passes
// briefly through CREATING); the constant is retained for parity and tests.
const (
	StateReady    = "READY"
	StateCreating = "CREATING"
)

// DialectGoogleStandardSQL is the default database dialect when a create request
// leaves databaseDialect unset, matching real Spanner.
const DialectGoogleStandardSQL = "GOOGLE_STANDARD_SQL"

// ProcessingUnitsPerNode is the fixed Spanner ratio: one node equals 1000
// processing units. Whichever of nodeCount / processingUnits a create or update
// sets, the other is derived from it through this ratio.
const ProcessingUnitsPerNode = 1000

// Instance is a Spanner instance.
type Instance struct {
	Name            string // projects/{p}/instances/{i}
	Config          string // projects/{p}/instanceConfigs/{c}
	DisplayName     string
	NodeCount       int64
	ProcessingUnits int64
	State           string
	Labels          map[string]string
	CreateTime      time.Time
	UpdateTime      time.Time
}

// CreateInstanceConfig is the input to CreateInstance. Exactly one of NodeCount
// or ProcessingUnits is expected to be set; the implementation derives the other.
type CreateInstanceConfig struct {
	Name            string // full resource name projects/{p}/instances/{i}
	Config          string
	DisplayName     string
	NodeCount       int64
	ProcessingUnits int64
	Labels          map[string]string
}

// UpdateInstanceConfig is the input to UpdateInstance. FieldMask holds the
// normalized (lowercased, snake/camel-insensitive) field tokens from the
// request's fieldMask; when empty the update applies presence heuristics.
type UpdateInstanceConfig struct {
	DisplayName     string
	NodeCount       int64
	ProcessingUnits int64
	Labels          map[string]string
	FieldMask       []string
}

// Database is a Spanner database within an instance.
type Database struct {
	Name                   string // projects/{p}/instances/{i}/databases/{d}
	State                  string
	DatabaseDialect        string
	VersionRetentionPeriod string
	EnableDropProtection   bool
	DDL                    []string // accumulated DDL statements
	CreateTime             time.Time
}

// CreateDatabaseConfig is the input to CreateDatabase.
type CreateDatabaseConfig struct {
	Parent          string // projects/{p}/instances/{i}
	Name            string // full resource name projects/{p}/instances/{i}/databases/{d}
	DatabaseDialect string
	ExtraStatements []string
}

// Operation is a completed long-running operation. Every CloudEmu mutation
// finishes synchronously, so Done is always true; the poller replays it so an
// SDK or Terraform LRO wait terminates on the first poll.
type Operation struct {
	Name       string // projects/{p}/instances/{i}[/databases/{d}]/operations/{op}
	Done       bool
	TargetName string // the resource the operation acted on
	Type       string // create-instance | update-instance | create-database | update-ddl
}

// Spanner is the control-plane interface a provider implements.
type Spanner interface {
	// CreateInstance provisions a new instance (returns a completed LRO).
	CreateInstance(ctx context.Context, cfg CreateInstanceConfig) (*Instance, *Operation, error)
	// GetInstance returns an instance by full resource name.
	GetInstance(ctx context.Context, name string) (*Instance, error)
	// ListInstances returns every instance in a project.
	ListInstances(ctx context.Context, project string) ([]Instance, error)
	// UpdateInstance applies a masked update to an instance (returns a completed LRO).
	UpdateInstance(ctx context.Context, name string, cfg UpdateInstanceConfig) (*Instance, *Operation, error)
	// DeleteInstance removes an instance and its databases (synchronous).
	DeleteInstance(ctx context.Context, name string) error

	// CreateDatabase provisions a database under an instance (returns a completed LRO).
	CreateDatabase(ctx context.Context, cfg CreateDatabaseConfig) (*Database, *Operation, error)
	// GetDatabase returns a database by full resource name.
	GetDatabase(ctx context.Context, name string) (*Database, error)
	// ListDatabases returns every database under an instance (parent).
	ListDatabases(ctx context.Context, parent string) ([]Database, error)
	// UpdateDatabaseDdl appends DDL statements to a database (returns a completed LRO).
	UpdateDatabaseDdl(ctx context.Context, name string, statements []string) (*Database, *Operation, error)
	// GetDatabaseDdl returns a database's accumulated DDL statements.
	GetDatabaseDdl(ctx context.Context, name string) ([]string, error)
	// DropDatabase deletes a database (synchronous).
	DropDatabase(ctx context.Context, name string) error

	// GetOperation resolves a (done) long-running operation by name.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
