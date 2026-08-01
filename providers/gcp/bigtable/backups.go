package bigtable

import (
	"context"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

func cloneBackup(in *btdriver.Backup) btdriver.Backup {
	b := *in
	b.ColumnFamilies = cloneColumnFamilies(in.ColumnFamilies)

	return b
}

// CreateBackup creates a backup of a table under a cluster (LRO).
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateBackup(_ context.Context, cfg btdriver.CreateBackupConfig) (*btdriver.Backup, *btdriver.Operation, error) {
	if cfg.BackupID == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "backupId is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(cfg.Parent) {
		return nil, nil, cerrors.Newf(cerrors.InvalidArgument, "cluster %q not found", cfg.Parent)
	}

	// The source table must be a live table in the same instance as the backup
	// cluster (a backup can't reference another instance's table).
	instance := parentName(cfg.Parent)
	if parentName(cfg.SourceTable) != instance {
		return nil, nil, cerrors.Newf(cerrors.InvalidArgument, "source table %q is not in instance %q", cfg.SourceTable, instance)
	}

	src, ok := m.tables.Get(cfg.SourceTable)
	if !ok || src.Deleted {
		return nil, nil, cerrors.Newf(cerrors.InvalidArgument, "source table %q not found", cfg.SourceTable)
	}

	name := cfg.Parent + "/backups/" + cfg.BackupID
	if m.backups.Has(name) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "backup %q already exists", name)
	}

	now := m.opts.Clock.Now().UTC()
	b := btdriver.Backup{
		Name:           name,
		SourceTable:    cfg.SourceTable,
		ColumnFamilies: cloneColumnFamilies(src.ColumnFamilies),
		ExpireTime:     cfg.ExpireTime,
		StartTime:      now,
		EndTime:        now,
		SizeBytes:      0,
		State:          "READY",
		BackupType:     orDefault(cfg.BackupType, "STANDARD"),
	}
	m.backups.Set(name, b)

	op := m.newOp("create-backup", name)
	out := cloneBackup(&b)

	return &out, op, nil
}

// GetBackup returns a backup by full name.
func (m *Mock) GetBackup(_ context.Context, name string) (*btdriver.Backup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	b, ok := m.backups.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "backup %q not found", name)
	}

	out := cloneBackup(&b)

	return &out, nil
}

// ListBackups returns the backups of a cluster.
func (m *Mock) ListBackups(_ context.Context, cluster string) ([]btdriver.Backup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := cluster + "/backups/"
	all := m.backups.SortedValues()
	out := make([]btdriver.Backup, 0, len(all))

	for i := range all {
		if strings.HasPrefix(all[i].Name, prefix) {
			out = append(out, cloneBackup(&all[i]))
		}
	}

	return out, nil
}

// UpdateBackup changes a backup's expiry.
func (m *Mock) UpdateBackup(_ context.Context, name string, expireTime time.Time) (*btdriver.Backup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.backups.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "backup %q not found", name)
	}

	if !expireTime.IsZero() {
		b.ExpireTime = expireTime
	}

	m.backups.Set(name, b)

	out := cloneBackup(&b)

	return &out, nil
}

// DeleteBackup removes a backup.
func (m *Mock) DeleteBackup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.backups.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "backup %q not found", name)
	}

	m.backups.Delete(name)

	return nil
}

// CopyBackup copies a backup into a (possibly different) cluster (LRO).
func (m *Mock) CopyBackup(_ context.Context, cfg btdriver.CopyBackupConfig) (*btdriver.Backup, *btdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(cfg.Parent) {
		return nil, nil, cerrors.Newf(cerrors.InvalidArgument, "cluster %q not found", cfg.Parent)
	}

	src, ok := m.backups.Get(cfg.SourceBackup)
	if !ok {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "source backup %q not found", cfg.SourceBackup)
	}

	name := cfg.Parent + "/backups/" + cfg.BackupID
	if m.backups.Has(name) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "backup %q already exists", name)
	}

	now := m.opts.Clock.Now().UTC()
	b := btdriver.Backup{
		Name:           name,
		SourceTable:    src.SourceTable,
		SourceBackup:   cfg.SourceBackup,
		ColumnFamilies: cloneColumnFamilies(src.ColumnFamilies),
		ExpireTime:     cfg.ExpireTime,
		StartTime:      now,
		EndTime:        now,
		SizeBytes:      src.SizeBytes,
		State:          "READY",
		BackupType:     src.BackupType,
	}
	m.backups.Set(name, b)

	op := m.newOp("copy-backup", name)
	out := cloneBackup(&b)

	return &out, op, nil
}
