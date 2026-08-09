package driver

import "context"

// Transition-to-IA / transition-to-primary values for lifecycle policies.
const (
	TransitionToIAAfter7Days   = "AFTER_7_DAYS"
	TransitionToIAAfter14Days  = "AFTER_14_DAYS"
	TransitionToIAAfter30Days  = "AFTER_30_DAYS"
	TransitionToIAAfter60Days  = "AFTER_60_DAYS"
	TransitionToIAAfter90Days  = "AFTER_90_DAYS"
	TransitionToIAAfter1Day    = "AFTER_1_DAY"
	TransitionToPrimaryOnFirst = "AFTER_1_ACCESS"
)

// Backup policy statuses.
const (
	BackupEnabled  = "ENABLED"
	BackupDisabled = "DISABLED"
)

// LifecyclePolicy is one rule in a file system's lifecycle configuration.
type LifecyclePolicy struct {
	TransitionToIA                  string
	TransitionToPrimaryStorageClass string
	TransitionToArchive             string
}

// Destination is a replication destination.
type Destination struct {
	Status               string
	FileSystemID         string
	Region               string
	AvailabilityZoneName string
	KMSKeyID             string
	RoleARN              string
	LastReplicatedTime   string
	OwnerID              string
}

// DestinationToCreate is a requested replication destination.
type DestinationToCreate struct {
	Region               string
	AvailabilityZoneName string
	KMSKeyID             string
	FileSystemID         string
	RoleARN              string
}

// ReplicationConfiguration is a file system's replication configuration.
type ReplicationConfiguration struct {
	SourceFileSystemID          string
	SourceFileSystemARN         string
	SourceFileSystemRegion      string
	OriginalSourceFileSystemARN string
	CreationTime                string
	Destinations                []Destination
	SourceFileSystemOwnerID     string
}

// Config is the lifecycle/backup/replication/account-preference surface of EFS.
type Config interface {
	PutLifecycleConfiguration(ctx context.Context, fileSystemID string, policies []LifecyclePolicy) ([]LifecyclePolicy, error)
	DescribeLifecycleConfiguration(ctx context.Context, fileSystemID string) ([]LifecyclePolicy, error)

	PutBackupPolicy(ctx context.Context, fileSystemID, status string) (string, error)
	DescribeBackupPolicy(ctx context.Context, fileSystemID string) (string, error)

	CreateReplicationConfiguration(
		ctx context.Context, sourceFileSystemID string, dests []DestinationToCreate,
	) (*ReplicationConfiguration, error)
	DeleteReplicationConfiguration(ctx context.Context, sourceFileSystemID string) error
	DescribeReplicationConfigurations(ctx context.Context, fileSystemID string) ([]ReplicationConfiguration, error)

	PutAccountPreferences(ctx context.Context, resourceIDType string) (string, error)
	DescribeAccountPreferences(ctx context.Context) (string, error)
}
