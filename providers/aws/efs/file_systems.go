package efs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// nameTag is the tag key EFS mirrors onto FileSystem.Name.
const nameTag = "Name"

// CreateFileSystem creates a new file system. A repeated creation token is
// rejected with AlreadyExists (matching real EFS, which returns the existing
// file system's ARN via a FileSystemAlreadyExists error). The token is claimed
// atomically via a token→id index so concurrent same-token calls can't both
// create.
//
//nolint:gocritic // in is the public CreateFileSystem input, taken by value to match the driver API
func (m *Mock) CreateFileSystem(_ context.Context, in driver.CreateFileSystemInput) (*driver.FileSystem, error) {
	if in.CreationToken == "" {
		return nil, errors.New(errors.InvalidArgument, "CreationToken is required")
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

	// Atomically claim the creation token. If another call already owns it, this
	// is a duplicate — reject without creating, echoing the existing file
	// system's id so an idempotent retry can recover it (real EFS behavior).
	if !m.tokenIndex.SetIfAbsent(in.CreationToken, id) {
		existingID, _ := m.tokenIndex.Get(in.CreationToken)

		return nil, conflictWithID(driver.KindFileSystem, existingID,
			"file system with creation token %q already exists", in.CreationToken)
	}

	now := m.opts.Clock.Now().UTC()
	name := in.Tags[nameTag]

	// An encrypted file system with no explicit key uses the account's
	// AWS-managed aws/elasticfilesystem CMK.
	kmsKeyID := in.KMSKeyID
	if in.Encrypted && kmsKeyID == "" {
		kmsKeyID = m.defaultKMSKeyARN()
	}

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
		KMSKeyID:                     kmsKeyID,
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

// DeleteFileSystem deletes a file system. It must have no mount targets or
// access points. The check and delete run under the file system's write lock so
// a concurrent CreateMountTarget can't attach to a file system mid-delete, and
// all index entries (token, access points) are released.
func (m *Mock) DeleteFileSystem(_ context.Context, fileSystemID string) error {
	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	if n := len(fd.mountTgts); n > 0 {
		return inUse(driver.KindFileSystem,
			"file system %q has %d mount target(s); delete them first", fileSystemID, n)
	}

	if n := len(fd.accessPts); n > 0 {
		return inUse(driver.KindFileSystem,
			"file system %q has %d access point(s); delete them first", fileSystemID, n)
	}

	m.fileSystems.Delete(fileSystemID)
	m.tokenIndex.Delete(fd.fs.CreationToken)

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
			return nil, notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
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
		return nil, notFound(driver.KindFileSystem, "file system %q not found", in.FileSystemID)
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
		return "", notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
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
		return "", notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
	}

	fd.mu.RLock()
	defer fd.mu.RUnlock()

	if fd.policy == "" {
		return "", notFound(driver.KindPolicy, "no policy set for file system %q", fileSystemID)
	}

	return fd.policy, nil
}

// DeleteFileSystemPolicy clears the resource policy.
func (m *Mock) DeleteFileSystemPolicy(_ context.Context, fileSystemID string) error {
	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
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

	return nil, nil, notFound(driver.KindFileSystem, "resource %q not found", resourceID)
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
