package cosmosdb

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/database/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk))

	return New(opts)
}

func createTestTable(t *testing.T, m *Mock) {
	t.Helper()

	err := m.CreateTable(context.Background(), driver.TableConfig{
		Name:         "users",
		PartitionKey: "pk",
		SortKey:      "sk",
	})
	require.NoError(t, err)
}

func TestCreateTable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name    string
		cfg     driver.TableConfig
		wantErr bool
		errMsg  string
	}{
		{name: "success", cfg: driver.TableConfig{Name: "table1", PartitionKey: "pk"}},
		{name: "duplicate", cfg: driver.TableConfig{Name: "table1", PartitionKey: "pk"}, wantErr: true, errMsg: "already exists"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.CreateTable(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDeleteTable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "table1", PartitionKey: "pk"}))

	tests := []struct {
		name    string
		table   string
		wantErr bool
		errMsg  string
	}{
		{name: "success", table: "table1"},
		{name: "not found", table: "missing", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteTable(ctx, tt.table)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDescribeTable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "table1", PartitionKey: "pk", SortKey: "sk"}))

	t.Run("success", func(t *testing.T) {
		cfg, err := m.DescribeTable(ctx, "table1")
		require.NoError(t, err)
		assert.Equal(t, "table1", cfg.Name)
		assert.Equal(t, "pk", cfg.PartitionKey)
		assert.Equal(t, "sk", cfg.SortKey)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.DescribeTable(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListTables(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("empty", func(t *testing.T) {
		tables, err := m.ListTables(ctx)
		require.NoError(t, err)
		assert.Empty(t, tables)
	})

	t.Run("with tables", func(t *testing.T) {
		require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "pk"}))
		require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t2", PartitionKey: "pk"}))

		tables, err := m.ListTables(ctx)
		require.NoError(t, err)
		assert.Len(t, tables, 2)
	})
}

func TestPutAndGetItem(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(t, m)

	t.Run("put and get", func(t *testing.T) {
		item := map[string]any{"pk": "user1", "sk": "profile", "name": "Alice"}
		require.NoError(t, m.PutItem(ctx, "users", item))

		result, err := m.GetItem(ctx, "users", map[string]any{"pk": "user1", "sk": "profile"})
		require.NoError(t, err)
		assert.Equal(t, "Alice", result["name"])
	})

	t.Run("get not found", func(t *testing.T) {
		_, err := m.GetItem(ctx, "users", map[string]any{"pk": "missing", "sk": "x"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("table not found", func(t *testing.T) {
		err := m.PutItem(ctx, "missing", map[string]any{"pk": "x"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDeleteItem(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(t, m)

	require.NoError(t, m.PutItem(ctx, "users", map[string]any{"pk": "u1", "sk": "s1"}))

	t.Run("success", func(t *testing.T) {
		err := m.DeleteItem(ctx, "users", map[string]any{"pk": "u1", "sk": "s1"})
		require.NoError(t, err)

		_, err = m.GetItem(ctx, "users", map[string]any{"pk": "u1", "sk": "s1"})
		require.Error(t, err)
	})

	t.Run("table not found", func(t *testing.T) {
		err := m.DeleteItem(ctx, "missing", map[string]any{"pk": "x"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestQuery(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(t, m)

	items := []map[string]any{
		{"pk": "user1", "sk": "a", "val": "1"},
		{"pk": "user1", "sk": "b", "val": "2"},
		{"pk": "user1", "sk": "c", "val": "3"},
		{"pk": "user2", "sk": "a", "val": "4"},
	}

	for _, item := range items {
		require.NoError(t, m.PutItem(ctx, "users", item))
	}

	tests := []struct {
		name      string
		input     driver.QueryInput
		wantCount int
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "partition key only",
			input:     driver.QueryInput{Table: "users", KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "user1"}},
			wantCount: 3,
		},
		{
			name: "with sort key equals",
			input: driver.QueryInput{Table: "users", KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "user1", SortOp: "=", SortVal: "a",
			}},
			wantCount: 1,
		},
		{
			name: "with sort key greater than",
			input: driver.QueryInput{Table: "users", KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "user1", SortOp: ">", SortVal: "a",
			}},
			wantCount: 2,
		},
		{
			name: "with BEGINS_WITH",
			input: driver.QueryInput{Table: "users", KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "user1", SortOp: "BEGINS_WITH", SortVal: "a",
			}},
			wantCount: 1,
		},
		{
			name: "with BETWEEN",
			input: driver.QueryInput{Table: "users", KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "user1", SortOp: "BETWEEN", SortVal: "a", SortValEnd: "b",
			}},
			wantCount: 2,
		},
		{
			name:    "table not found",
			input:   driver.QueryInput{Table: "missing", KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "x"}},
			wantErr: true, errMsg: "not found",
		},
		{
			name: "invalid index",
			input: driver.QueryInput{Table: "users", IndexName: "bad-index", KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "user1",
			}},
			wantErr: true, errMsg: "index",
		},
		{
			name: "with limit",
			input: driver.QueryInput{Table: "users", Limit: 1, KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "user1",
			}},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.Query(ctx, tt.input)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, result.Count)
			}
		})
	}
}

func TestScanWithAllOperators(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(t, m)

	items := []map[string]any{
		{"pk": "1", "sk": "a", "age": 25, "name": "Alice"},
		{"pk": "2", "sk": "b", "age": 30, "name": "Bob"},
		{"pk": "3", "sk": "c", "age": 35, "name": "Charlie"},
	}

	for _, item := range items {
		require.NoError(t, m.PutItem(ctx, "users", item))
	}

	tests := []struct {
		name      string
		filters   []driver.ScanFilter
		wantCount int
	}{
		{name: "equals", filters: []driver.ScanFilter{{Field: "age", Op: "=", Value: 30}}, wantCount: 1},
		{name: "not equals", filters: []driver.ScanFilter{{Field: "age", Op: "!=", Value: 30}}, wantCount: 2},
		{name: "less than", filters: []driver.ScanFilter{{Field: "age", Op: "<", Value: 30}}, wantCount: 1},
		{name: "greater than", filters: []driver.ScanFilter{{Field: "age", Op: ">", Value: 30}}, wantCount: 1},
		{name: "less equal", filters: []driver.ScanFilter{{Field: "age", Op: "<=", Value: 30}}, wantCount: 2},
		{name: "greater equal", filters: []driver.ScanFilter{{Field: "age", Op: ">=", Value: 30}}, wantCount: 2},
		{name: "contains", filters: []driver.ScanFilter{{Field: "name", Op: "CONTAINS", Value: "li"}}, wantCount: 2},
		{name: "begins with", filters: []driver.ScanFilter{{Field: "name", Op: "BEGINS_WITH", Value: "Al"}}, wantCount: 1},
		{name: "no filters", filters: nil, wantCount: 3},
		{name: "table not found", filters: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := "users"
			if tt.name == "table not found" {
				table = "missing"
			}

			result, err := m.Scan(ctx, driver.ScanInput{Table: table, Filters: tt.filters})

			switch tt.name {
			case "table not found":
				require.Error(t, err)
				assert.Contains(t, err.Error(), "not found")
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, result.Count)
			}
		})
	}
}

func TestNumericComparison(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{name: "numeric less", a: "5", b: "10", want: -1},
		{name: "numeric equal", a: "10", b: "10", want: 0},
		{name: "numeric greater", a: "15", b: "10", want: 1},
		{name: "string less", a: "apple", b: "banana", want: -1},
		{name: "string equal", a: "cat", b: "cat", want: 0},
		{name: "string greater", a: "dog", b: "cat", want: 1},
		{name: "float comparison", a: "3.14", b: "2.71", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, compareValues(tt.a, tt.b))
		})
	}
}

func TestBatchOperations(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(t, m)

	t.Run("batch put and get", func(t *testing.T) {
		items := []map[string]any{
			{"pk": "u1", "sk": "s1", "v": "v1"},
			{"pk": "u2", "sk": "s2", "v": "v2"},
		}

		require.NoError(t, m.BatchPutItems(ctx, "users", items))

		keys := []map[string]any{
			{"pk": "u1", "sk": "s1"},
			{"pk": "u2", "sk": "s2"},
			{"pk": "u3", "sk": "s3"},
		}

		results, err := m.BatchGetItems(ctx, "users", keys)
		require.NoError(t, err)
		assert.Len(t, results, 2)
	})

	t.Run("batch put table not found", func(t *testing.T) {
		err := m.BatchPutItems(ctx, "missing", []map[string]any{{"pk": "x"}})
		require.Error(t, err)
	})

	t.Run("batch get table not found", func(t *testing.T) {
		_, err := m.BatchGetItems(ctx, "missing", []map[string]any{{"pk": "x"}})
		require.Error(t, err)
	})
}
