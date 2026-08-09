package efs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// nameTag is the tag key EFS mirrors onto FileSystem.Name.
const nameTag = "Name"

// CreateFileSystem creates a new file system. A repeated creation token returns
// the existing file system (idempotent create), matching EFS.
//
//nolint:gocritic // in is the public CreateFileSystem input, taken by value to match the driver API
func (m *Mock) CreateFileSystem(_ context.Context, in driver.CreateFileSystemInput) (*driver.FileSystem, error) {
	if in.CreationToken == "" {
		return nil, errors.New(errors.InvalidArgument, "CreationToken is required")
	}

	// Idempotency: an existing file system with the same creation token is
	// returned rather than duplicated.
	for _, fd := range m.fileSystems.All() {
		fd.mu.RLock()
		match := fd.fs.CreationToken == in.CreationToken
		fd.mu.RUnlock()

		if match {
			return nil, errors.Newf(errors.AlreadyExists,
				"file system with creation token %q already exists", in.CreationToken)
		}
	}

	perf := in.PerformanceMode
	if perf == "" {
		perf = driver.PerformanceGeneralPurpose
	}

	tput := in.ThroughputMode
	if tput == "" {
		tput = driver.ThroughputBursting
	}

	if tput == driver.ThroughputProvisioned && in.ProvisionedThroughputInMibps <= 0 {
		return nil, errors.New(errors.InvalidArgument,
			"ProvisionedThroughputInMibps is required when ThroughputMode is provisioned")
	}

	id := "fs-" + idgen.GenerateID("")
	now := m.opts.Clock.Now().UTC()

	name := in.Tags[nameTag]

	fs := driver.FileSystem{
		OwnerID:                      m.opts.AccountID,
		CreationToken:                in.CreationToken,
		FileSystemID:                 id,
		ARN:                          m.fsARN(id),
		CreationTime:                 now,
		LifeCycleState:               driver.StateAvailable,
		Name:                         name,
		NumberOfMountTargets:         0,
		SizeInBytes:                  driver.FileSystemSize{Timestamp: now},
		PerformanceMode:              perf,
		Encrypted:                    in.Encrypted,
		KMSKeyID:                     in.KMSKeyID,
		ThroughputMode:               tput,
		ProvisionedThroughputInMibps: in.ProvisionedThroughputInMibps,
		AvailabilityZoneName:         in.AvailabilityZoneName,
		Tags:                         copyTags(in.Tags),
		Protection:                   driver.FileSystemProtection{ReplicationOverwriteProtection: "ENABLED"},
	}

	m.fileSystems.Set(id, &fsData{
		fs:        fs,
		mountTgts: map[string]*driver.MountTarget{},
		accessPts: map[string]*driver.AccessPoint{},
		backup:    driver.BackupDisabled,
	})

	out := fs

	return &out, nil
}

// DeleteFileSystem deletes a file system. It must have no mount targets.
func (m *Mock) DeleteFileSystem(_ context.Context, fileSystemID string) error {
	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return errors.Newf(errors.NotFound, "file system %q not found", fileSystemID)
	}

	fd.mu.RLock()
	n := len(fd.mountTgts)
	fd.mu.RUnlock()

	if n > 0 {
		return errors.Newf(errors.FailedPrecondition,
			"file system %q has %d mount target(s); delete them first", fileSystemID, n)
	}

	m.fileSystems.Delete(fileSystemID)

	return nil
}

// DescribeFileSystems returns file systems, optionally filtered by id or
// creation token.
func (m *Mock) DescribeFileSystems(
	_ context.Context, fileSystemID, creationToken string,
) ([]driver.FileSystem, error) {
	if fileSystemID != "" {
		fd, ok := m.getFS(fileSystemID)
		if !ok {
			return nil, errors.Newf(errors.NotFound, "file system %q not found", fileSystemID)
		}

		fd.mu.RLock()
		defer fd.mu.RUnlock()

		return []driver.FileSystem{fd.fs}, nil
	}

	all := m.fileSystems.All()
	out := make([]driver.FileSystem, 0, len(all))

	for _, fd := range all {
		fd.mu.RLock()
		if creationToken == "" || fd.fs.CreationToken == creationToken {
			out = append(out, fd.fs)
		}
		fd.mu.RUnlock()
	}

	return out, nil
}

// UpdateFileSystem changes throughput settings.
func (m *Mock) UpdateFileSystem(_ context.Context, in driver.UpdateFileSystemInput) (*driver.FileSystem, error) {
	fd, ok := m.getFS(in.FileSystemID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "file system %q not found", in.FileSystemID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	if in.ThroughputMode != "" {
		fd.fs.ThroughputMode = in.ThroughputMode
	}

	if in.ThroughputMode == driver.ThroughputProvisioned || fd.fs.ThroughputMode == driver.ThroughputProvisioned {
		if in.ProvisionedThroughputInMibps > 0 {
			fd.fs.ProvisionedThroughputInMibps = in.ProvisionedThroughputInMibps
		}
	}

	if in.ThroughputMode != "" && in.ThroughputMode != driver.ThroughputProvisioned {
		fd.fs.ProvisionedThroughputInMibps = 0
	}

	out := fd.fs

	return &out, nil
}

// PutFileSystemPolicy sets the resource policy.
func (m *Mock) PutFileSystemPolicy(
	_ context.Context, fileSystemID, policy string, _ bool,
) (string, error) {
	if policy == "" {
		return "", errors.New(errors.InvalidArgument, "Policy is required")
	}

	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return "", errors.Newf(errors.NotFound, "file system %q not found", fileSystemID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	fd.policy = policy

	return policy, nil
}

// DescribeFileSystemPolicy returns the resource policy, erroring when unset.
func (m *Mock) DescribeFileSystemPolicy(_ context.Context, fileSystemID string) (string, error) {
	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return "", errors.Newf(errors.NotFound, "file system %q not found", fileSystemID)
	}

	fd.mu.RLock()
	defer fd.mu.RUnlock()

	if fd.policy == "" {
		return "", errors.Newf(errors.NotFound, "no policy set for file system %q", fileSystemID)
	}

	return fd.policy, nil
}

// DeleteFileSystemPolicy clears the resource policy.
func (m *Mock) DeleteFileSystemPolicy(_ context.Context, fileSystemID string) error {
	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return errors.Newf(errors.NotFound, "file system %q not found", fileSystemID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	fd.policy = ""

	return nil
}

// resolveTagTarget maps a resource id (file system or access point) to its
// tag map, so tagging works uniformly across resource types.
func (m *Mock) resolveTagTarget(resourceID string) (*fsData, func() map[string]string, error) {
	if fd, ok := m.getFS(resourceID); ok {
		return fd, func() map[string]string { return fd.fs.Tags }, nil
	}

	if fsID, ok := m.apIndex.Get(resourceID); ok {
		if fd, ok := m.getFS(fsID); ok {
			return fd, func() map[string]string {
				if ap := fd.accessPts[resourceID]; ap != nil {
					return ap.Tags
				}

				return nil
			}, nil
		}
	}

	return nil, nil, errors.Newf(errors.NotFound, "resource %q not found", resourceID)
}

// TagResource adds or overwrites tags on a file system or access point.
func (m *Mock) TagResource(_ context.Context, resourceID string, tags map[string]string) error {
	fd, getTags, err := m.resolveTagTarget(resourceID)
	if err != nil {
		return err
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	target := getTags()

	for k, v := range tags {
		target[k] = v

		if fd.fs.FileSystemID == resourceID && k == nameTag {
			fd.fs.Name = v
		}
	}

	return nil
}

// UntagResource removes tags from a file system or access point.
func (m *Mock) UntagResource(_ context.Context, resourceID string, tagKeys []string) error {
	fd, getTags, err := m.resolveTagTarget(resourceID)
	if err != nil {
		return err
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	target := getTags()

	for _, k := range tagKeys {
		delete(target, k)

		if fd.fs.FileSystemID == resourceID && k == nameTag {
			fd.fs.Name = ""
		}
	}

	return nil
}

// ListTagsForResource returns a copy of a resource's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceID string) (map[string]string, error) {
	fd, getTags, err := m.resolveTagTarget(resourceID)
	if err != nil {
		return nil, err
	}

	fd.mu.RLock()
	defer fd.mu.RUnlock()

	return copyTags(getTags()), nil
}
