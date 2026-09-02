package dynamodb

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// seedItem writes one item into a table, failing the test on error.
func seedItem(t *testing.T, m *Mock, table string, item map[string]any) {
	t.Helper()
	requireNoError(t, m.PutItem(context.Background(), table, item))
}

func TestCreateBackupSnapshotsItems(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(m, "orders")
	seedItem(t, m, "orders", map[string]any{"pk": "a", "sk": "1", "v": "x"})
	seedItem(t, m, "orders", map[string]any{"pk": "b", "sk": "1"})

	info, err := m.CreateBackup(ctx, "orders", "nightly")
	requireNoError(t, err)
	assertNotEmpty(t, info.BackupArn)
	assertEqual(t, "nightly", info.BackupName)
	assertEqual(t, driver.BackupStatusAvailable, info.Status)
	assertEqual(t, driver.BackupTypeUser, info.Type)
	assertEqual(t, int64(2), info.ItemCount)

	// Mutating the source table after the backup must not change the backup.
	requireNoError(t, m.DeleteItem(ctx, "orders", map[string]any{"pk": "a", "sk": "1"}))

	got, err := m.DescribeBackup(ctx, info.BackupArn)
	requireNoError(t, err)
	assertEqual(t, int64(2), got.ItemCount)
}

func TestCreateBackupMissingTable(t *testing.T) {
	m := newTestMock()
	_, err := m.CreateBackup(context.Background(), "ghost", "b")
	assertError(t, err, true)
}

func TestRestoreTableFromBackupIsIndependent(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(m, "src")
	seedItem(t, m, "src", map[string]any{"pk": "a", "sk": "1", "v": "orig"})

	info, err := m.CreateBackup(ctx, "src", "b")
	requireNoError(t, err)

	requireNoError(t, m.RestoreTableFromBackup(ctx, info.BackupArn, "dst"))

	restored, err := m.GetItem(ctx, "dst", map[string]any{"pk": "a", "sk": "1"})
	requireNoError(t, err)
	assertEqual(t, "orig", restored["v"])

	// The restored table is a new resource with its own ARN.
	srcCfg, err := m.DescribeTable(ctx, "src")
	requireNoError(t, err)
	dstCfg, err := m.DescribeTable(ctx, "dst")
	requireNoError(t, err)
	if srcCfg.TableArn == dstCfg.TableArn {
		t.Fatalf("restored table must have a distinct ARN, both are %q", dstCfg.TableArn)
	}

	// Writing into the restored table must not leak into the source.
	seedItem(t, m, "dst", map[string]any{"pk": "z", "sk": "1"})
	if _, err := m.GetItem(ctx, "src", map[string]any{"pk": "z", "sk": "1"}); err == nil {
		t.Fatal("write to restored table leaked into source table")
	}
}

func TestRestoreTableFromBackupErrors(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(m, "src")
	createTestTable(m, "exists")

	info, err := m.CreateBackup(ctx, "src", "b")
	requireNoError(t, err)

	// Unknown backup ARN.
	assertError(t, m.RestoreTableFromBackup(ctx, "arn:aws:dynamodb:us-east-1:0:table/x/backup/nope", "t"), true)
	// Target already exists.
	assertError(t, m.RestoreTableFromBackup(ctx, info.BackupArn, "exists"), true)
}

func TestDeleteBackup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(m, "src")

	info, err := m.CreateBackup(ctx, "src", "b")
	requireNoError(t, err)

	_, err = m.DeleteBackup(ctx, info.BackupArn)
	requireNoError(t, err)

	_, err = m.DescribeBackup(ctx, info.BackupArn)
	assertError(t, err, true)
	// Deleting a missing backup is an error.
	_, err = m.DeleteBackup(ctx, info.BackupArn)
	assertError(t, err, true)
}

func TestListBackupsFiltersByTable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(m, "t1")
	createTestTable(m, "t2")

	_, err := m.CreateBackup(ctx, "t1", "b1")
	requireNoError(t, err)
	_, err = m.CreateBackup(ctx, "t2", "b2")
	requireNoError(t, err)

	all, err := m.ListBackups(ctx, "")
	requireNoError(t, err)
	assertEqual(t, 2, len(all))

	only, err := m.ListBackups(ctx, "t1")
	requireNoError(t, err)
	assertEqual(t, 1, len(only))
	assertEqual(t, "t1", only[0].SourceTable.Name)
}

func TestRestoreTableToPointInTimeRequiresPITR(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(m, "src")
	seedItem(t, m, "src", map[string]any{"pk": "a", "sk": "1", "v": "1"})

	// PITR disabled: rejected.
	assertError(t, m.RestoreTableToPointInTime(ctx, "src", "dst", true), true)

	requireNoError(t, m.SetPITR(ctx, "src", true))
	requireNoError(t, m.RestoreTableToPointInTime(ctx, "src", "dst", true))

	got, err := m.GetItem(ctx, "dst", map[string]any{"pk": "a", "sk": "1"})
	requireNoError(t, err)
	assertEqual(t, "1", got["v"])

	// Target already exists.
	assertError(t, m.RestoreTableToPointInTime(ctx, "src", "dst", true), true)
	// Missing source.
	assertError(t, m.RestoreTableToPointInTime(ctx, "ghost", "x", true), true)
}

func TestPITRWindow(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(m, "src")

	earliest, latest, err := m.PITRWindow(ctx, "src")
	requireNoError(t, err)
	if latest < earliest {
		t.Fatalf("latest %v must be >= earliest %v", latest, earliest)
	}

	_, _, err = m.PITRWindow(ctx, "ghost")
	assertError(t, err, true)
}
