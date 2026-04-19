package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/database/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/providers/aws/cloudwatch"
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

func TestUpdateTTL(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	t.Run("enable TTL", func(t *testing.T) {
		err := m.UpdateTTL(ctx, "tbl", driver.TTLConfig{Enabled: true, AttributeName: "expiresAt"})
		requireNoError(t, err)

		cfg, err := m.DescribeTTL(ctx, "tbl")
		requireNoError(t, err)
		assertEqual(t, true, cfg.Enabled)
		assertEqual(t, "expiresAt", cfg.AttributeName)
	})

	t.Run("table not found", func(t *testing.T) {
		err := m.UpdateTTL(ctx, "nope", driver.TTLConfig{Enabled: true, AttributeName: "ttl"})
		assertError(t, err, true)
	})

	t.Run("describe TTL table not found", func(t *testing.T) {
		_, err := m.DescribeTTL(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestTTLExpiry(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc))
	m := New(opts)
	ctx := context.Background()
	createTestTable(m, "tbl")

	// Enable TTL
	err := m.UpdateTTL(ctx, "tbl", driver.TTLConfig{Enabled: true, AttributeName: "expiresAt"})
	requireNoError(t, err)

	// Put item with TTL 1 hour from now
	ttlTime := fc.Now().Add(1 * time.Hour).Unix()
	err = m.PutItem(ctx, "tbl", map[string]any{
		"pk": "user1", "sk": "info", "name": "Alice", "expiresAt": ttlTime,
	})
	requireNoError(t, err)

	// Item should be accessible before TTL
	item, err := m.GetItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "info"})
	requireNoError(t, err)
	assertEqual(t, "Alice", item["name"])

	// Advance clock past TTL
	fc.Advance(2 * time.Hour)

	// Item should now be expired
	_, err = m.GetItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "info"})
	assertError(t, err, true)
}

func TestTTLFilterInScan(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc))
	m := New(opts)
	ctx := context.Background()
	createTestTable(m, "tbl")

	err := m.UpdateTTL(ctx, "tbl", driver.TTLConfig{Enabled: true, AttributeName: "ttl"})
	requireNoError(t, err)

	// One item expires in 1 hour, one in 3 hours
	_ = m.PutItem(ctx, "tbl", map[string]any{
		"pk": "1", "sk": "a", "ttl": fc.Now().Add(1 * time.Hour).Unix(),
	})
	_ = m.PutItem(ctx, "tbl", map[string]any{
		"pk": "2", "sk": "b", "ttl": fc.Now().Add(3 * time.Hour).Unix(),
	})
	_ = m.PutItem(ctx, "tbl", map[string]any{
		"pk": "3", "sk": "c", // no TTL attribute - never expires
	})

	// Before any expiry - all 3 visible
	result, err := m.Scan(ctx, driver.ScanInput{Table: "tbl"})
	requireNoError(t, err)
	assertEqual(t, 3, result.Count)

	// Advance 2 hours - item 1 expired
	fc.Advance(2 * time.Hour)

	result, err = m.Scan(ctx, driver.ScanInput{Table: "tbl"})
	requireNoError(t, err)
	assertEqual(t, 2, result.Count)

	// Advance 2 more hours - item 2 also expired
	fc.Advance(2 * time.Hour)

	result, err = m.Scan(ctx, driver.ScanInput{Table: "tbl"})
	requireNoError(t, err)
	assertEqual(t, 1, result.Count)
}

func TestTTLFilterInQuery(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc))
	m := New(opts)
	ctx := context.Background()
	createTestTable(m, "tbl")

	err := m.UpdateTTL(ctx, "tbl", driver.TTLConfig{Enabled: true, AttributeName: "ttl"})
	requireNoError(t, err)

	_ = m.PutItem(ctx, "tbl", map[string]any{
		"pk": "user1", "sk": "a", "ttl": fc.Now().Add(1 * time.Hour).Unix(),
	})
	_ = m.PutItem(ctx, "tbl", map[string]any{
		"pk": "user1", "sk": "b", "ttl": fc.Now().Add(3 * time.Hour).Unix(),
	})

	// Both items visible
	result, err := m.Query(ctx, driver.QueryInput{
		Table:        "tbl",
		KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "user1"},
	})
	requireNoError(t, err)
	assertEqual(t, 2, result.Count)

	// Expire one
	fc.Advance(2 * time.Hour)

	result, err = m.Query(ctx, driver.QueryInput{
		Table:        "tbl",
		KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "user1"},
	})
	requireNoError(t, err)
	assertEqual(t, 1, result.Count)
}

func TestStreamConfig(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	t.Run("enable streams", func(t *testing.T) {
		err := m.UpdateStreamConfig(ctx, "tbl", driver.StreamConfig{
			Enabled:  true,
			ViewType: "NEW_AND_OLD_IMAGES",
		})
		requireNoError(t, err)
	})

	t.Run("table not found", func(t *testing.T) {
		err := m.UpdateStreamConfig(ctx, "nope", driver.StreamConfig{Enabled: true})
		assertError(t, err, true)
	})

	t.Run("streams not enabled returns error", func(t *testing.T) {
		createTestTable(m, "no-stream")
		_, err := m.GetStreamRecords(ctx, "no-stream", 10, "")
		assertError(t, err, true)
	})
}

func TestStreamRecords(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	err := m.UpdateStreamConfig(ctx, "tbl", driver.StreamConfig{
		Enabled:  true,
		ViewType: "NEW_AND_OLD_IMAGES",
	})
	requireNoError(t, err)

	// INSERT event
	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "info", "name": "Alice"})

	// MODIFY event
	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "info", "name": "Alice Updated"})

	// DELETE (REMOVE event)
	_ = m.DeleteItem(ctx, "tbl", map[string]any{"pk": "user1", "sk": "info"})

	iter, err := m.GetStreamRecords(ctx, "tbl", 100, "")
	requireNoError(t, err)
	assertEqual(t, 3, len(iter.Records))
	assertEqual(t, "INSERT", iter.Records[0].EventType)
	assertEqual(t, "MODIFY", iter.Records[1].EventType)
	assertEqual(t, "REMOVE", iter.Records[2].EventType)

	// Verify NEW_AND_OLD_IMAGES view type
	assertEqual(t, "Alice", iter.Records[1].OldImage["name"])
	assertEqual(t, "Alice Updated", iter.Records[1].NewImage["name"])
}

func TestStreamPagination(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	err := m.UpdateStreamConfig(ctx, "tbl", driver.StreamConfig{
		Enabled:  true,
		ViewType: "KEYS_ONLY",
	})
	requireNoError(t, err)

	// Insert 5 items to generate 5 stream records
	for i := 0; i < 5; i++ {
		_ = m.PutItem(ctx, "tbl", map[string]any{
			"pk": "user", "sk": string(rune('a' + i)),
		})
	}

	// Get first page of 2
	iter, err := m.GetStreamRecords(ctx, "tbl", 2, "")
	requireNoError(t, err)
	assertEqual(t, 2, len(iter.Records))
	assertNotEmpty(t, iter.NextToken)

	// Get next page using token
	iter2, err := m.GetStreamRecords(ctx, "tbl", 2, iter.NextToken)
	requireNoError(t, err)
	assertEqual(t, 2, len(iter2.Records))
	assertNotEmpty(t, iter2.NextToken)

	// Last page
	iter3, err := m.GetStreamRecords(ctx, "tbl", 2, iter2.NextToken)
	requireNoError(t, err)
	assertEqual(t, 1, len(iter3.Records))
	assertEqual(t, "", iter3.NextToken)
}

func TestTransactWriteItems(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	t.Run("atomic puts and deletes", func(t *testing.T) {
		// Pre-insert an item to delete
		_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "old", "sk": "item", "val": "delete-me"})

		puts := []map[string]any{
			{"pk": "new1", "sk": "a", "val": "x"},
			{"pk": "new2", "sk": "b", "val": "y"},
		}
		deletes := []map[string]any{
			{"pk": "old", "sk": "item"},
		}

		err := m.TransactWriteItems(ctx, "tbl", puts, deletes)
		requireNoError(t, err)

		// Verify puts succeeded
		item, err := m.GetItem(ctx, "tbl", map[string]any{"pk": "new1", "sk": "a"})
		requireNoError(t, err)
		assertEqual(t, "x", item["val"])

		item, err = m.GetItem(ctx, "tbl", map[string]any{"pk": "new2", "sk": "b"})
		requireNoError(t, err)
		assertEqual(t, "y", item["val"])

		// Verify delete succeeded
		_, err = m.GetItem(ctx, "tbl", map[string]any{"pk": "old", "sk": "item"})
		assertError(t, err, true)
	})

	t.Run("table not found", func(t *testing.T) {
		err := m.TransactWriteItems(ctx, "nope", nil, nil)
		assertError(t, err, true)
	})
}

func TestDynamoDBMetricsEmission(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc))
	m := New(opts)
	ctx := context.Background()

	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	createTestTable(m, "tbl")

	t.Run("PutItem emits metrics", func(t *testing.T) {
		err := m.PutItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "a"})
		requireNoError(t, err)

		result, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  "AWS/DynamoDB",
			MetricName: "ConsumedWriteCapacityUnits",
			Dimensions: map[string]string{"TableName": "tbl"},
			StartTime:  fc.Now().Add(-1 * time.Hour),
			EndTime:    fc.Now().Add(1 * time.Hour),
			Period:     60,
			Stat:       "Sum",
		})
		requireNoError(t, err)
		assertEqual(t, true, len(result.Values) > 0)
	})

	t.Run("GetItem emits metrics", func(t *testing.T) {
		_, err := m.GetItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "a"})
		requireNoError(t, err)

		result, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  "AWS/DynamoDB",
			MetricName: "ConsumedReadCapacityUnits",
			Dimensions: map[string]string{"TableName": "tbl"},
			StartTime:  fc.Now().Add(-1 * time.Hour),
			EndTime:    fc.Now().Add(1 * time.Hour),
			Period:     60,
			Stat:       "Sum",
		})
		requireNoError(t, err)
		assertEqual(t, true, len(result.Values) > 0)
	})
}

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

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()
	if s == "" {
		t.Error("expected non-empty string")
	}
}

func TestUpdateItemSetFields(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "info", "name": "Alice", "age": 30})

	updated, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "tbl",
		Key:   map[string]any{"pk": "u1", "sk": "info"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "name", Value: "Alice Smith"},
			{Action: "SET", Field: "email", Value: "alice@test.com"},
		},
	})
	requireNoError(t, err)
	assertEqual(t, "Alice Smith", updated["name"])
	assertEqual(t, "alice@test.com", updated["email"])
	assertEqual(t, 30, updated["age"])
}

func TestUpdateItemRemoveFields(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "info", "name": "Alice", "city": "NYC"})

	updated, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "tbl",
		Key:   map[string]any{"pk": "u1", "sk": "info"},
		Actions: []driver.UpdateAction{
			{Action: "REMOVE", Field: "city"},
		},
	})
	requireNoError(t, err)

	if _, has := updated["city"]; has {
		t.Error("expected city to be removed")
	}

	assertEqual(t, "Alice", updated["name"])
}

func TestUpdateItemSetAndRemoveCombined(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "info", "name": "Alice", "old_field": "remove_me"})

	updated, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "tbl",
		Key:   map[string]any{"pk": "u1", "sk": "info"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "name", Value: "Bob"},
			{Action: "REMOVE", Field: "old_field"},
		},
	})
	requireNoError(t, err)
	assertEqual(t, "Bob", updated["name"])

	if _, has := updated["old_field"]; has {
		t.Error("expected old_field to be removed")
	}
}

func TestUpdateItemPersistsChanges(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "info", "v": "old"})

	_, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "tbl",
		Key:   map[string]any{"pk": "u1", "sk": "info"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "v", Value: "new"},
		},
	})
	requireNoError(t, err)

	got, err := m.GetItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "info"})
	requireNoError(t, err)
	assertEqual(t, "new", got["v"])
}

func TestUpdateItemTableNotFound(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   "nonexistent",
		Key:     map[string]any{"pk": "x"},
		Actions: []driver.UpdateAction{{Action: "SET", Field: "v", Value: 1}},
	})
	assertError(t, err, true)
}

func TestUpdateItemItemNotFound(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	_, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   "tbl",
		Key:     map[string]any{"pk": "missing", "sk": "missing"},
		Actions: []driver.UpdateAction{{Action: "SET", Field: "v", Value: 1}},
	})
	assertError(t, err, true)
}

func TestUpdateItemInvalidAction(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "info", "v": 1})

	_, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   "tbl",
		Key:     map[string]any{"pk": "u1", "sk": "info"},
		Actions: []driver.UpdateAction{{Action: "ADD", Field: "v", Value: 1}},
	})
	assertError(t, err, true)
}

func TestUpdateItemEmitsStreamRecord(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "tbl")

	_ = m.UpdateStreamConfig(ctx, "tbl", driver.StreamConfig{
		Enabled: true, ViewType: "NEW_AND_OLD_IMAGES",
	})

	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "info", "val": "old"})

	_, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "tbl",
		Key:   map[string]any{"pk": "u1", "sk": "info"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "val", Value: "new"},
		},
	})
	requireNoError(t, err)

	iter, err := m.GetStreamRecords(ctx, "tbl", 10, "")
	requireNoError(t, err)
	assertEqual(t, 2, len(iter.Records))
	assertEqual(t, "MODIFY", iter.Records[1].EventType)
	assertEqual(t, "old", iter.Records[1].OldImage["val"])
	assertEqual(t, "new", iter.Records[1].NewImage["val"])
}

func TestUpdateItemEmitsMetrics(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc))
	m := New(opts)
	ctx := context.Background()

	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)
	createTestTable(m, "tbl")

	_ = m.PutItem(ctx, "tbl", map[string]any{"pk": "u1", "sk": "info"})

	_, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "tbl",
		Key:   map[string]any{"pk": "u1", "sk": "info"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "v", Value: "x"},
		},
	})
	requireNoError(t, err)

	result, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace:  "AWS/DynamoDB",
		MetricName: "ConsumedWriteCapacityUnits",
		Dimensions: map[string]string{"TableName": "tbl"},
		StartTime:  fc.Now().Add(-1 * time.Hour),
		EndTime:    fc.Now().Add(1 * time.Hour),
		Period:     60,
		Stat:       "Sum",
	})
	requireNoError(t, err)
	assertEqual(t, true, len(result.Values) > 0)
}
