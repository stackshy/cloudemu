package cosmosdb

import (
	"context"
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

func TestUpdateTTL(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(t, m)

	tests := []struct {
		name    string
		table   string
		cfg     driver.TTLConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:  "enable TTL",
			table: "users",
			cfg:   driver.TTLConfig{Enabled: true, AttributeName: "expiry"},
		},
		{
			name:  "disable TTL",
			table: "users",
			cfg:   driver.TTLConfig{Enabled: false, AttributeName: "expiry"},
		},
		{
			name:    "table not found",
			table:   "missing",
			cfg:     driver.TTLConfig{Enabled: true, AttributeName: "expiry"},
			wantErr: true, errMsg: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.UpdateTTL(ctx, tt.table, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)

				got, err := m.DescribeTTL(ctx, tt.table)
				require.NoError(t, err)
				assert.Equal(t, tt.cfg.Enabled, got.Enabled)
				assert.Equal(t, tt.cfg.AttributeName, got.AttributeName)
			}
		})
	}
}

func TestTTLExpiry(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk))
	m := New(opts)

	ctx := context.Background()

	err := m.CreateTable(ctx, driver.TableConfig{Name: "ttl-table", PartitionKey: "pk", SortKey: "sk"})
	require.NoError(t, err)

	require.NoError(t, m.UpdateTTL(ctx, "ttl-table", driver.TTLConfig{Enabled: true, AttributeName: "expiry"}))

	pastExpiry := clk.Now().Unix() - 100
	futureExpiry := clk.Now().Unix() + 3600

	require.NoError(t, m.PutItem(ctx, "ttl-table", map[string]any{
		"pk": "u1", "sk": "s1", "expiry": pastExpiry,
	}))
	require.NoError(t, m.PutItem(ctx, "ttl-table", map[string]any{
		"pk": "u2", "sk": "s2", "expiry": futureExpiry,
	}))

	t.Run("expired item not returned by GetItem", func(t *testing.T) {
		_, err := m.GetItem(ctx, "ttl-table", map[string]any{"pk": "u1", "sk": "s1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("non-expired item returned", func(t *testing.T) {
		item, err := m.GetItem(ctx, "ttl-table", map[string]any{"pk": "u2", "sk": "s2"})
		require.NoError(t, err)
		assert.Equal(t, "u2", item["pk"])
	})

	t.Run("expired items excluded from Scan", func(t *testing.T) {
		result, err := m.Scan(ctx, driver.ScanInput{Table: "ttl-table"})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Count)
	})
}

func TestStreamConfig(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(t, m)

	tests := []struct {
		name    string
		table   string
		cfg     driver.StreamConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:  "enable change feed",
			table: "users",
			cfg:   driver.StreamConfig{Enabled: true, ViewType: "NEW_AND_OLD_IMAGES"},
		},
		{
			name:    "table not found",
			table:   "missing",
			cfg:     driver.StreamConfig{Enabled: true, ViewType: "NEW_IMAGE"},
			wantErr: true, errMsg: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.UpdateStreamConfig(ctx, tt.table, tt.cfg)

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

func TestStreamRecords(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(t, m)

	t.Run("stream not enabled returns error", func(t *testing.T) {
		_, err := m.GetStreamRecords(ctx, "users", 10, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not enabled")
	})

	require.NoError(t, m.UpdateStreamConfig(ctx, "users", driver.StreamConfig{
		Enabled: true, ViewType: "NEW_AND_OLD_IMAGES",
	}))

	t.Run("INSERT event recorded", func(t *testing.T) {
		require.NoError(t, m.PutItem(ctx, "users", map[string]any{"pk": "u1", "sk": "s1", "name": "Alice"}))

		iter, err := m.GetStreamRecords(ctx, "users", 10, "")
		require.NoError(t, err)
		require.NotEmpty(t, iter.Records)
		assert.Equal(t, "INSERT", iter.Records[0].EventType)
		assert.NotNil(t, iter.Records[0].NewImage)
	})

	t.Run("MODIFY event recorded", func(t *testing.T) {
		require.NoError(t, m.PutItem(ctx, "users", map[string]any{"pk": "u1", "sk": "s1", "name": "Bob"}))

		iter, err := m.GetStreamRecords(ctx, "users", 10, "")
		require.NoError(t, err)
		require.Len(t, iter.Records, 2)
		assert.Equal(t, "MODIFY", iter.Records[1].EventType)
	})

	t.Run("REMOVE event recorded", func(t *testing.T) {
		require.NoError(t, m.DeleteItem(ctx, "users", map[string]any{"pk": "u1", "sk": "s1"}))

		iter, err := m.GetStreamRecords(ctx, "users", 10, "")
		require.NoError(t, err)
		require.Len(t, iter.Records, 3)
		assert.Equal(t, "REMOVE", iter.Records[2].EventType)
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
	createTestTable(t, m)

	t.Run("puts and deletes atomically", func(t *testing.T) {
		// Seed an item to delete
		require.NoError(t, m.PutItem(ctx, "users", map[string]any{"pk": "del1", "sk": "s1", "val": "old"}))

		puts := []map[string]any{
			{"pk": "new1", "sk": "s1", "val": "v1"},
			{"pk": "new2", "sk": "s2", "val": "v2"},
		}
		deletes := []map[string]any{
			{"pk": "del1", "sk": "s1"},
		}

		err := m.TransactWriteItems(ctx, "users", puts, deletes)
		require.NoError(t, err)

		// Verify puts
		item, err := m.GetItem(ctx, "users", map[string]any{"pk": "new1", "sk": "s1"})
		require.NoError(t, err)
		assert.Equal(t, "v1", item["val"])

		item, err = m.GetItem(ctx, "users", map[string]any{"pk": "new2", "sk": "s2"})
		require.NoError(t, err)
		assert.Equal(t, "v2", item["val"])

		// Verify delete
		_, err = m.GetItem(ctx, "users", map[string]any{"pk": "del1", "sk": "s1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("table not found", func(t *testing.T) {
		err := m.TransactWriteItems(ctx, "missing", nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestCosmosDBMetricsEmission(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk))
	m := New(opts)

	mon := &cosmosMetricsCollector{}
	m.SetMonitoring(mon)

	ctx := context.Background()
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "metrics-table", PartitionKey: "pk", SortKey: "sk"}))

	t.Run("PutItem emits metrics", func(t *testing.T) {
		mon.reset()
		require.NoError(t, m.PutItem(ctx, "metrics-table", map[string]any{"pk": "u1", "sk": "s1"}))
		assert.True(t, mon.hasMetric("Microsoft.DocumentDB/databaseAccounts", "TotalRequests"))
	})

	t.Run("GetItem emits metrics", func(t *testing.T) {
		mon.reset()
		_, err := m.GetItem(ctx, "metrics-table", map[string]any{"pk": "u1", "sk": "s1"})
		require.NoError(t, err)
		assert.True(t, mon.hasMetric("Microsoft.DocumentDB/databaseAccounts", "TotalRequests"))
	})

	t.Run("Query emits metrics", func(t *testing.T) {
		mon.reset()
		_, err := m.Query(ctx, driver.QueryInput{
			Table:        "metrics-table",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "u1"},
		})
		require.NoError(t, err)
		assert.True(t, mon.hasMetric("Microsoft.DocumentDB/databaseAccounts", "TotalRequests"))
	})
}

type cosmosMetricsCollector struct {
	data []mondriver.MetricDatum
}

func (c *cosmosMetricsCollector) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	c.data = append(c.data, data...)
	return nil
}

func (c *cosmosMetricsCollector) GetMetricData(_ context.Context, _ mondriver.GetMetricInput) (*mondriver.MetricDataResult, error) {
	return &mondriver.MetricDataResult{}, nil
}

func (c *cosmosMetricsCollector) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (c *cosmosMetricsCollector) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (c *cosmosMetricsCollector) DescribeAlarms(_ context.Context, _ []string) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (c *cosmosMetricsCollector) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}

func (c *cosmosMetricsCollector) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (c *cosmosMetricsCollector) reset() {
	c.data = nil
}

func (c *cosmosMetricsCollector) hasMetric(namespace, metricName string) bool {
	for _, d := range c.data {
		if d.Namespace == namespace && d.MetricName == metricName {
			return true
		}
	}
	return false
}
