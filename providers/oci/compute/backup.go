package compute

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Backup types OCI offers. An incremental backup holds only the blocks that
// changed since the last one.
const (
	BackupFull        = "FULL"
	BackupIncremental = "INCREMENTAL"
)

// Portable snapshot states, as the driver's SnapshotInfo carries them.
const snapshotCompleted = "completed"

type backupData struct {
	ID          string
	VolumeID    string
	State       string
	Description string
	Size        int
	CreatedAt   string
	Tags        map[string]string
	Type        string
	// Boot marks a backup of a boot volume, which OCI keeps in its own
	// collection under /bootVolumeBackups.
	Boot bool
}

// CreateSnapshot creates a block volume backup.
func (m *Mock) CreateSnapshot(_ context.Context, cfg driver.SnapshotConfig) (*driver.SnapshotInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, err := m.addBackup(cfg.VolumeID, cfg.Description, BackupIncremental, cfg.Tags)
	if err != nil {
		return nil, err
	}

	info := toSnapshotInfo(b)

	return &info, nil
}

// CreateVolumeBackup creates a block or boot volume backup of the requested
// type, which the portable SnapshotConfig cannot express.
func (m *Mock) CreateVolumeBackup(
	_ context.Context, volumeID, displayName, backupType string, boot bool, tags map[string]string,
) (*driver.SnapshotInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if backupType == "" {
		backupType = BackupIncremental
	}

	if backupType != BackupFull && backupType != BackupIncremental {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported backup type %q", backupType)
	}

	b, err := m.addBootAwareBackup(volumeID, displayName, backupType, boot, tags)
	if err != nil {
		return nil, err
	}

	info := toSnapshotInfo(b)

	return &info, nil
}

// addBackup stores a block volume backup. The caller holds m.mu.
func (m *Mock) addBackup(volumeID, description, backupType string, tags map[string]string) (*backupData, error) {
	return m.addBootAwareBackup(volumeID, description, backupType, false, tags)
}

// addBootAwareBackup stores a backup of a block or a boot volume. The caller
// holds m.mu.
func (m *Mock) addBootAwareBackup(
	volumeID, description, backupType string, boot bool, tags map[string]string,
) (*backupData, error) {
	size := 0

	if boot {
		bv, ok := m.bootVolumes.Get(volumeID)
		if !ok {
			return nil, bootVolumeNotFound(volumeID)
		}

		size = bv.SizeInGBs
	} else {
		v, ok := m.volumes.Get(volumeID)
		if !ok {
			return nil, volumeNotFound(volumeID)
		}

		size = v.Size
	}

	id := m.newOCID(typeVolumeBackup)
	b := &backupData{
		ID:          id,
		VolumeID:    volumeID,
		State:       snapshotCompleted,
		Description: description,
		Size:        size,
		CreatedAt:   m.now(),
		Tags:        copyTags(tags),
		Type:        backupType,
		Boot:        boot,
	}

	m.backups.Set(id, b)
	m.record(id)

	return b, nil
}

// DeleteSnapshot deletes a volume backup.
func (m *Mock) DeleteSnapshot(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.backups.Has(id) {
		return backupNotFound(id)
	}

	m.backups.Delete(id)
	m.forget(id)

	return nil
}

// DescribeSnapshots returns volume backups matching the given OCIDs, or all if
// empty.
func (m *Mock) DescribeSnapshots(_ context.Context, ids []string) ([]driver.SnapshotInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.backups, ids, toSnapshotInfo), nil
}

// VolumeBackupDetails returns the OCI-only attributes of a backup: its type
// and whether it backs a boot volume.
func (m *Mock) VolumeBackupDetails(id string) (backupType string, boot, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	b, found := m.backups.Get(id)
	if !found {
		return "", false, false
	}

	return b.Type, b.Boot, true
}

// UpdateVolumeBackup changes a backup's display name and tags.
func (m *Mock) UpdateVolumeBackup(_ context.Context, id string, upd Update) (*driver.SnapshotInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.backups.Has(id) {
		return nil, backupNotFound(id)
	}

	m.backups.Update(id, func(b *backupData) *backupData {
		if upd.DisplayName != nil {
			b.Description = *upd.DisplayName
		}

		if upd.Tags != nil {
			b.Tags = mergeTags(b.Tags, upd.Tags)
		}

		return b
	})

	updated, _ := m.backups.Get(id)
	info := toSnapshotInfo(updated)

	return &info, nil
}

func toSnapshotInfo(b *backupData) driver.SnapshotInfo {
	return driver.SnapshotInfo{
		ID:          b.ID,
		VolumeID:    b.VolumeID,
		State:       b.State,
		Description: b.Description,
		Size:        b.Size,
		CreatedAt:   b.CreatedAt,
		Tags:        copyTags(b.Tags),
	}
}

func backupNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "volume backup %q not found", id)
}
