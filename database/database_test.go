package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/database/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/providers/aws/dynamodb"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDriver() (driver.Database, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return dynamodb.New(opts), fc
}

func newTestDatabase(opts ...Option) (*Database, *config.FakeClock) {
	d, fc := newTestDriver()
	return NewDatabase(d, opts...), fc
}

func setupTableWithItem(t *testing.T, db *Database) {
	t.Helper()

	ctx := context.Background()

	err := db.CreateTable(ctx, driver.TableConfig{
		Name:         "test-table",
		PartitionKey: "pk",
		SortKey:      "sk",
	})
	require.NoError(t, err)

	err = db.PutItem(ctx, "test-table", map[string]any{"pk": "user1", "sk": "item1", "data": "hello"})
	require.NoError(t, err)
}

func TestNewDatabase(t *testing.T) {
	db, _ := newTestDatabase()

	require.NotNil(t, db)
	require.NotNil(t, db.driver)
}

func TestCreateTablePortable(t *testing.T) {
	db, _ := newTestDatabase()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		err := db.CreateTable(ctx, driver.TableConfig{Name: "my-table", PartitionKey: "pk"})
		require.NoError(t, err)
	})

	t.Run("duplicate error", func(t *testing.T) {
		err := db.CreateTable(ctx, driver.TableConfig{Name: "my-table", PartitionKey: "pk"})
		require.Error(t, err)
	})
}

func TestDeleteTablePortable(t *testing.T) {
	db, _ := newTestDatabase()
	ctx := context.Background()

	err := db.CreateTable(ctx, driver.TableConfig{Name: "del-table", PartitionKey: "pk"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		delErr := db.DeleteTable(ctx, "del-table")
		require.NoError(t, delErr)
	})

	t.Run("not found", func(t *testing.T) {
		delErr := db.DeleteTable(ctx, "nonexistent")
		require.Error(t, delErr)
	})
}

func TestDescribeTablePortable(t *testing.T) {
	db, _ := newTestDatabase()
	ctx := context.Background()

	err := db.CreateTable(ctx, driver.TableConfig{Name: "desc-table", PartitionKey: "pk"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		cfg, descErr := db.DescribeTable(ctx, "desc-table")
		require.NoError(t, descErr)
		assert.Equal(t, "desc-table", cfg.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, descErr := db.DescribeTable(ctx, "nonexistent")
		require.Error(t, descErr)
	})
}

func TestListTablesPortable(t *testing.T) {
	db, _ := newTestDatabase()
	ctx := context.Background()

	tables, err := db.ListTables(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(tables))

	err = db.CreateTable(ctx, driver.TableConfig{Name: "a", PartitionKey: "pk"})
	require.NoError(t, err)

	err = db.CreateTable(ctx, driver.TableConfig{Name: "b", PartitionKey: "pk"})
	require.NoError(t, err)

	tables, err = db.ListTables(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(tables))
}

func TestPutGetDeleteItemPortable(t *testing.T) {
	db, _ := newTestDatabase()
	ctx := context.Background()

	setupTableWithItem(t, db)

	t.Run("get existing item", func(t *testing.T) {
		item, err := db.GetItem(ctx, "test-table", map[string]any{"pk": "user1", "sk": "item1"})
		require.NoError(t, err)
		assert.Equal(t, "hello", item["data"])
	})

	t.Run("delete item", func(t *testing.T) {
		err := db.DeleteItem(ctx, "test-table", map[string]any{"pk": "user1", "sk": "item1"})
		require.NoError(t, err)

		item, _ := db.GetItem(ctx, "test-table", map[string]any{"pk": "user1", "sk": "item1"})
		assert.Nil(t, item)
	})
}

func TestQueryPortable(t *testing.T) {
	db, _ := newTestDatabase()
	ctx := context.Background()

	setupTableWithItem(t, db)

	result, err := db.Query(ctx, driver.QueryInput{
		Table: "test-table",
		KeyCondition: driver.KeyCondition{
			PartitionKey: "pk",
			PartitionVal: "user1",
		},
		ScanForward: true,
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, result.Count, 1)
}

func TestScanPortable(t *testing.T) {
	db, _ := newTestDatabase()
	ctx := context.Background()

	setupTableWithItem(t, db)

	result, err := db.Scan(ctx, driver.ScanInput{Table: "test-table"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, result.Count, 1)
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	db, _ := newTestDatabase(WithRecorder(rec))
	ctx := context.Background()

	err := db.CreateTable(ctx, driver.TableConfig{Name: "rec-table", PartitionKey: "pk"})
	require.NoError(t, err)

	err = db.PutItem(ctx, "rec-table", map[string]any{"pk": "k1", "data": "v"})
	require.NoError(t, err)

	_, err = db.GetItem(ctx, "rec-table", map[string]any{"pk": "k1"})
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 3)

	createCalls := rec.CallCountFor("database", "CreateTable")
	assert.Equal(t, 1, createCalls)

	putCalls := rec.CallCountFor("database", "PutItem")
	assert.Equal(t, 1, putCalls)

	getCalls := rec.CallCountFor("database", "GetItem")
	assert.Equal(t, 1, getCalls)
}

func TestWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	db, _ := newTestDatabase(WithRecorder(rec))
	ctx := context.Background()

	_, _ = db.DescribeTable(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last, "expected a recorded call")
	assert.NotNil(t, last.Error, "expected recorded call to have an error")
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	db, _ := newTestDatabase(WithMetrics(mc))
	ctx := context.Background()

	err := db.CreateTable(ctx, driver.TableConfig{Name: "met-table", PartitionKey: "pk"})
	require.NoError(t, err)

	err = db.PutItem(ctx, "met-table", map[string]any{"pk": "k1", "data": "v"})
	require.NoError(t, err)

	_, err = db.GetItem(ctx, "met-table", map[string]any{"pk": "k1"})
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 3)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 3)
}

func TestWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	db, _ := newTestDatabase(WithMetrics(mc))
	ctx := context.Background()

	_, _ = db.DescribeTable(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	db, _ := newTestDatabase(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("database", "CreateTable", injectedErr, inject.Always{})

	err := db.CreateTable(ctx, driver.TableConfig{Name: "fail-table", PartitionKey: "pk"})
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	db, _ := newTestDatabase(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("database", "PutItem", injectedErr, inject.Always{})

	err := db.CreateTable(ctx, driver.TableConfig{Name: "inj-table", PartitionKey: "pk"})
	require.NoError(t, err)

	err = db.PutItem(ctx, "inj-table", map[string]any{"pk": "k1"})
	require.Error(t, err)

	putCalls := rec.CallsFor("database", "PutItem")
	assert.Equal(t, 1, len(putCalls))
	assert.NotNil(t, putCalls[0].Error, "expected recorded PutItem call to have an error")
}

func TestWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	db, _ := newTestDatabase(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("database", "CreateTable", injectedErr, inject.Always{})

	err := db.CreateTable(ctx, driver.TableConfig{Name: "test", PartitionKey: "pk"})
	require.Error(t, err)

	inj.Remove("database", "CreateTable")

	err = db.CreateTable(ctx, driver.TableConfig{Name: "test", PartitionKey: "pk"})
	require.NoError(t, err)
}

func TestWithRateLimiter(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	d := dynamodb.New(opts)
	limiter := ratelimit.New(1, 1, fc)
	db := NewDatabase(d, WithRateLimiter(limiter))
	ctx := context.Background()

	err := db.CreateTable(ctx, driver.TableConfig{Name: "rl-table", PartitionKey: "pk"})
	require.NoError(t, err)

	_, err = db.ListTables(ctx)
	require.Error(t, err, "expected rate limit error on second call without time advance")
}

func TestWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	db, _ := newTestDatabase(WithLatency(latency))
	ctx := context.Background()

	err := db.CreateTable(ctx, driver.TableConfig{Name: "lat-table", PartitionKey: "pk"})
	require.NoError(t, err)

	tables, err := db.ListTables(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, len(tables))
}

func TestAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	db, _ := newTestDatabase(
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	err := db.CreateTable(ctx, driver.TableConfig{Name: "all-opts", PartitionKey: "pk"})
	require.NoError(t, err)

	err = db.PutItem(ctx, "all-opts", map[string]any{"pk": "k1"})
	require.NoError(t, err)

	assert.Equal(t, 2, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}

func TestPortableDeleteTableError(t *testing.T) {
	db, _ := newTestDatabase()
	ctx := context.Background()

	err := db.DeleteTable(ctx, "no-table")
	require.Error(t, err)
}

func TestPortableGetItemError(t *testing.T) {
	db, _ := newTestDatabase()
	ctx := context.Background()

	_, err := db.GetItem(ctx, "no-table", map[string]any{"pk": "k1"})
	require.Error(t, err)
}

func TestPortablePutItemError(t *testing.T) {
	db, _ := newTestDatabase()
	ctx := context.Background()

	err := db.PutItem(ctx, "no-table", map[string]any{"pk": "k1"})
	require.Error(t, err)
}
