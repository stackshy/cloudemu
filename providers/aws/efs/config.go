package efs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// PutLifecycleConfiguration replaces a file system's lifecycle policies (an
// empty list clears them).
func (m *Mock) PutLifecycleConfiguration(
	_ context.Context, fileSystemID string, policies []driver.LifecyclePolicy,
) ([]driver.LifecyclePolicy, error) {
	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return nil, notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	fd.lifecycle = append([]driver.LifecyclePolicy(nil), policies...)

	return append([]driver.LifecyclePolicy(nil), fd.lifecycle...), nil
}

// DescribeLifecycleConfiguration returns a file system's lifecycle policies.
func (m *Mock) DescribeLifecycleConfiguration(
	_ context.Context, fileSystemID string,
) ([]driver.LifecyclePolicy, error) {
	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return nil, notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
	}

	fd.mu.RLock()
	defer fd.mu.RUnlock()

	return append([]driver.LifecyclePolicy(nil), fd.lifecycle...), nil
}

// PutBackupPolicy sets a file system's backup policy status.
func (m *Mock) PutBackupPolicy(_ context.Context, fileSystemID, status string) (string, error) {
	if status != driver.BackupEnabled && status != driver.BackupDisabled {
		return "", errors.Newf(errors.InvalidArgument, "backup status must be ENABLED or DISABLED, got %q", status)
	}

	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return "", notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	fd.backup = status

	return status, nil
}

// DescribeBackupPolicy returns a file system's backup policy status.
func (m *Mock) DescribeBackupPolicy(_ context.Context, fileSystemID string) (string, error) {
	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return "", notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
	}

	fd.mu.RLock()
	defer fd.mu.RUnlock()

	if fd.backup == "" {
		return driver.BackupDisabled, nil
	}

	return fd.backup, nil
}

// CreateReplicationConfiguration sets up replication from a source file system
// to one or more destinations, creating a destination file system id for each.
func (m *Mock) CreateReplicationConfiguration(
	_ context.Context, sourceFileSystemID string, dests []driver.DestinationToCreate,
) (*driver.ReplicationConfiguration, error) {
	if len(dests) == 0 {
		return nil, errors.New(errors.InvalidArgument, "at least one destination is required")
	}

	fd, ok := m.getFS(sourceFileSystemID)
	if !ok {
		return nil, notFound(driver.KindFileSystem, "file system %q not found", sourceFileSystemID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	if fd.replication != nil {
		return nil, conflict(driver.KindReplication,
			"file system %q already has a replication configuration", sourceFileSystemID)
	}

	now := m.opts.Clock.Now().UTC()

	rc := &driver.ReplicationConfiguration{
		SourceFileSystemID:          fd.fs.FileSystemID,
		SourceFileSystemARN:         fd.fs.ARN,
		SourceFileSystemRegion:      m.opts.Region,
		OriginalSourceFileSystemARN: fd.fs.ARN,
		CreationTime:                now,
		SourceFileSystemOwnerID:     m.opts.AccountID,
	}

	for i := range dests {
		region := dests[i].Region
		if region == "" {
			region = m.opts.Region
		}

		destFSID := dests[i].FileSystemID
		if destFSID == "" {
			destFSID = "fs-" + idgen.GenerateID("")
		}

		rc.Destinations = append(rc.Destinations, driver.Destination{
			Status:               "ENABLED",
			FileSystemID:         destFSID,
			Region:               region,
			AvailabilityZoneName: dests[i].AvailabilityZoneName,
			KMSKeyID:             dests[i].KMSKeyID,
			RoleARN:              dests[i].RoleARN,
			LastReplicatedTime:   now,
			OwnerID:              m.opts.AccountID,
		})
	}

	fd.replication = rc

	out := *rc

	return &out, nil
}

// DeleteReplicationConfiguration removes a source file system's replication.
func (m *Mock) DeleteReplicationConfiguration(_ context.Context, sourceFileSystemID string) error {
	fd, ok := m.getFS(sourceFileSystemID)
	if !ok {
		return notFound(driver.KindFileSystem, "file system %q not found", sourceFileSystemID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	if fd.replication == nil {
		return notFound(driver.KindReplication, "no replication configuration for file system %q", sourceFileSystemID)
	}

	fd.replication = nil

	return nil
}

// DescribeReplicationConfigurations returns replication configs, optionally
// filtered to one source file system.
func (m *Mock) DescribeReplicationConfigurations(
	_ context.Context, fileSystemID string,
) ([]driver.ReplicationConfiguration, error) {
	if fileSystemID != "" {
		fd, ok := m.getFS(fileSystemID)
		if !ok {
			return nil, notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
		}

		fd.mu.RLock()
		defer fd.mu.RUnlock()

		if fd.replication == nil {
			return nil, notFound(driver.KindReplication,
				"no replication configuration for file system %q", fileSystemID)
		}

		return []driver.ReplicationConfiguration{*fd.replication}, nil
	}

	var out []driver.ReplicationConfiguration

	for _, fd := range m.fileSystems.All() {
		fd.mu.RLock()
		if fd.replication != nil {
			out = append(out, *fd.replication)
		}
		fd.mu.RUnlock()
	}

	return out, nil
}

// PutAccountPreferences sets the account-level resource-id preference.
func (m *Mock) PutAccountPreferences(_ context.Context, resourceIDType string) (string, error) {
	if resourceIDType == "" {
		return "", errors.New(errors.InvalidArgument, "ResourceIdType is required")
	}

	m.prefMu.Lock()
	defer m.prefMu.Unlock()

	m.accountPref = resourceIDType

	return resourceIDType, nil
}

// DescribeAccountPreferences returns the account-level resource-id preference.
func (m *Mock) DescribeAccountPreferences(_ context.Context) (string, error) {
	m.prefMu.RLock()
	defer m.prefMu.RUnlock()

	return m.accountPref, nil
}
