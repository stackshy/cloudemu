package timestreamwrite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/timestreamwrite"
	"github.com/stackshy/cloudemu/v2/services/timestreamwrite/driver"
)

func newMock() *timestreamwrite.Mock {
	return timestreamwrite.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireError(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertException(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *driver.APIError, got %T: %v", err, err)
	}

	if apiErr.Exception != want {
		t.Fatalf("exception = %q, want %q", apiErr.Exception, want)
	}
}

func createDatabase(t *testing.T, m *timestreamwrite.Mock, name string) *driver.Database {
	t.Helper()

	db, err := m.CreateDatabase(context.Background(), &driver.CreateDatabaseInput{DatabaseName: name})
	requireNoError(t, err)

	return db
}

func TestCreateDatabaseMintsStableArnAndKmsKey(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	db := createDatabase(t, m, "metrics")
	assertEqual(t, db.Arn, "arn:aws:timestream:us-east-1:123456789012:database/metrics")

	if db.KmsKeyID == "" {
		t.Fatalf("expected an AWS-managed KMS key ARN")
	}

	assertEqual(t, db.TableCount, int64(0))

	// A second read is byte-stable on the computed fields.
	got, err := m.DescribeDatabase(ctx, "metrics")
	requireNoError(t, err)
	assertEqual(t, got.Arn, db.Arn)
	assertEqual(t, got.KmsKeyID, db.KmsKeyID)

	if !got.CreationTime.Equal(db.CreationTime) {
		t.Fatalf("creationTime drifted across reads")
	}
}

func TestCreateDatabaseDuplicateIsConflict(t *testing.T) {
	m := newMock()

	createDatabase(t, m, "dup")

	_, err := m.CreateDatabase(context.Background(), &driver.CreateDatabaseInput{DatabaseName: "dup"})
	requireError(t, err)
	assertException(t, err, driver.ExConflict)
}

func TestDescribeDatabaseMissing(t *testing.T) {
	m := newMock()

	_, err := m.DescribeDatabase(context.Background(), "missing")
	requireError(t, err)
	assertException(t, err, driver.ExResourceNotFound)
}

func TestUpdateDatabaseKmsKey(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	db := createDatabase(t, m, "db")

	const key = "arn:aws:kms:us-east-1:123456789012:key/custom"

	updated, err := m.UpdateDatabase(ctx, &driver.UpdateDatabaseInput{DatabaseName: "db", KmsKeyID: key})
	requireNoError(t, err)
	assertEqual(t, updated.KmsKeyID, key)
	assertEqual(t, updated.Arn, db.Arn)
}

func TestCreateTableRequiresDatabase(t *testing.T) {
	m := newMock()

	_, err := m.CreateTable(context.Background(), &driver.CreateTableInput{
		DatabaseName: "nope", TableName: "t",
	})
	requireError(t, err)
	assertException(t, err, driver.ExResourceNotFound)
}

func TestCreateTableAndTableCount(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	createDatabase(t, m, "db")

	table, err := m.CreateTable(ctx, &driver.CreateTableInput{
		DatabaseName: "db",
		TableName:    "cpu",
		RetentionProperties: &driver.RetentionProperties{
			MemoryStoreRetentionPeriodInHours:  24,
			MagneticStoreRetentionPeriodInDays: 73,
		},
	})
	requireNoError(t, err)
	assertEqual(t, table.Arn, "arn:aws:timestream:us-east-1:123456789012:database/db/table/cpu")
	assertEqual(t, table.TableStatus, driver.TableStatusActive)

	// Defaults are applied for the omitted blocks.
	if table.MagneticStoreWriteProperties == nil || table.Schema == nil {
		t.Fatalf("expected default magnetic-store and schema blocks")
	}

	// The parent database's table count reflects the new table.
	db, err := m.DescribeDatabase(ctx, "db")
	requireNoError(t, err)
	assertEqual(t, db.TableCount, int64(1))
}

func TestRetentionRoundTripsAndUpdates(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	createDatabase(t, m, "db")

	_, err := m.CreateTable(ctx, &driver.CreateTableInput{
		DatabaseName: "db", TableName: "t",
		RetentionProperties: &driver.RetentionProperties{
			MemoryStoreRetentionPeriodInHours:  12,
			MagneticStoreRetentionPeriodInDays: 30,
		},
	})
	requireNoError(t, err)

	got, err := m.DescribeTable(ctx, "db", "t")
	requireNoError(t, err)
	assertEqual(t, got.RetentionProperties.MemoryStoreRetentionPeriodInHours, int64(12))
	assertEqual(t, got.RetentionProperties.MagneticStoreRetentionPeriodInDays, int64(30))

	updated, err := m.UpdateTable(ctx, &driver.UpdateTableInput{
		DatabaseName: "db", TableName: "t",
		RetentionProperties: &driver.RetentionProperties{
			MemoryStoreRetentionPeriodInHours:  48,
			MagneticStoreRetentionPeriodInDays: 100,
		},
	})
	requireNoError(t, err)
	assertEqual(t, updated.RetentionProperties.MemoryStoreRetentionPeriodInHours, int64(48))
	assertEqual(t, updated.Arn, got.Arn)
}

func TestDeleteDatabaseWithTablesIsValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	createDatabase(t, m, "db")

	_, err := m.CreateTable(ctx, &driver.CreateTableInput{DatabaseName: "db", TableName: "t"})
	requireNoError(t, err)

	err = m.DeleteDatabase(ctx, "db")
	requireError(t, err)
	assertException(t, err, driver.ExValidation)

	// After deleting the table the database can be removed.
	requireNoError(t, m.DeleteTable(ctx, "db", "t"))
	requireNoError(t, m.DeleteDatabase(ctx, "db"))

	_, err = m.DescribeDatabase(ctx, "db")
	assertException(t, err, driver.ExResourceNotFound)
}

func TestListTablesFiltersByDatabase(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	createDatabase(t, m, "a")
	createDatabase(t, m, "b")

	_, err := m.CreateTable(ctx, &driver.CreateTableInput{DatabaseName: "a", TableName: "t1"})
	requireNoError(t, err)
	_, err = m.CreateTable(ctx, &driver.CreateTableInput{DatabaseName: "b", TableName: "t2"})
	requireNoError(t, err)

	tables, _, err := m.ListTables(ctx, "a", driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(tables), 1)
	assertEqual(t, tables[0].TableName, "t1")

	// An unfiltered list returns every table.
	all, _, err := m.ListTables(ctx, "", driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(all), 2)
}

func TestListTablesUnknownDatabase(t *testing.T) {
	m := newMock()

	_, _, err := m.ListTables(context.Background(), "ghost", driver.Page{})
	requireError(t, err)
	assertException(t, err, driver.ExResourceNotFound)
}

func TestTagsRoundTripOnDatabaseAndTable(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	db := createDatabase(t, m, "db")
	table, err := m.CreateTable(ctx, &driver.CreateTableInput{DatabaseName: "db", TableName: "t"})
	requireNoError(t, err)

	requireNoError(t, m.TagResource(ctx, db.Arn, []driver.Tag{{Key: "env", Value: "prod"}}))
	requireNoError(t, m.TagResource(ctx, table.Arn, []driver.Tag{{Key: "team", Value: "obs"}}))

	dbTags, err := m.ListTagsForResource(ctx, db.Arn)
	requireNoError(t, err)
	assertEqual(t, len(dbTags), 1)
	assertEqual(t, dbTags[0].Value, "prod")

	tableTags, err := m.ListTagsForResource(ctx, table.Arn)
	requireNoError(t, err)
	assertEqual(t, len(tableTags), 1)
	assertEqual(t, tableTags[0].Key, "team")

	requireNoError(t, m.UntagResource(ctx, db.Arn, []string{"env"}))
	dbTags, err = m.ListTagsForResource(ctx, db.Arn)
	requireNoError(t, err)
	assertEqual(t, len(dbTags), 0)
}

func TestSnapshotRestorePreservesIdentity(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	db := createDatabase(t, m, "db")
	_, err := m.CreateTable(ctx, &driver.CreateTableInput{DatabaseName: "db", TableName: "t"})
	requireNoError(t, err)

	snap, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, snap))

	got, err := restored.DescribeDatabase(ctx, "db")
	requireNoError(t, err)
	assertEqual(t, got.Arn, db.Arn)
	assertEqual(t, got.TableCount, int64(1))

	gotTable, err := restored.DescribeTable(ctx, "db", "t")
	requireNoError(t, err)
	assertEqual(t, gotTable.TableName, "t")
}
