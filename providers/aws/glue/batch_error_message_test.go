package glue_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// requireCleanBatchErrors fails if any per-entry ErrorMessage carries the
// internal canonical-code prefix ("NotFound: ...") — real Glue reports only the
// human message.
func requireCleanBatchErrors(t *testing.T, op string, errs []driver.BatchError) {
	t.Helper()

	if len(errs) == 0 {
		t.Fatalf("%s: expected a per-entry error", op)
	}

	for _, e := range errs {
		if e.ErrorMessage == "" || strings.HasPrefix(e.ErrorMessage, "NotFound: ") ||
			strings.HasPrefix(e.ErrorMessage, "AlreadyExists: ") {
			t.Fatalf("%s: ErrorMessage %q leaks internal error-code prefix", op, e.ErrorMessage)
		}
	}
}

func TestBatchErrorMessagesOmitInternalCodePrefix(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if err := m.CreateDatabase(ctx, "", driver.Database{Name: "db"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	tbl := driver.Table{
		Name:              "events",
		StorageDescriptor: &driver.StorageDescriptor{Columns: []driver.Column{{Name: "id", Type: "string"}}},
		PartitionKeys:     []driver.Column{{Name: "dt", Type: "string"}},
	}
	if err := m.CreateTable(ctx, "", "db", tbl); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	p := driver.Partition{Values: []string{"2024-01-01"}}
	if err := m.CreatePartition(ctx, "", "db", "events", p); err != nil {
		t.Fatalf("CreatePartition: %v", err)
	}

	errs, err := m.BatchCreatePartition(ctx, "", "db", "events", []driver.Partition{p})
	if err != nil {
		t.Fatalf("BatchCreatePartition: %v", err)
	}
	requireCleanBatchErrors(t, "BatchCreatePartition", errs)

	errs, err = m.BatchDeletePartition(ctx, "", "db", "events", [][]string{{"missing"}})
	if err != nil {
		t.Fatalf("BatchDeletePartition: %v", err)
	}
	requireCleanBatchErrors(t, "BatchDeletePartition", errs)

	errs, err = m.BatchDeleteTable(ctx, "", "db", []string{"nope"})
	if err != nil {
		t.Fatalf("BatchDeleteTable: %v", err)
	}
	requireCleanBatchErrors(t, "BatchDeleteTable", errs)

	errs, err = m.BatchDeleteTableVersion(ctx, "", "db", "events", []string{"99"})
	if err != nil {
		t.Fatalf("BatchDeleteTableVersion: %v", err)
	}
	requireCleanBatchErrors(t, "BatchDeleteTableVersion", errs)

	connErrs, err := m.BatchDeleteConnection(ctx, "", []string{"nope"})
	if err != nil {
		t.Fatalf("BatchDeleteConnection: %v", err)
	}
	flat := make([]driver.BatchError, 0, len(connErrs))
	for _, e := range connErrs {
		flat = append(flat, e)
	}
	requireCleanBatchErrors(t, "BatchDeleteConnection", flat)
}
