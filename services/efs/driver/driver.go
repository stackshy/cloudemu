// Package driver defines the interface and types for AWS EFS (Elastic File
// System) implementations. It models file systems, mount targets, access
// points, and their configuration (lifecycle, backup, policy, replication).
package driver

import (
	"context"
	"time"
)

// Performance modes.
const (
	PerformanceGeneralPurpose = "generalPurpose"
	PerformanceMaxIO          = "maxIO"
)

// Throughput modes.
const (
	ThroughputBursting    = "bursting"
	ThroughputProvisioned = "provisioned"
	ThroughputElastic     = "elastic"
)

// Lifecycle states.
const (
	StateCreating  = "creating"
	StateAvailable = "available"
	StateUpdating  = "updating"
	StateDeleting  = "deleting"
	StateDeleted   = "deleted"
	StateError     = "error"
)

// FileSystemSize is the size breakdown of a file system.
type FileSystemSize struct {
	Value           int64
	Timestamp       time.Time
	ValueInIA       int64
	ValueInStandard int64
}

// FileSystemProtection is the replication-overwrite protection setting.
type FileSystemProtection struct {
	ReplicationOverwriteProtection string // ENABLED | DISABLED | REPLICATING
}

// FileSystem is the description of an EFS file system.
type FileSystem struct {
	OwnerID                      string
	CreationToken                string
	FileSystemID                 string
	ARN                          string
	CreationTime                 time.Time
	LifeCycleState               string
	Name                         string
	NumberOfMountTargets         int32
	SizeInBytes                  FileSystemSize
	PerformanceMode              string
	Encrypted                    bool
	KMSKeyID                     string
	ThroughputMode               string
	ProvisionedThroughputInMibps float64
	AvailabilityZoneName         string
	AvailabilityZoneID           string
	Tags                         map[string]string
	Protection                   FileSystemProtection
}

// CreateFileSystemInput describes a file system to create.
type CreateFileSystemInput struct {
	CreationToken                string
	PerformanceMode              string
	Encrypted                    bool
	KMSKeyID                     string
	ThroughputMode               string
	ProvisionedThroughputInMibps float64
	AvailabilityZoneName         string
	Backup                       bool
	Tags                         map[string]string
}

// UpdateFileSystemInput describes changes to a file system.
type UpdateFileSystemInput struct {
	FileSystemID                 string
	ThroughputMode               string
	ProvisionedThroughputInMibps float64
}

// EFS is the interface an EFS backend implements. It grows across files:
// file systems + policy + tags here; mount targets, access points, and the
// configuration surface (lifecycle/backup/replication/preferences) are added
// by embedded sub-interfaces in later files of this package.
type EFS interface {
	MountTargets
	AccessPoints

	// File systems.
	CreateFileSystem(ctx context.Context, in CreateFileSystemInput) (*FileSystem, error)
	DeleteFileSystem(ctx context.Context, fileSystemID string) error
	DescribeFileSystems(ctx context.Context, fileSystemID, creationToken string) ([]FileSystem, error)
	UpdateFileSystem(ctx context.Context, in UpdateFileSystemInput) (*FileSystem, error)

	// File system policy.
	PutFileSystemPolicy(ctx context.Context, fileSystemID, policy string, bypassCheck bool) (string, error)
	DescribeFileSystemPolicy(ctx context.Context, fileSystemID string) (string, error)
	DeleteFileSystemPolicy(ctx context.Context, fileSystemID string) error

	// Tags (current + legacy APIs share this backing).
	TagResource(ctx context.Context, resourceID string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceID string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceID string) (map[string]string, error)
}
