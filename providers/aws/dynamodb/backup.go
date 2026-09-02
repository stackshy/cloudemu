package dynamodb

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// pitrRetentionDays is the 35-day continuous-backup retention window DynamoDB
// keeps, bounding a table's EarliestRestorableDateTime.
const pitrRetentionDays = 35

var _ driver.Backuper = (*Mock)(nil)

// backupData is one stored on-demand backup: its description plus a deep copy of
// the table's items captured at backup time, so the backup is immune to any
// later mutation or deletion of the source table.
type backupData struct {
	info  driver.BackupInfo
	items []map[string]any
}

// CreateBackup snapshots the table's schema and items under a fresh BackupArn.
func (m *Mock) CreateBackup(_ context.Context, table, backupName string) (driver.BackupInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return driver.BackupInfo{}, cerrors.Newf(cerrors.NotFound, "Table not found: %s", table)
	}

	items := make([]map[string]any, 0, td.items.Len())

	var size int64

	for _, it := range td.items.All() {
		items = append(items, deepCopyItem(it))
		size += int64(itemSizeBytes(it))
	}

	info := driver.BackupInfo{
		BackupArn:   td.config.TableArn + "/backup/" + m.newBackupID(),
		BackupName:  backupName,
		Status:      driver.BackupStatusAvailable,
		Type:        driver.BackupTypeUser,
		CreatedUnix: float64(m.opts.Clock.Now().Unix()),
		SizeBytes:   size,
		ItemCount:   int64(len(items)),
		SourceTable: td.config,
	}

	m.backups[info.BackupArn] = &backupData{info: info, items: items}

	return info, nil
}

// DescribeBackup returns the backup identified by backupArn.
func (m *Mock) DescribeBackup(_ context.Context, backupArn string) (driver.BackupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	b, ok := m.backups[backupArn]
	if !ok {
		return driver.BackupInfo{}, cerrors.Newf(cerrors.NotFound, "Backup not found: %s", backupArn)
	}

	return b.info, nil
}

// ListBackups returns every backup (ordered by BackupArn), or only those of
// tableName when it is non-empty.
func (m *Mock) ListBackups(_ context.Context, tableName string) ([]driver.BackupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]driver.BackupInfo, 0, len(m.backups))

	for _, b := range m.backups {
		if tableName != "" && b.info.SourceTable.Name != tableName {
			continue
		}

		out = append(out, b.info)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].BackupArn < out[j].BackupArn })

	return out, nil
}

// DeleteBackup removes the backup identified by backupArn, returning its last
// description.
func (m *Mock) DeleteBackup(_ context.Context, backupArn string) (driver.BackupInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.backups[backupArn]
	if !ok {
		return driver.BackupInfo{}, cerrors.Newf(cerrors.NotFound, "Backup not found: %s", backupArn)
	}

	delete(m.backups, backupArn)

	return b.info, nil
}

// RestoreTableFromBackup creates targetTable from the backup's schema and item
// snapshot.
func (m *Mock) RestoreTableFromBackup(ctx context.Context, backupArn, targetTable string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.backups[backupArn]
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Backup not found: %s", backupArn)
	}

	if _, exists := m.tables[targetTable]; exists {
		return cerrors.Newf(cerrors.AlreadyExists, "Table already exists: %s", targetTable)
	}

	m.restoreInto(ctx, targetTable, &b.info.SourceTable, b.items)

	return nil
}

// RestoreTableToPointInTime creates targetTable from sourceTable's CURRENT item
// set. The emulator retains no per-second history, so it restores the latest
// restorable point; useLatest is accepted for wire fidelity but does not change
// the restored data. It requires PITR (continuous backups) enabled on the
// source, matching real DynamoDB.
func (m *Mock) RestoreTableToPointInTime(ctx context.Context, sourceTable, targetTable string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.tables[sourceTable]
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Source table not found: %s", sourceTable)
	}

	if !src.pitrEnabled {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"Point in time recovery is not enabled for table: %s", sourceTable)
	}

	if _, exists := m.tables[targetTable]; exists {
		return cerrors.Newf(cerrors.AlreadyExists, "Table already exists: %s", targetTable)
	}

	items := make([]map[string]any, 0, src.items.Len())
	for _, it := range src.items.All() {
		items = append(items, it)
	}

	m.restoreInto(ctx, targetTable, &src.config, items)

	return nil
}

// PITRWindow reports the continuous-backup restorable window for a table: the
// latest restorable point is now, and the earliest is bounded by the table's
// creation time and the 35-day retention window. Backs the restorable-window
// fields of DescribeContinuousBackups.
func (m *Mock) PITRWindow(_ context.Context, table string) (earliest, latest float64, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return 0, 0, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	now := m.opts.Clock.Now()
	earliest = td.config.CreatedAtUnix

	if floor := float64(now.Add(-pitrRetentionDays * 24 * time.Hour).Unix()); earliest < floor {
		earliest = floor
	}

	return earliest, float64(now.Unix()), nil
}

// restoreInto materializes a new table named targetTable from a schema snapshot
// and a set of items, deep-copying each item so the new table shares no state
// with the backup or the source. The restored table is a NEW resource: it gets
// its own ARN, id and creation time, and starts with streams disabled (a
// restore does not carry continuous backups or streams forward). Caller holds
// m.mu.
func (m *Mock) restoreInto(ctx context.Context, targetTable string, srcCfg *driver.TableConfig, items []map[string]any) {
	cfg := *srcCfg
	cfg.Name = targetTable
	cfg.TableArn = idgen.AWSARN("dynamodb", regionctx.RegionOr(ctx, m.opts.Region), m.opts.AccountID, "table/"+targetTable)
	cfg.CreatedAtUnix = float64(m.opts.Clock.Now().Unix())
	cfg.TableID = uuidV4()
	cfg.StreamEnabled = false
	cfg.StreamViewType = ""
	cfg.StreamArn = ""
	cfg.StreamLabel = ""

	td := &tableData{items: memstore.New[map[string]any](), config: cfg}

	for _, it := range items {
		copied := deepCopyItem(it)
		td.items.Set(itemKey(cfg, copied), copied)
	}

	m.tables[targetTable] = td
}

// newBackupID builds the id segment of a BackupArn: a millisecond timestamp and
// a short random suffix, mirroring the shape a real DynamoDB backup id carries.
func (m *Mock) newBackupID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("dynamodb: crypto/rand failed: " + err.Error())
	}

	return fmt.Sprintf("%013d-%08x", m.opts.Clock.Now().UnixMilli(), b)
}

// deepCopyItem returns a fully independent copy of item, recursing through
// nested maps, lists, binary scalars and the set types so a backup or a
// restored table can never be mutated through an alias into the source.
func deepCopyItem(item map[string]any) map[string]any {
	out := make(map[string]any, len(item))
	for k, v := range item {
		out[k] = deepCopyValue(v)
	}

	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyItem(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = deepCopyValue(t[i])
		}

		return out
	case []byte:
		return append([]byte(nil), t...)
	case expr.StringSet:
		return append(expr.StringSet(nil), t...)
	case expr.NumberSet:
		return append(expr.NumberSet(nil), t...)
	case expr.BinarySet:
		out := make(expr.BinarySet, len(t))
		for i := range t {
			out[i] = append([]byte(nil), t[i]...)
		}

		return out
	default:
		return v
	}
}
