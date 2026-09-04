package driver

import "context"

// Backup status/type constants a DynamoDB on-demand backup carries. A backup is
// synchronous in the emulator, so it lands AVAILABLE immediately, and every
// backup this surface creates is a user-initiated (USER) backup.
const (
	BackupStatusAvailable = "AVAILABLE"
	BackupTypeUser        = "USER"
)

// BackupInfo describes a single on-demand table backup. SourceTable is the
// schema snapshot of the table at backup time (name, keys, attributes, billing
// mode, throughput, id and ARN), which a DescribeBackup/ListBackups response
// renders as SourceTableDetails and a RestoreTableFromBackup replays to build
// the new table. The item data itself is held by the provider, keyed by
// BackupArn, and never leaves it.
type BackupInfo struct {
	BackupArn   string
	BackupName  string
	Status      string  // BackupStatusAvailable
	Type        string  // BackupTypeUser
	CreatedUnix float64 // backup creation time (Unix seconds)
	SizeBytes   int64   // approximate on-the-wire size of the backed-up items
	ItemCount   int64   // number of items captured
	SourceTable TableConfig
}

// Backuper is an OPTIONAL capability, discovered by type assertion (like the
// TableAttributes capability): a provider whose tables support DynamoDB-style
// on-demand backups and point-in-time recovery restore implements it. Only the
// AWS DynamoDB mock does; Cosmos DB / Firestore don't and contribute nothing,
// so the capability stays off the cross-cloud Database interface.
type Backuper interface {
	// CreateBackup snapshots the table's schema and items under a new BackupArn.
	CreateBackup(ctx context.Context, table, backupName string) (BackupInfo, error)
	// DescribeBackup returns the backup identified by backupArn.
	DescribeBackup(ctx context.Context, backupArn string) (BackupInfo, error)
	// ListBackups returns every backup, or only those of tableName when it is
	// non-empty, ordered by BackupArn.
	ListBackups(ctx context.Context, tableName string) ([]BackupInfo, error)
	// DeleteBackup removes the backup identified by backupArn, returning its
	// last description.
	DeleteBackup(ctx context.Context, backupArn string) (BackupInfo, error)
	// RestoreTableFromBackup creates targetTable from the backup's schema and
	// item snapshot.
	RestoreTableFromBackup(ctx context.Context, backupArn, targetTable string) error
	// RestoreTableToPointInTime creates targetTable from sourceTable. The
	// emulator retains no per-second history, so it restores the source table's
	// CURRENT item set (the latest restorable point); useLatest is accepted for
	// wire fidelity but does not change the restored data.
	RestoreTableToPointInTime(ctx context.Context, sourceTable, targetTable string, useLatest bool) error
}
