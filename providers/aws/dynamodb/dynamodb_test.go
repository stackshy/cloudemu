package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/database/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc))
	return New(opts)
}

func createTestTable(m *Mock, name string) {
	_ = m.CreateTable(context.Background(), driver.TableConfig{
		Name:         name,
		PartitionKey: "pk",
		SortKey:      "sk",
	})
}

func TestCreateTable(t *testing.T) {
	tests := []struct {
		name      string
		tableName string
		setup     func(m *Mock)
		expectErr bool
	}{
		{name: "success", tableName: "users"},
		{
			name:      "already exists",
			tableName: "dup",
			setup: func(m *Mock) {
				createTestTable(m, "dup")
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			err := m.CreateTable(context.Background(), driver.TableConfig{
				Name: tc.tableName, PartitionKey: "pk",
			})
			assertError(t, err, tc.expectErr)
		})
	}
}

func TestDeleteTable(t *testing.T) {
	tests := []struct {
		name      string
		table     string
		setup     func(m *Mock)
		expectErr bool
	}{
		{
			name:  "success",
			table: "tbl",
			setup: func(m *Mock) { createTestTable(m, "tbl") },
		},
		{name: "not found", table: "nope", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			err := m.DeleteTable(context.Background(), tc.table)
			assertError(t, err, tc.expectErr)
		})
	}
}

func TestDescribeTable(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "mytable")

	t.Run("found", func(t *testing.T) {
		cfg, err := m.DescribeTable(ctx, "mytable")
		requireNoError(t, err)
		assertEqual(t, "mytable", cfg.Name)
		assertEqual(t, "pk", cfg.PartitionKey)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.DescribeTable(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestListTables(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	tables, err := m.ListTables(ctx)
	requireNoError(t, err)
	assertEqual(t, 0, len(tables))

	createTestTable(m, "a")
	createTestTable(m, "b")

	tables, err = m.ListTables(ctx)
	requireNoError(t, err)
	assertEqual(t, 2, len(tables))
}

func TestPutAndGetItem(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	t.Run("success", func(t *testing.T) {
		err := m.PutItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "info", "name": "Alice"})
		requireNoError(t, err)

		item, err := m.GetItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "info"})
		requireNoError(t, err)
		assertEqual(t, "Alice", item["name"])
	})

	t.Run("table not found for put", func(t *testing.T) {
		err := m.PutItem(ctx, "nope", map[string]any{"pk": "x"})
		assertError(t, err, true)
	})

	t.Run("table not found for get", func(t *testing.T) {
		_, err := m.GetItem(ctx, "nope", map[string]any{"pk": "x"})
		assertError(t, err, true)
	})

	t.Run("item not found", func(t *testing.T) {
		_, err := m.GetItem(ctx, "tbl", map[string]any{"pk": "missing", "sk": "missing"})
		assertError(t, err, true)
	})
}

func TestDeleteItem(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")
	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "info"})

	err := m.DeleteItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "info"})
	requireNoError(t, err)

	_, err = m.GetItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "info"})
	assertError(t, err, true)
}

func TestQuery(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "a", "val": "1"})
	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "b", "val": "2"})
	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "user2", "sk": "a", "val": "3"})

	t.Run("partition key only", func(t *testing.T) {
		result, err := m.Query(ctx, driver.QueryInput{
			Table:        "tbl",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "user1"},
		})
		requireNoError(t, err)
		assertEqual(t, 2, result.Count)
	})

	t.Run("with sort key condition =", func(t *testing.T) {
		result, err := m.Query(ctx, driver.QueryInput{
			Table: "tbl",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "user1",
				SortOp: "=", SortVal: "a",
			},
		})
		requireNoError(t, err)
		assertEqual(t, 1, result.Count)
	})

	t.Run("with sort key BEGINS_WITH", func(t *testing.T) {
		result, err := m.Query(ctx, driver.QueryInput{
			Table: "tbl",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "user1",
				SortOp: "BEGINS_WITH", SortVal: "a",
			},
		})
		requireNoError(t, err)
		assertEqual(t, 1, result.Count)
	})

	t.Run("table not found", func(t *testing.T) {
		_, err := m.Query(ctx, driver.QueryInput{Table: "nope"})
		assertError(t, err, true)
	})
}

func TestQueryBetween(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "u", "sk": "1"})
	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "u", "sk": "3"})
	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "u", "sk": "5"})

	result, err := m.Query(ctx, driver.QueryInput{
		Table: "tbl",
		KeyCondition: driver.KeyCondition{
			PartitionKey: "pk", PartitionVal: "u",
			SortOp: "BETWEEN", SortVal: "1", SortValEnd: "4",
		},
	})
	requireNoError(t, err)
	assertEqual(t, 2, result.Count)
}

func TestScanWithAllOperators(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "1", "sk": "a", "age": "25", "name": "Alice"})
	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "2", "sk": "b", "age": "30", "name": "Bob"})
	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "3", "sk": "c", "age": "35", "name": "Charlie"})

	tests := []struct {
		name    string
		filters []driver.ScanFilter
		expect  int
	}{
		{name: "equal", filters: []driver.ScanFilter{{Field: "name", Op: "=", Value: "Alice"}}, expect: 1},
		{name: "not equal", filters: []driver.ScanFilter{{Field: "name", Op: "!=", Value: "Alice"}}, expect: 2},
		{name: "less than", filters: []driver.ScanFilter{{Field: "age", Op: "<", Value: "30"}}, expect: 1},
		{name: "greater than", filters: []driver.ScanFilter{{Field: "age", Op: ">", Value: "30"}}, expect: 1},
		{name: "less equal", filters: []driver.ScanFilter{{Field: "age", Op: "<=", Value: "30"}}, expect: 2},
		{name: "greater equal", filters: []driver.ScanFilter{{Field: "age", Op: ">=", Value: "30"}}, expect: 2},
		{name: "contains", filters: []driver.ScanFilter{{Field: "name", Op: "CONTAINS", Value: "li"}}, expect: 2},
		{name: "begins with", filters: []driver.ScanFilter{{Field: "name", Op: "BEGINS_WITH", Value: "Ch"}}, expect: 1},
		{name: "no filters", filters: nil, expect: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := m.Scan(ctx, driver.ScanInput{Table: "tbl", Filters: tc.filters})
			requireNoError(t, err)
			assertEqual(t, tc.expect, result.Count)
		})
	}

	t.Run("table not found", func(t *testing.T) {
		_, err := m.Scan(ctx, driver.ScanInput{Table: "nope"})
		assertError(t, err, true)
	})
}

func TestNumericComparison(t *testing.T) {
	tests := []struct {
		name   string
		a, b   string
		expect int
	}{
		{name: "numeric less", a: "5", b: "10", expect: -1},
		{name: "numeric equal", a: "10", b: "10", expect: 0},
		{name: "numeric greater", a: "20", b: "10", expect: 1},
		{name: "string less", a: "abc", b: "def", expect: -1},
		{name: "string equal", a: "abc", b: "abc", expect: 0},
		{name: "string greater", a: "def", b: "abc", expect: 1},
		{name: "float comparison", a: "1.5", b: "2.5", expect: -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := compareValues(tc.a, tc.b)
			assertEqual(t, tc.expect, result)
		})
	}
}

func TestBatchOperations(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	items := []map[string]any{
		{"pk": "1", "sk": "a", "val": "x"},
		{"pk": "2", "sk": "b", "val": "y"},
	}

	t.Run("batch put", func(t *testing.T) {
		err := m.BatchPutItems(ctx, "tbl", items)
		requireNoError(t, err)
	})

	t.Run("batch get", func(t *testing.T) {
		keys := []map[string]any{
			{"pk": "1", "sk": "a"},
			{"pk": "2", "sk": "b"},
			{"pk": "3", "sk": "c"},
		}
		result, err := m.BatchGetItems(ctx, "tbl", keys)
		requireNoError(t, err)
		assertEqual(t, 2, len(result))
	})

	t.Run("batch put table not found", func(t *testing.T) {
		err := m.BatchPutItems(ctx, "nope", items)
		assertError(t, err, true)
	})

	t.Run("batch get table not found", func(t *testing.T) {
		_, err := m.BatchGetItems(ctx, "nope", nil)
		assertError(t, err, true)
	})
}

func TestQueryWithGSI(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_ = m.CreateTable(ctx, driver.TableConfig{
		Name:         "tbl",
		PartitionKey: "pk",
		SortKey:      "sk",
		GSIs: []driver.GSIConfig{
			{Name: "gsi-name", PartitionKey: "name", SortKey: "age"},
		},
	})

	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "1", "sk": "a", "name": "Alice", "age": "25"})
	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "2", "sk": "b", "name": "Alice", "age": "30"})

	t.Run("query on GSI", func(t *testing.T) {
		result, err := m.Query(ctx, driver.QueryInput{
			Table:     "tbl",
			IndexName: "gsi-name",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "name", PartitionVal: "Alice",
			},
		})
		requireNoError(t, err)
		assertEqual(t, 2, result.Count)
	})

	t.Run("GSI not found", func(t *testing.T) {
		_, err := m.Query(ctx, driver.QueryInput{
			Table:     "tbl",
			IndexName: "nonexistent",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "name", PartitionVal: "Alice",
			},
		})
		assertError(t, err, true)
	})
}

// --- test helpers ---

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()
	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}
