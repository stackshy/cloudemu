package firestore

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
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	return New(opts)
}

func TestCreateTable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.TableConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.TableConfig{Name: "users", PartitionKey: "pk"}},
		{name: "duplicate", cfg: driver.TableConfig{Name: "users", PartitionKey: "pk"}, wantErr: true, errSubstr: "already exists"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.CreateTable(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDeleteTable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "pk"}))

	tests := []struct {
		name      string
		table     string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", table: "t1"},
		{name: "not found", table: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteTable(ctx, tt.table)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestPutAndGetItem(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))

	t.Run("put and get success", func(t *testing.T) {
		item := map[string]any{"id": "1", "name": "Alice"}
		require.NoError(t, m.PutItem(ctx, "t1", item))

		got, err := m.GetItem(ctx, "t1", map[string]any{"id": "1"})
		require.NoError(t, err)
		assert.Equal(t, "Alice", got["name"])
	})

	t.Run("put to missing table", func(t *testing.T) {
		err := m.PutItem(ctx, "missing", map[string]any{"id": "1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("get from missing table", func(t *testing.T) {
		_, err := m.GetItem(ctx, "missing", map[string]any{"id": "1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("get missing item", func(t *testing.T) {
		_, err := m.GetItem(ctx, "t1", map[string]any{"id": "999"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDeleteItem(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "name": "Alice"}))

	t.Run("success", func(t *testing.T) {
		require.NoError(t, m.DeleteItem(ctx, "t1", map[string]any{"id": "1"}))
		_, err := m.GetItem(ctx, "t1", map[string]any{"id": "1"})
		require.Error(t, err)
	})

	t.Run("missing table", func(t *testing.T) {
		err := m.DeleteItem(ctx, "missing", map[string]any{"id": "1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestQuery(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "orders", PartitionKey: "customer", SortKey: "orderID"}))
	require.NoError(t, m.PutItem(ctx, "orders", map[string]any{"customer": "alice", "orderID": "001", "total": 100}))
	require.NoError(t, m.PutItem(ctx, "orders", map[string]any{"customer": "alice", "orderID": "002", "total": 200}))
	require.NoError(t, m.PutItem(ctx, "orders", map[string]any{"customer": "alice", "orderID": "003", "total": 300}))
	require.NoError(t, m.PutItem(ctx, "orders", map[string]any{"customer": "bob", "orderID": "001", "total": 50}))

	tests := []struct {
		name      string
		input     driver.QueryInput
		wantCount int
		wantErr   bool
		errSubstr string
	}{
		{name: "by partition key", input: driver.QueryInput{Table: "orders", KeyCondition: driver.KeyCondition{PartitionKey: "customer", PartitionVal: "alice"}}, wantCount: 3},
		{name: "with sort equal", input: driver.QueryInput{Table: "orders", KeyCondition: driver.KeyCondition{PartitionKey: "customer", PartitionVal: "alice", SortOp: "=", SortVal: "002"}}, wantCount: 1},
		{name: "with sort less than", input: driver.QueryInput{Table: "orders", KeyCondition: driver.KeyCondition{PartitionKey: "customer", PartitionVal: "alice", SortOp: "<", SortVal: "002"}}, wantCount: 1},
		{name: "with sort greater than", input: driver.QueryInput{Table: "orders", KeyCondition: driver.KeyCondition{PartitionKey: "customer", PartitionVal: "alice", SortOp: ">", SortVal: "001"}}, wantCount: 2},
		{name: "with sort begins_with", input: driver.QueryInput{Table: "orders", KeyCondition: driver.KeyCondition{PartitionKey: "customer", PartitionVal: "alice", SortOp: "BEGINS_WITH", SortVal: "00"}}, wantCount: 3},
		{name: "with sort between", input: driver.QueryInput{Table: "orders", KeyCondition: driver.KeyCondition{PartitionKey: "customer", PartitionVal: "alice", SortOp: "BETWEEN", SortVal: "001", SortValEnd: "002"}}, wantCount: 2},
		{name: "with limit", input: driver.QueryInput{Table: "orders", KeyCondition: driver.KeyCondition{PartitionKey: "customer", PartitionVal: "alice"}, Limit: 1}, wantCount: 1},
		{name: "missing table", input: driver.QueryInput{Table: "missing", KeyCondition: driver.KeyCondition{PartitionKey: "customer", PartitionVal: "alice"}}, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.Query(ctx, tt.input)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
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

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "items", PartitionKey: "id"}))
	require.NoError(t, m.PutItem(ctx, "items", map[string]any{"id": "1", "score": 10, "name": "alpha-one"}))
	require.NoError(t, m.PutItem(ctx, "items", map[string]any{"id": "2", "score": 20, "name": "beta-two"}))
	require.NoError(t, m.PutItem(ctx, "items", map[string]any{"id": "3", "score": 30, "name": "alpha-three"}))

	tests := []struct {
		name      string
		filters   []driver.ScanFilter
		wantCount int
	}{
		{name: "equal", filters: []driver.ScanFilter{{Field: "score", Op: "=", Value: 10}}, wantCount: 1},
		{name: "not equal", filters: []driver.ScanFilter{{Field: "score", Op: "!=", Value: 10}}, wantCount: 2},
		{name: "less than", filters: []driver.ScanFilter{{Field: "score", Op: "<", Value: 20}}, wantCount: 1},
		{name: "greater than", filters: []driver.ScanFilter{{Field: "score", Op: ">", Value: 10}}, wantCount: 2},
		{name: "less equal", filters: []driver.ScanFilter{{Field: "score", Op: "<=", Value: 20}}, wantCount: 2},
		{name: "greater equal", filters: []driver.ScanFilter{{Field: "score", Op: ">=", Value: 20}}, wantCount: 2},
		{name: "contains", filters: []driver.ScanFilter{{Field: "name", Op: "CONTAINS", Value: "alpha"}}, wantCount: 2},
		{name: "begins_with", filters: []driver.ScanFilter{{Field: "name", Op: "BEGINS_WITH", Value: "beta"}}, wantCount: 1},
		{name: "missing table", filters: nil, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := "items"
			// Special case for "missing table" test
			switch tt.name {
			case "missing table":
				_, err := m.Scan(ctx, driver.ScanInput{Table: "missing"})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "not found")
				return
			}
			result, err := m.Scan(ctx, driver.ScanInput{Table: table, Filters: tt.filters})
			require.NoError(t, err)
			assert.Equal(t, tt.wantCount, result.Count)
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
		{name: "numeric equal", a: "10", b: "10", want: 0},
		{name: "numeric less", a: "5", b: "10", want: -1},
		{name: "numeric greater", a: "20", b: "10", want: 1},
		{name: "float comparison", a: "1.5", b: "2.5", want: -1},
		{name: "string comparison", a: "apple", b: "banana", want: -1},
		{name: "string equal", a: "same", b: "same", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, compareValues(tt.a, tt.b))
		})
	}
}

func TestQueryWithGSI(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{
		Name: "t1", PartitionKey: "pk", SortKey: "sk",
		GSIs: []driver.GSIConfig{{Name: "gsi1", PartitionKey: "gsi_pk", SortKey: "gsi_sk"}},
	}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"pk": "a", "sk": "1", "gsi_pk": "x", "gsi_sk": "10"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"pk": "a", "sk": "2", "gsi_pk": "x", "gsi_sk": "20"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"pk": "b", "sk": "1", "gsi_pk": "y", "gsi_sk": "10"}))

	t.Run("query via GSI", func(t *testing.T) {
		result, err := m.Query(ctx, driver.QueryInput{
			Table:     "t1",
			IndexName: "gsi1",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "gsi_pk", PartitionVal: "x",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, result.Count)
	})

	t.Run("invalid GSI name", func(t *testing.T) {
		_, err := m.Query(ctx, driver.QueryInput{
			Table:     "t1",
			IndexName: "nonexistent",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "gsi_pk", PartitionVal: "x",
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}
