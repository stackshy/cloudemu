package firestore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/database/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
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

func TestUpdateTTL(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))

	tests := []struct {
		name      string
		table     string
		cfg       driver.TTLConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name:  "enable TTL",
			table: "t1",
			cfg:   driver.TTLConfig{Enabled: true, AttributeName: "expireAt"},
		},
		{
			name:      "table not found",
			table:     "missing",
			cfg:       driver.TTLConfig{Enabled: true, AttributeName: "expireAt"},
			wantErr:   true,
			errSubstr: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.UpdateTTL(ctx, tt.table, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				got, descErr := m.DescribeTTL(ctx, tt.table)
				require.NoError(t, descErr)
				assert.True(t, got.Enabled)
				assert.Equal(t, "expireAt", got.AttributeName)
			}
		})
	}
}

func TestTTLExpiry(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))
	m := New(opts)

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
	require.NoError(t, m.UpdateTTL(ctx, "t1", driver.TTLConfig{Enabled: true, AttributeName: "expireAt"}))

	// Set TTL to 60 seconds from now
	ttlTimestamp := clk.Now().Unix() + 60
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "name": "Alice", "expireAt": ttlTimestamp}))

	t.Run("item accessible before TTL", func(t *testing.T) {
		item, err := m.GetItem(ctx, "t1", map[string]any{"id": "1"})
		require.NoError(t, err)
		assert.Equal(t, "Alice", item["name"])
	})

	t.Run("item expired after TTL", func(t *testing.T) {
		clk.Advance(61 * time.Second)
		_, err := m.GetItem(ctx, "t1", map[string]any{"id": "1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestStreamConfig(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))

	tests := []struct {
		name      string
		table     string
		cfg       driver.StreamConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name:  "enable stream",
			table: "t1",
			cfg:   driver.StreamConfig{Enabled: true, ViewType: "NEW_AND_OLD_IMAGES"},
		},
		{
			name:      "table not found",
			table:     "missing",
			cfg:       driver.StreamConfig{Enabled: true, ViewType: "NEW_IMAGE"},
			wantErr:   true,
			errSubstr: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.UpdateStreamConfig(ctx, tt.table, tt.cfg)
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

func TestStreamRecords(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
	require.NoError(t, m.UpdateStreamConfig(ctx, "t1", driver.StreamConfig{Enabled: true, ViewType: "NEW_AND_OLD_IMAGES"}))

	t.Run("insert generates stream record", func(t *testing.T) {
		require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "name": "Alice"}))

		iter, err := m.GetStreamRecords(ctx, "t1", 10, "")
		require.NoError(t, err)
		require.Len(t, iter.Records, 1)
		assert.Equal(t, "INSERT", iter.Records[0].EventType)
		assert.Equal(t, "Alice", iter.Records[0].NewImage["name"])
	})

	t.Run("modify generates stream record", func(t *testing.T) {
		require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "name": "Bob"}))

		iter, err := m.GetStreamRecords(ctx, "t1", 10, "")
		require.NoError(t, err)
		require.Len(t, iter.Records, 2)
		assert.Equal(t, "MODIFY", iter.Records[1].EventType)
		assert.Equal(t, "Bob", iter.Records[1].NewImage["name"])
		assert.Equal(t, "Alice", iter.Records[1].OldImage["name"])
	})

	t.Run("delete generates stream record", func(t *testing.T) {
		require.NoError(t, m.DeleteItem(ctx, "t1", map[string]any{"id": "1"}))

		iter, err := m.GetStreamRecords(ctx, "t1", 10, "")
		require.NoError(t, err)
		require.Len(t, iter.Records, 3)
		assert.Equal(t, "REMOVE", iter.Records[2].EventType)
	})

	t.Run("stream not enabled returns error", func(t *testing.T) {
		require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t2", PartitionKey: "id"}))
		_, err := m.GetStreamRecords(ctx, "t2", 10, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not enabled")
	})

	t.Run("table not found", func(t *testing.T) {
		_, err := m.GetStreamRecords(ctx, "missing", 10, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestTransactWriteItems(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "name": "Alice"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "2", "name": "Bob"}))

	t.Run("atomic put and delete", func(t *testing.T) {
		err := m.TransactWriteItems(ctx, "t1",
			[]map[string]any{{"id": "3", "name": "Charlie"}},
			[]map[string]any{{"id": "1"}},
		)
		require.NoError(t, err)

		// item 3 should exist
		item, getErr := m.GetItem(ctx, "t1", map[string]any{"id": "3"})
		require.NoError(t, getErr)
		assert.Equal(t, "Charlie", item["name"])

		// item 1 should be deleted
		_, getErr = m.GetItem(ctx, "t1", map[string]any{"id": "1"})
		require.Error(t, getErr)
		assert.Contains(t, getErr.Error(), "not found")

		// item 2 should be unchanged
		item, getErr = m.GetItem(ctx, "t1", map[string]any{"id": "2"})
		require.NoError(t, getErr)
		assert.Equal(t, "Bob", item["name"])
	})

	t.Run("table not found", func(t *testing.T) {
		err := m.TransactWriteItems(ctx, "missing",
			[]map[string]any{{"id": "1"}}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestFirestoreMetricsEmission(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	mon := &firestoreMonMock{data: make(map[string][]mondriver.MetricDatum)}
	m := New(opts)
	m.SetMonitoring(mon)

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))

	t.Run("PutItem emits write metric", func(t *testing.T) {
		require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "name": "Alice"}))
		assert.NotEmpty(t, mon.data["firestore.googleapis.com/document/write_count"])
	})

	t.Run("GetItem emits read metric", func(t *testing.T) {
		_, err := m.GetItem(ctx, "t1", map[string]any{"id": "1"})
		require.NoError(t, err)
		assert.NotEmpty(t, mon.data["firestore.googleapis.com/document/read_count"])
	})

	t.Run("DeleteItem emits delete metric", func(t *testing.T) {
		require.NoError(t, m.DeleteItem(ctx, "t1", map[string]any{"id": "1"}))
		assert.NotEmpty(t, mon.data["firestore.googleapis.com/document/delete_count"])
	})
}

type firestoreMonMock struct {
	data map[string][]mondriver.MetricDatum
}

func (m *firestoreMonMock) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	for _, d := range data {
		key := d.Namespace + "/" + d.MetricName
		m.data[key] = append(m.data[key], d)
	}

	return nil
}

func (m *firestoreMonMock) GetMetricData(
	_ context.Context, _ mondriver.GetMetricInput,
) (*mondriver.MetricDataResult, error) {
	return &mondriver.MetricDataResult{}, nil
}

func (m *firestoreMonMock) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (m *firestoreMonMock) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (m *firestoreMonMock) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (m *firestoreMonMock) DescribeAlarms(_ context.Context, _ []string) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (m *firestoreMonMock) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}

func TestDescribeTable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "users", PartitionKey: "pk", SortKey: "sk"}))

	t.Run("success", func(t *testing.T) {
		cfg, err := m.DescribeTable(ctx, "users")
		require.NoError(t, err)
		assert.Equal(t, "users", cfg.Name)
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

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "pk"}))
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t2", PartitionKey: "pk"}))

	names, err := m.ListTables(ctx)
	require.NoError(t, err)
	assert.Len(t, names, 2)
	assert.Contains(t, names, "t1")
	assert.Contains(t, names, "t2")
}

func TestDescribeTTLSuccess(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
	require.NoError(t, m.UpdateTTL(ctx, "t1", driver.TTLConfig{Enabled: true, AttributeName: "expireAt"}))

	cfg, err := m.DescribeTTL(ctx, "t1")
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, "expireAt", cfg.AttributeName)
}

func TestDescribeTTLTableNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.DescribeTTL(ctx, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTTLFilteringInQuery(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))
	m := New(opts)

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "orders", PartitionKey: "customer", SortKey: "orderID"}))
	require.NoError(t, m.UpdateTTL(ctx, "orders", driver.TTLConfig{Enabled: true, AttributeName: "expireAt"}))

	ttlPast := clk.Now().Unix() + 30
	ttlFuture := clk.Now().Unix() + 3600

	require.NoError(t, m.PutItem(ctx, "orders", map[string]any{"customer": "alice", "orderID": "001", "expireAt": ttlPast}))
	require.NoError(t, m.PutItem(ctx, "orders", map[string]any{"customer": "alice", "orderID": "002", "expireAt": ttlFuture}))

	t.Run("both items visible before expiry", func(t *testing.T) {
		result, err := m.Query(ctx, driver.QueryInput{
			Table:        "orders",
			KeyCondition: driver.KeyCondition{PartitionKey: "customer", PartitionVal: "alice"},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, result.Count)
	})

	t.Run("expired item excluded from query", func(t *testing.T) {
		clk.Advance(60 * time.Second)
		result, err := m.Query(ctx, driver.QueryInput{
			Table:        "orders",
			KeyCondition: driver.KeyCondition{PartitionKey: "customer", PartitionVal: "alice"},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Count)
	})
}

func TestStreamRecordsViewTypes(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		viewType   string
		hasNew     bool
		hasOld     bool
		hasKeysNew bool
	}{
		{name: "KEYS_ONLY", viewType: ViewKeysOnly, hasNew: false, hasOld: false},
		{name: "NEW_IMAGE", viewType: ViewNewImage, hasNew: true, hasOld: false},
		{name: "OLD_IMAGE", viewType: ViewOldImage, hasNew: false, hasOld: false}, // no old on INSERT
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMock()
			require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
			require.NoError(t, m.UpdateStreamConfig(ctx, "t1", driver.StreamConfig{Enabled: true, ViewType: tt.viewType}))

			require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "name": "Alice"}))

			iter, err := m.GetStreamRecords(ctx, "t1", 10, "")
			require.NoError(t, err)
			require.Len(t, iter.Records, 1)

			rec := iter.Records[0]
			assert.Equal(t, "INSERT", rec.EventType)
			assert.NotNil(t, rec.Keys)

			if tt.hasNew {
				assert.NotNil(t, rec.NewImage)
			} else {
				assert.Nil(t, rec.NewImage)
			}

			if tt.hasOld {
				assert.NotNil(t, rec.OldImage)
			} else {
				assert.Nil(t, rec.OldImage)
			}
		})
	}

	t.Run("OLD_IMAGE on modify", func(t *testing.T) {
		m := newTestMock()
		require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
		require.NoError(t, m.UpdateStreamConfig(ctx, "t1", driver.StreamConfig{Enabled: true, ViewType: ViewOldImage}))

		require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "name": "Alice"}))
		require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "name": "Bob"}))

		iter, err := m.GetStreamRecords(ctx, "t1", 10, "")
		require.NoError(t, err)
		require.Len(t, iter.Records, 2)

		modifyRec := iter.Records[1]
		assert.Equal(t, "MODIFY", modifyRec.EventType)
		assert.NotNil(t, modifyRec.OldImage)
		assert.Equal(t, "Alice", modifyRec.OldImage["name"])
		assert.Nil(t, modifyRec.NewImage)
	})
}

func TestGetStreamRecordsPagination(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
	require.NoError(t, m.UpdateStreamConfig(ctx, "t1", driver.StreamConfig{Enabled: true, ViewType: ViewNewImage}))

	// Insert 5 items to generate 5 stream records
	for i := 1; i <= 5; i++ {
		require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": fmt.Sprintf("%d", i), "name": "item"}))
	}

	t.Run("paginate with limit 2", func(t *testing.T) {
		iter, err := m.GetStreamRecords(ctx, "t1", 2, "")
		require.NoError(t, err)
		assert.Len(t, iter.Records, 2)
		assert.NotEmpty(t, iter.NextToken)

		// Second page using the token
		iter2, err := m.GetStreamRecords(ctx, "t1", 2, iter.NextToken)
		require.NoError(t, err)
		assert.Len(t, iter2.Records, 2)
		assert.NotEmpty(t, iter2.NextToken)

		// Third page should have 1 remaining
		iter3, err := m.GetStreamRecords(ctx, "t1", 2, iter2.NextToken)
		require.NoError(t, err)
		assert.Len(t, iter3.Records, 1)
		assert.Empty(t, iter3.NextToken)
	})
}

func TestGetStreamRecordsNotEnabled(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))

	_, err := m.GetStreamRecords(ctx, "t1", 10, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not enabled")
}

func TestTransactWriteItemsWithStreamRecords(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
	require.NoError(t, m.UpdateStreamConfig(ctx, "t1", driver.StreamConfig{Enabled: true, ViewType: ViewNewAndOld}))

	// Pre-insert items to be deleted
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "name": "Alice"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "2", "name": "Bob"}))

	// Clear records by checking them
	_, err := m.GetStreamRecords(ctx, "t1", 100, "")
	require.NoError(t, err)

	// Execute transact write with mixed puts and deletes
	txErr := m.TransactWriteItems(ctx, "t1",
		[]map[string]any{{"id": "3", "name": "Charlie"}, {"id": "1", "name": "AliceUpdated"}},
		[]map[string]any{{"id": "2"}},
	)
	require.NoError(t, txErr)

	// Verify stream records generated for all operations
	iter, streamErr := m.GetStreamRecords(ctx, "t1", 100, "")
	require.NoError(t, streamErr)

	// 2 inserts + 2 transact puts (1 INSERT + 1 MODIFY) + 1 REMOVE = 5 total from beginning
	// But we want to check that the transact ops generated records
	// Total records: 2 (initial puts) + 2 (transact puts) + 1 (transact delete) = 5
	assert.Len(t, iter.Records, 5)

	// Verify the items are in correct state
	item, getErr := m.GetItem(ctx, "t1", map[string]any{"id": "3"})
	require.NoError(t, getErr)
	assert.Equal(t, "Charlie", item["name"])

	item, getErr = m.GetItem(ctx, "t1", map[string]any{"id": "1"})
	require.NoError(t, getErr)
	assert.Equal(t, "AliceUpdated", item["name"])

	_, getErr = m.GetItem(ctx, "t1", map[string]any{"id": "2"})
	require.Error(t, getErr)
}

func TestBatchPutAndGetItems(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))

	t.Run("batch put success", func(t *testing.T) {
		items := []map[string]any{
			{"id": "1", "name": "Alice"},
			{"id": "2", "name": "Bob"},
			{"id": "3", "name": "Charlie"},
		}
		require.NoError(t, m.BatchPutItems(ctx, "t1", items))
	})

	t.Run("batch get success", func(t *testing.T) {
		keys := []map[string]any{
			{"id": "1"},
			{"id": "2"},
			{"id": "999"}, // non-existent
		}
		results, err := m.BatchGetItems(ctx, "t1", keys)
		require.NoError(t, err)
		assert.Len(t, results, 2)
	})

	t.Run("batch put missing table", func(t *testing.T) {
		err := m.BatchPutItems(ctx, "missing", []map[string]any{{"id": "1"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("batch get missing table", func(t *testing.T) {
		_, err := m.BatchGetItems(ctx, "missing", []map[string]any{{"id": "1"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestQueryWithSortLessEqual(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "pk", SortKey: "sk"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"pk": "a", "sk": "001"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"pk": "a", "sk": "002"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"pk": "a", "sk": "003"}))

	t.Run("sort <=", func(t *testing.T) {
		result, err := m.Query(ctx, driver.QueryInput{
			Table:        "t1",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "a", SortOp: "<=", SortVal: "002"},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, result.Count)
	})

	t.Run("sort >=", func(t *testing.T) {
		result, err := m.Query(ctx, driver.QueryInput{
			Table:        "t1",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "a", SortOp: ">=", SortVal: "002"},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, result.Count)
	})

	t.Run("sort unsupported op", func(t *testing.T) {
		result, err := m.Query(ctx, driver.QueryInput{
			Table:        "t1",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "a", SortOp: "INVALID", SortVal: "002"},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, result.Count)
	})
}

func TestScanMetricsEmission(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	mon := &firestoreMonMock{data: make(map[string][]mondriver.MetricDatum)}
	m := New(opts)
	m.SetMonitoring(mon)

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "name": "Alice"}))

	t.Run("Scan emits read metric", func(t *testing.T) {
		_, err := m.Scan(ctx, driver.ScanInput{Table: "t1"})
		require.NoError(t, err)
		assert.NotEmpty(t, mon.data["firestore.googleapis.com/document/read_count"])
	})

	t.Run("Query emits read metric", func(t *testing.T) {
		before := len(mon.data["firestore.googleapis.com/document/read_count"])
		_, err := m.Query(ctx, driver.QueryInput{
			Table:        "t1",
			KeyCondition: driver.KeyCondition{PartitionKey: "id", PartitionVal: "1"},
		})
		require.NoError(t, err)
		assert.Greater(t, len(mon.data["firestore.googleapis.com/document/read_count"]), before)
	})
}

func TestToUnixTimestampVariousTypes(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want int64
	}{
		{name: "int64", val: int64(1000), want: 1000},
		{name: "float64", val: float64(2000.5), want: 2000},
		{name: "int", val: int(3000), want: 3000},
		{name: "string number", val: "4000", want: 4000},
		{name: "invalid string", val: "not-a-number", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, toUnixTimestamp(tt.val))
		})
	}
}

func TestCompareValuesStringGreater(t *testing.T) {
	assert.Equal(t, 1, compareValues("banana", "apple"))
}

func TestEmitMetricNilMonitoring(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	// No monitoring set - should not panic
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1"}))
}

func TestScanWithDefaultLimit(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))

	// Just test that scanning with no filters and default limit works
	result, err := m.Scan(ctx, driver.ScanInput{Table: "t1"})
	require.NoError(t, err)
	assert.Equal(t, 0, result.Count)
}

func TestScanUnsupportedFilter(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "id"}))
	require.NoError(t, m.PutItem(ctx, "t1", map[string]any{"id": "1", "score": 10}))

	result, err := m.Scan(ctx, driver.ScanInput{
		Table:   "t1",
		Filters: []driver.ScanFilter{{Field: "score", Op: "UNSUPPORTED", Value: 10}},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, result.Count)
}
