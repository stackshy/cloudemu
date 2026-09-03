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

// TestBackupSchemaIndependence guards that a backup and every table restored
// from it own their schema slices outright: an in-place GSI mutation on the
// source table (DeleteIndex's append-shift, CreateIndex's append-grow) must not
// leak into the backup's SourceTable or a restored table's indexes.
func TestBackupSchemaIndependence(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	requireNoError(t, m.CreateTable(ctx, driver.TableConfig{
		Name:         "products",
		PartitionKey: "pk",
		SortKey:      "sk",
		Attributes: []driver.AttributeDef{
			{Name: "pk", Type: "S"}, {Name: "sk", Type: "S"},
			{Name: "g1pk", Type: "S"}, {Name: "g2pk", Type: "S"}, {Name: "lsk", Type: "S"},
		},
		GSIs: []driver.GSIConfig{
			{Name: "idx1", PartitionKey: "g1pk", Projection: "INCLUDE", NonKeyAttributes: []string{"n1"}},
			{Name: "idx2", PartitionKey: "g2pk", Projection: "ALL"},
		},
		LSIs: []driver.LSIConfig{
			{Name: "lsi1", SortKey: "lsk", Projection: "INCLUDE", NonKeyAttributes: []string{"n2"}},
		},
	}))

	info, err := m.CreateBackup(ctx, "products", "b")
	requireNoError(t, err)
	requireNoError(t, m.RestoreTableFromBackup(ctx, info.BackupArn, "restored1"))
	requireNoError(t, m.RestoreTableFromBackup(ctx, info.BackupArn, "restored2"))

	// Mutate the SOURCE schema in place — the append-shift previously corrupted
	// the backing array shared with the backup; the append-grow exercises the
	// same aliasing on growth.
	requireNoError(t, m.DeleteIndex(ctx, "products", "idx1"))
	_, err = m.CreateIndex(ctx, "products", driver.GSIConfig{Name: "idx3", PartitionKey: "g2pk"})
	requireNoError(t, err)

	// Mutate a RESTORED table in place — this must not leak back into the backup
	// or into a sibling restore (which would happen if restore aliased the
	// backup's slices).
	requireNoError(t, m.DeleteIndex(ctx, "restored1", "idx2"))

	// The backup's schema must be untouched: both original GSIs, uncorrupted,
	// with their own NonKeyAttributes, plus the LSI and attribute definitions.
	got, err := m.DescribeBackup(ctx, info.BackupArn)
	requireNoError(t, err)
	assertEqual(t, 2, len(got.SourceTable.GSIs))
	assertEqual(t, "idx1", got.SourceTable.GSIs[0].Name)
	assertEqual(t, "idx2", got.SourceTable.GSIs[1].Name)
	assertEqual(t, 1, len(got.SourceTable.GSIs[0].NonKeyAttributes))
	assertEqual(t, "n1", got.SourceTable.GSIs[0].NonKeyAttributes[0])
	assertEqual(t, 1, len(got.SourceTable.LSIs))
	assertEqual(t, "n2", got.SourceTable.LSIs[0].NonKeyAttributes[0])
	assertEqual(t, 5, len(got.SourceTable.Attributes))

	// The untouched sibling restore must still show both original GSIs, its LSI
	// and its attribute definitions.
	assertGSINames(t, m, "restored2", "idx1", "idx2")

	rcfg, err := m.DescribeTable(ctx, "restored2")
	requireNoError(t, err)
	assertEqual(t, 1, len(rcfg.LSIs))
	assertEqual(t, "n2", rcfg.LSIs[0].NonKeyAttributes[0])
	assertEqual(t, 5, len(rcfg.Attributes))
}

// assertGSINames asserts a table's GSIs are exactly the given names.
func assertGSINames(t *testing.T, m *Mock, table string, want ...string) {
	t.Helper()

	idxs, err := m.ListIndexes(context.Background(), table)
	requireNoError(t, err)
	assertEqual(t, len(want), len(idxs))

	got := map[string]bool{}
	for _, ix := range idxs {
		got[ix.Name] = true
	}

	for _, name := range want {
		if !got[name] {
			t.Fatalf("table %q missing GSI %q; has %v", table, name, got)
		}
	}
}
