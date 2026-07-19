package cosmosdb

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// e2eCampaignMock builds a Cosmos DB mock with a fake clock so TTL and stream
// timestamps are deterministic.
func e2eCampaignMock() (*Mock, *config.FakeClock) {
	clk := config.NewFakeClock(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk))

	return New(opts), clk
}

// TestE2ECampaignLifecycle walks a full user journey: create container, write
// items with varied attribute types, read, query with sort conditions, update
// (the driver's conditional path), batch ops, delete items, delete container.
func TestE2ECampaignLifecycle(t *testing.T) {
	ctx := context.Background()
	m, _ := e2eCampaignMock()

	// Create container with partition + sort key.
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{
		Name:         "orders",
		PartitionKey: "pk",
		SortKey:      "sk",
	}))

	names, err := m.ListTables(ctx)
	require.NoError(t, err)
	assert.Contains(t, names, "orders")

	cfg, err := m.DescribeTable(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, "orders", cfg.Name)
	assert.Equal(t, "pk", cfg.PartitionKey)
	assert.Equal(t, "sk", cfg.SortKey)

	// DescribeTable must return a copy, not the live config.
	cfg.PartitionKey = "mutated"
	cfg2, err := m.DescribeTable(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, "pk", cfg2.PartitionKey)

	// Put items with varied attribute types: string, number, bool, nil,
	// nested map, list, empty string, and a large-ish (128 KiB) value.
	large := strings.Repeat("x", 128*1024)
	item := map[string]any{
		"pk":     "cust#1",
		"sk":     "order#001",
		"name":   "Widget",
		"qty":    3,
		"price":  19.99,
		"active": true,
		"note":   nil,
		"empty":  "",
		"tags":   []any{"a", "b"},
		"meta":   map[string]any{"color": "red", "weight": 1.5},
		"blob":   large,
	}
	require.NoError(t, m.PutItem(ctx, "orders", item))

	got, err := m.GetItem(ctx, "orders", map[string]any{"pk": "cust#1", "sk": "order#001"})
	require.NoError(t, err)
	assert.Equal(t, "Widget", got["name"])
	assert.Equal(t, 3, got["qty"])
	assert.Equal(t, 19.99, got["price"])
	assert.Equal(t, true, got["active"])
	assert.Nil(t, got["note"])
	assert.Equal(t, "", got["empty"])
	assert.Equal(t, []any{"a", "b"}, got["tags"])
	assert.Equal(t, map[string]any{"color": "red", "weight": 1.5}, got["meta"])
	assert.Len(t, got["blob"], 128*1024)

	// PutItem is an unconditional upsert: same key fully replaces.
	require.NoError(t, m.PutItem(ctx, "orders", map[string]any{
		"pk": "cust#1", "sk": "order#001", "name": "Widget v2",
	}))

	got, err = m.GetItem(ctx, "orders", map[string]any{"pk": "cust#1", "sk": "order#001"})
	require.NoError(t, err)
	assert.Equal(t, "Widget v2", got["name"])
	assert.NotContains(t, got, "qty", "upsert should replace, not merge")

	// More rows in the same partition for query-by-key with sort conditions.
	for i := 2; i <= 5; i++ {
		require.NoError(t, m.PutItem(ctx, "orders", map[string]any{
			"pk": "cust#1", "sk": fmt.Sprintf("order#%03d", i), "qty": i,
		}))
	}
	require.NoError(t, m.PutItem(ctx, "orders", map[string]any{
		"pk": "cust#2", "sk": "order#001", "qty": 100,
	}))

	// Partition-equality query.
	res, err := m.Query(ctx, driver.QueryInput{
		Table:        "orders",
		KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "cust#1"},
	})
	require.NoError(t, err)
	assert.Equal(t, 5, res.Count)
	assert.Len(t, res.Items, 5)

	// Sort-key conditions: >, BETWEEN, BEGINS_WITH.
	res, err = m.Query(ctx, driver.QueryInput{
		Table: "orders",
		KeyCondition: driver.KeyCondition{
			PartitionKey: "pk", PartitionVal: "cust#1",
			SortOp: OpGreaterThan, SortVal: "order#003",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Count)

	res, err = m.Query(ctx, driver.QueryInput{
		Table: "orders",
		KeyCondition: driver.KeyCondition{
			PartitionKey: "pk", PartitionVal: "cust#1",
			SortOp: OpBetween, SortVal: "order#002", SortValEnd: "order#004",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 3, res.Count)

	res, err = m.Query(ctx, driver.QueryInput{
		Table: "orders",
		KeyCondition: driver.KeyCondition{
			PartitionKey: "pk", PartitionVal: "cust#1",
			SortOp: OpBeginsWith, SortVal: "order#",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 5, res.Count)

	// UpdateItem SET + REMOVE on an existing item.
	updated, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "orders",
		Key:   map[string]any{"pk": "cust#1", "sk": "order#002"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "status", Value: "shipped"},
			{Action: "REMOVE", Field: "qty"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "shipped", updated["status"])
	assert.NotContains(t, updated, "qty")

	got, err = m.GetItem(ctx, "orders", map[string]any{"pk": "cust#1", "sk": "order#002"})
	require.NoError(t, err)
	assert.Equal(t, "shipped", got["status"])

	// Unsupported update action is rejected with InvalidArgument.
	_, err = m.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   "orders",
		Key:     map[string]any{"pk": "cust#1", "sk": "order#002"},
		Actions: []driver.UpdateAction{{Action: "ADD", Field: "qty", Value: 1}},
	})
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))

	// Conditional-write semantics in this driver: UpdateItem is the
	// "must already exist" path — it fails with NotFound on a missing item
	// (PutItem never fails, so this is the only conditional-failure path).
	_, err = m.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   "orders",
		Key:     map[string]any{"pk": "cust#9", "sk": "order#999"},
		Actions: []driver.UpdateAction{{Action: "SET", Field: "x", Value: 1}},
	})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	// Batch ops.
	batch := []map[string]any{
		{"pk": "cust#3", "sk": "a", "n": 1},
		{"pk": "cust#3", "sk": "b", "n": 2},
		{"pk": "cust#3", "sk": "c", "n": 3},
	}
	require.NoError(t, m.BatchPutItems(ctx, "orders", batch))

	fetched, err := m.BatchGetItems(ctx, "orders", []map[string]any{
		{"pk": "cust#3", "sk": "a"},
		{"pk": "cust#3", "sk": "c"},
		{"pk": "cust#3", "sk": "missing"}, // silently skipped
	})
	require.NoError(t, err)
	assert.Len(t, fetched, 2, "missing keys are skipped, not errored")

	// TransactWriteItems: puts then deletes atomically.
	require.NoError(t, m.TransactWriteItems(ctx, "orders",
		[]map[string]any{{"pk": "cust#4", "sk": "t1", "v": "tx"}},
		[]map[string]any{{"pk": "cust#3", "sk": "b"}},
	))

	got, err = m.GetItem(ctx, "orders", map[string]any{"pk": "cust#4", "sk": "t1"})
	require.NoError(t, err)
	assert.Equal(t, "tx", got["v"])

	_, err = m.GetItem(ctx, "orders", map[string]any{"pk": "cust#3", "sk": "b"})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	// Delete item, then delete again (idempotent).
	require.NoError(t, m.DeleteItem(ctx, "orders", map[string]any{"pk": "cust#1", "sk": "order#001"}))
	require.NoError(t, m.DeleteItem(ctx, "orders", map[string]any{"pk": "cust#1", "sk": "order#001"}))

	_, err = m.GetItem(ctx, "orders", map[string]any{"pk": "cust#1", "sk": "order#001"})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	// Delete container; it is gone from listings and subsequent ops 404.
	require.NoError(t, m.DeleteTable(ctx, "orders"))

	names, err = m.ListTables(ctx)
	require.NoError(t, err)
	assert.NotContains(t, names, "orders")

	err = m.PutItem(ctx, "orders", map[string]any{"pk": "p", "sk": "s"})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

// TestE2ECampaignEdgeCases covers typed errors on missing resources, duplicate
// creation, empty-table queries, unicode data, and key-coercion quirks.
func TestE2ECampaignEdgeCases(t *testing.T) {
	ctx := context.Background()
	m, _ := e2eCampaignMock()

	t.Run("operations on missing container return NotFound", func(t *testing.T) {
		_, err := m.GetItem(ctx, "ghost", map[string]any{"pk": "x"})
		require.Error(t, err)
		assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

		err = m.DeleteItem(ctx, "ghost", map[string]any{"pk": "x"})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))

		err = m.DeleteTable(ctx, "ghost")
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))

		_, err = m.DescribeTable(ctx, "ghost")
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))

		_, err = m.Query(ctx, driver.QueryInput{
			Table:        "ghost",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "x"},
		})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))

		_, err = m.Scan(ctx, driver.ScanInput{Table: "ghost"})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))

		_, err = m.BatchGetItems(ctx, "ghost", []map[string]any{{"pk": "x"}})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("duplicate container create returns AlreadyExists", func(t *testing.T) {
		require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "dup", PartitionKey: "pk"}))

		err := m.CreateTable(ctx, driver.TableConfig{Name: "dup", PartitionKey: "other"})
		require.Error(t, err)
		assert.True(t, cerrors.IsAlreadyExists(err))
		assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))
	})

	t.Run("missing item in existing container returns NotFound", func(t *testing.T) {
		require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "items", PartitionKey: "pk"}))

		_, err := m.GetItem(ctx, "items", map[string]any{"pk": "nope"})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("query and scan on empty container return empty results", func(t *testing.T) {
		require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "empty", PartitionKey: "pk"}))

		res, err := m.Query(ctx, driver.QueryInput{
			Table:        "empty",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "anything"},
		})
		require.NoError(t, err)
		assert.Zero(t, res.Count)
		assert.Empty(t, res.Items)
		assert.Empty(t, res.NextPageToken)

		res, err = m.Scan(ctx, driver.ScanInput{Table: "empty"})
		require.NoError(t, err)
		assert.Zero(t, res.Count)
		assert.Empty(t, res.Items)
	})

	t.Run("unicode keys and values round-trip", func(t *testing.T) {
		require.NoError(t, m.CreateTable(ctx, driver.TableConfig{
			Name: "unicode", PartitionKey: "pk", SortKey: "sk",
		}))

		item := map[string]any{
			"pk":    "ключ-🔑",
			"sk":    "日本語/ソート",
			"name":  "café ✨ naïve",
			"emoji": "👩‍👩‍👧‍👦",
		}
		require.NoError(t, m.PutItem(ctx, "unicode", item))

		got, err := m.GetItem(ctx, "unicode", map[string]any{"pk": "ключ-🔑", "sk": "日本語/ソート"})
		require.NoError(t, err)
		assert.Equal(t, "café ✨ naïve", got["name"])
		assert.Equal(t, "👩‍👩‍👧‍👦", got["emoji"])

		res, err := m.Query(ctx, driver.QueryInput{
			Table: "unicode",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "ключ-🔑",
				SortOp: OpBeginsWith, SortVal: "日本語",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Count)
	})

	t.Run("numeric and string partition values collide via %v key coercion", func(t *testing.T) {
		// Documented quirk: item identity is fmt.Sprintf("%v", pk), so 25
		// (int) and "25" (string) address the same slot.
		require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "coerce", PartitionKey: "pk"}))

		require.NoError(t, m.PutItem(ctx, "coerce", map[string]any{"pk": 25, "v": "int"}))
		require.NoError(t, m.PutItem(ctx, "coerce", map[string]any{"pk": "25", "v": "string"}))

		got, err := m.GetItem(ctx, "coerce", map[string]any{"pk": 25})
		require.NoError(t, err)
		assert.Equal(t, "string", got["v"], "string-keyed put overwrote the int-keyed item")

		res, err := m.Scan(ctx, driver.ScanInput{Table: "coerce"})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Count)
	})
}

// TestE2ECampaignPagination pages through 30 items with driver-level
// offset tokens on both Scan and Query.
func TestE2ECampaignPagination(t *testing.T) {
	ctx := context.Background()
	m, _ := e2eCampaignMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{
		Name: "paged", PartitionKey: "pk", SortKey: "sk",
	}))

	const total = 30

	items := make([]map[string]any, 0, total)
	for i := 0; i < total; i++ {
		items = append(items, map[string]any{
			"pk": "tenant#1", "sk": fmt.Sprintf("row#%02d", i), "n": i,
		})
	}
	require.NoError(t, m.BatchPutItems(ctx, "paged", items))

	t.Run("scan pages", func(t *testing.T) {
		seen := map[string]bool{}
		token := ""
		pages := 0

		for {
			res, err := m.Scan(ctx, driver.ScanInput{Table: "paged", Limit: 12, PageToken: token})
			require.NoError(t, err)
			require.LessOrEqual(t, res.Count, 12)

			pages++
			require.LessOrEqual(t, pages, 10, "runaway pagination loop")

			for _, it := range res.Items {
				sk := fmt.Sprintf("%v", it["sk"])
				assert.False(t, seen[sk], "item %s returned twice across pages", sk)
				seen[sk] = true
			}

			if res.NextPageToken == "" {
				break
			}

			token = res.NextPageToken
		}

		assert.Len(t, seen, total)
		assert.Equal(t, 3, pages) // 12 + 12 + 6
	})

	t.Run("query pages", func(t *testing.T) {
		seen := map[string]bool{}
		token := ""
		pages := 0

		for {
			res, err := m.Query(ctx, driver.QueryInput{
				Table:        "paged",
				KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "tenant#1"},
				Limit:        7,
				PageToken:    token,
			})
			require.NoError(t, err)

			pages++
			require.LessOrEqual(t, pages, 10, "runaway pagination loop")

			for _, it := range res.Items {
				seen[fmt.Sprintf("%v", it["sk"])] = true
			}

			if res.NextPageToken == "" {
				break
			}

			token = res.NextPageToken
		}

		assert.Len(t, seen, total)
		assert.Equal(t, 5, pages) // ceil(30/7)
	})

	t.Run("default limit is 100", func(t *testing.T) {
		res, err := m.Scan(ctx, driver.ScanInput{Table: "paged"})
		require.NoError(t, err)
		assert.Equal(t, total, res.Count)
		assert.Empty(t, res.NextPageToken)
	})
}

// TestE2ECampaignScanFilters exercises the AND-combined scan filter operators.
func TestE2ECampaignScanFilters(t *testing.T) {
	ctx := context.Background()
	m, _ := e2eCampaignMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "flt", PartitionKey: "pk"}))

	require.NoError(t, m.BatchPutItems(ctx, "flt", []map[string]any{
		{"pk": "a", "kind": "fruit", "name": "apple", "price": 3},
		{"pk": "b", "kind": "fruit", "name": "banana", "price": 1},
		{"pk": "c", "kind": "veg", "name": "carrot", "price": 2},
		{"pk": "d", "kind": "fruit", "name": "apricot", "price": 5},
	}))

	scan := func(filters ...driver.ScanFilter) *driver.QueryResult {
		res, err := m.Scan(ctx, driver.ScanInput{Table: "flt", Filters: filters})
		require.NoError(t, err)

		return res
	}

	assert.Equal(t, 3, scan(driver.ScanFilter{Field: "kind", Op: OpEqual, Value: "fruit"}).Count)
	assert.Equal(t, 3, scan(driver.ScanFilter{Field: "kind", Op: OpNotEqual, Value: "veg"}).Count)
	assert.Equal(t, 2, scan(driver.ScanFilter{Field: "name", Op: OpBeginsWith, Value: "ap"}).Count)
	assert.Equal(t, 1, scan(driver.ScanFilter{Field: "name", Op: OpContains, Value: "an"}).Count) // banana
	// numeric comparison: both sides parse as float64
	assert.Equal(t, 2, scan(driver.ScanFilter{Field: "price", Op: OpGreaterEqual, Value: 3}).Count)
	assert.Equal(t, 1, scan(driver.ScanFilter{Field: "price", Op: OpLessThan, Value: 2}).Count)

	// AND-combined filters.
	res := scan(
		driver.ScanFilter{Field: "kind", Op: OpEqual, Value: "fruit"},
		driver.ScanFilter{Field: "price", Op: OpGreaterThan, Value: 2},
	)
	assert.Equal(t, 2, res.Count) // apple(3), apricot(5)

	// Unknown operator matches nothing.
	assert.Zero(t, scan(driver.ScanFilter{Field: "kind", Op: "OR", Value: "fruit"}).Count)
}

// TestE2ECampaignTTL exercises deterministic lazy TTL expiry using the fake
// clock, including the BatchGetItems no-TTL-check divergence.
func TestE2ECampaignTTL(t *testing.T) {
	ctx := context.Background()
	m, clk := e2eCampaignMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "sessions", PartitionKey: "pk"}))

	// TTL config round-trip.
	require.NoError(t, m.UpdateTTL(ctx, "sessions", driver.TTLConfig{Enabled: true, AttributeName: "expiresAt"}))

	ttlCfg, err := m.DescribeTTL(ctx, "sessions")
	require.NoError(t, err)
	assert.True(t, ttlCfg.Enabled)
	assert.Equal(t, "expiresAt", ttlCfg.AttributeName)

	now := clk.Now().Unix()

	require.NoError(t, m.PutItem(ctx, "sessions", map[string]any{
		"pk": "s1", "expiresAt": now + 60, "user": "alice",
	}))
	require.NoError(t, m.PutItem(ctx, "sessions", map[string]any{
		"pk": "s2", "expiresAt": fmt.Sprintf("%d", now+3600), "user": "bob", // numeric string accepted
	}))
	require.NoError(t, m.PutItem(ctx, "sessions", map[string]any{
		"pk": "s3", "user": "carol", // no TTL attribute → never expires
	}))

	// Before expiry everything is visible.
	got, err := m.GetItem(ctx, "sessions", map[string]any{"pk": "s1"})
	require.NoError(t, err)
	assert.Equal(t, "alice", got["user"])

	res, err := m.Scan(ctx, driver.ScanInput{Table: "sessions"})
	require.NoError(t, err)
	assert.Equal(t, 3, res.Count)

	// Advance past s1's expiry but not s2's.
	clk.Advance(61 * time.Second)

	// BatchGetItems does NOT check TTL — expired s1 is still returned here
	// (documented divergence); do this before GetItem lazily deletes it.
	batch, err := m.BatchGetItems(ctx, "sessions", []map[string]any{{"pk": "s1"}})
	require.NoError(t, err)
	assert.Len(t, batch, 1, "BatchGetItems skips TTL checks by design")

	// Scan and Query skip (but do not delete) expired items.
	res, err = m.Scan(ctx, driver.ScanInput{Table: "sessions"})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Count)

	qres, err := m.Query(ctx, driver.QueryInput{
		Table:        "sessions",
		KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "s1"},
	})
	require.NoError(t, err)
	assert.Zero(t, qres.Count)

	// GetItem on the expired item returns NotFound and lazily deletes it.
	_, err = m.GetItem(ctx, "sessions", map[string]any{"pk": "s1"})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	// Proof of lazy deletion: disable TTL entirely; s1 stays gone while
	// s2/s3 are still present.
	require.NoError(t, m.UpdateTTL(ctx, "sessions", driver.TTLConfig{Enabled: false}))

	_, err = m.GetItem(ctx, "sessions", map[string]any{"pk": "s1"})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err), "expired item should have been physically removed")

	got, err = m.GetItem(ctx, "sessions", map[string]any{"pk": "s2"})
	require.NoError(t, err)
	assert.Equal(t, "bob", got["user"])

	got, err = m.GetItem(ctx, "sessions", map[string]any{"pk": "s3"})
	require.NoError(t, err)
	assert.Equal(t, "carol", got["user"])

	// Far future: s2 (numeric-string TTL) still valid, TTL disabled anyway.
	clk.Advance(10 * time.Hour)

	_, err = m.GetItem(ctx, "sessions", map[string]any{"pk": "s2"})
	require.NoError(t, err, "TTL disabled, nothing should expire")
}

// TestE2ECampaignChangeFeed exercises the Cosmos change-feed emulation:
// enablement gating, INSERT/MODIFY/REMOVE events, image capture per view
// type, sequence-number tokens, and the lease-000 shard id.
func TestE2ECampaignChangeFeed(t *testing.T) {
	ctx := context.Background()
	m, clk := e2eCampaignMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "feed", PartitionKey: "pk"}))

	// Reading the feed before enabling it fails with FailedPrecondition.
	_, err := m.GetStreamRecords(ctx, "feed", 10, "")
	require.Error(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(err))

	// Unknown container is NotFound, not FailedPrecondition.
	_, err = m.GetStreamRecords(ctx, "ghost", 10, "")
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	require.NoError(t, m.UpdateStreamConfig(ctx, "feed", driver.StreamConfig{
		Enabled: true, ViewType: ViewNewAndOld,
	}))

	start := clk.Now()

	// INSERT, MODIFY (via put-overwrite), MODIFY (via UpdateItem), REMOVE.
	require.NoError(t, m.PutItem(ctx, "feed", map[string]any{"pk": "doc1", "v": 1}))
	clk.Advance(time.Second)
	require.NoError(t, m.PutItem(ctx, "feed", map[string]any{"pk": "doc1", "v": 2}))

	_, err = m.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   "feed",
		Key:     map[string]any{"pk": "doc1"},
		Actions: []driver.UpdateAction{{Action: "SET", Field: "v", Value: 3}},
	})
	require.NoError(t, err)
	require.NoError(t, m.DeleteItem(ctx, "feed", map[string]any{"pk": "doc1"}))

	it, err := m.GetStreamRecords(ctx, "feed", 0, "")
	require.NoError(t, err)
	assert.Equal(t, "lease-000", it.ShardID)
	require.Len(t, it.Records, 4)
	assert.Empty(t, it.NextToken)

	assert.Equal(t, "INSERT", it.Records[0].EventType)
	assert.Equal(t, "MODIFY", it.Records[1].EventType)
	assert.Equal(t, "MODIFY", it.Records[2].EventType)
	assert.Equal(t, "REMOVE", it.Records[3].EventType)

	// Monotonic sequence numbers and event ids.
	for i, rec := range it.Records {
		assert.Equal(t, fmt.Sprintf("%d", i+1), rec.SequenceNumber)
		assert.Equal(t, fmt.Sprintf("event-%d", i+1), rec.EventID)
		assert.Equal(t, "feed", rec.Table)
		assert.Equal(t, map[string]any{"pk": "doc1"}, rec.Keys)
	}

	// Timestamps come from the injected fake clock.
	assert.Equal(t, start, it.Records[0].Timestamp)
	assert.Equal(t, start.Add(time.Second), it.Records[1].Timestamp)

	// NEW_AND_OLD_IMAGES captures both sides.
	assert.Nil(t, it.Records[0].OldImage)
	assert.Equal(t, 1, it.Records[0].NewImage["v"])
	assert.Equal(t, 1, it.Records[1].OldImage["v"])
	assert.Equal(t, 2, it.Records[1].NewImage["v"])
	assert.Equal(t, 3, it.Records[3].OldImage["v"])
	assert.Nil(t, it.Records[3].NewImage)

	// Token-based consumption: read 2, then resume from the token.
	page1, err := m.GetStreamRecords(ctx, "feed", 2, "")
	require.NoError(t, err)
	require.Len(t, page1.Records, 2)
	assert.Equal(t, "2", page1.NextToken)

	page2, err := m.GetStreamRecords(ctx, "feed", 10, page1.NextToken)
	require.NoError(t, err)
	require.Len(t, page2.Records, 2)
	assert.Equal(t, "MODIFY", page2.Records[0].EventType)
	assert.Equal(t, "REMOVE", page2.Records[1].EventType)
	assert.Empty(t, page2.NextToken)

	// KEYS_ONLY view captures no images on subsequent events.
	require.NoError(t, m.UpdateStreamConfig(ctx, "feed", driver.StreamConfig{
		Enabled: true, ViewType: ViewKeysOnly,
	}))
	require.NoError(t, m.PutItem(ctx, "feed", map[string]any{"pk": "doc2", "v": 9}))

	it, err = m.GetStreamRecords(ctx, "feed", 10, "4")
	require.NoError(t, err)
	require.Len(t, it.Records, 1)
	assert.Equal(t, "INSERT", it.Records[0].EventType)
	assert.Nil(t, it.Records[0].NewImage)
	assert.Nil(t, it.Records[0].OldImage)
	assert.Equal(t, map[string]any{"pk": "doc2"}, it.Records[0].Keys)
}

// TestE2ECampaignIndexesAndTags exercises GSI lifecycle, GSI-backed queries,
// and the tagging surface.
func TestE2ECampaignIndexesAndTags(t *testing.T) {
	ctx := context.Background()
	m, _ := e2eCampaignMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{
		Name: "prods", PartitionKey: "pk", SortKey: "sk",
	}))

	t.Run("GSI lifecycle and query", func(t *testing.T) {
		info, err := m.CreateIndex(ctx, "prods", driver.GSIConfig{
			Name: "by-category", PartitionKey: "category", SortKey: "price",
		})
		require.NoError(t, err)
		assert.Equal(t, "ACTIVE", info.Status)

		// Duplicate index name → AlreadyExists; empty name → InvalidArgument.
		_, err = m.CreateIndex(ctx, "prods", driver.GSIConfig{Name: "by-category", PartitionKey: "x"})
		require.Error(t, err)
		assert.True(t, cerrors.IsAlreadyExists(err))

		_, err = m.CreateIndex(ctx, "prods", driver.GSIConfig{Name: "", PartitionKey: "x"})
		require.Error(t, err)
		assert.True(t, cerrors.IsInvalidArgument(err))

		require.NoError(t, m.BatchPutItems(ctx, "prods", []map[string]any{
			{"pk": "p1", "sk": "a", "category": "tools", "price": 10},
			{"pk": "p2", "sk": "a", "category": "tools", "price": 30},
			{"pk": "p3", "sk": "a", "category": "toys", "price": 20},
		}))

		// Query through the GSI with a numeric sort condition.
		res, err := m.Query(ctx, driver.QueryInput{
			Table:     "prods",
			IndexName: "by-category",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "category", PartitionVal: "tools",
				SortOp: OpGreaterEqual, SortVal: 10,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, res.Count)

		res, err = m.Query(ctx, driver.QueryInput{
			Table:     "prods",
			IndexName: "by-category",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "category", PartitionVal: "tools",
				SortOp: OpGreaterThan, SortVal: 10,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Count)

		// Unknown index → NotFound.
		_, err = m.Query(ctx, driver.QueryInput{
			Table:        "prods",
			IndexName:    "nope",
			KeyCondition: driver.KeyCondition{PartitionKey: "category", PartitionVal: "tools"},
		})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))

		infos, err := m.ListIndexes(ctx, "prods")
		require.NoError(t, err)
		require.Len(t, infos, 1)
		assert.Equal(t, "by-category", infos[0].Name)
		assert.Equal(t, "ACTIVE", infos[0].Status)

		desc, err := m.DescribeIndex(ctx, "prods", "by-category")
		require.NoError(t, err)
		assert.Equal(t, "category", desc.PartitionKey)
		assert.Equal(t, "price", desc.SortKey)

		require.NoError(t, m.DeleteIndex(ctx, "prods", "by-category"))

		err = m.DeleteIndex(ctx, "prods", "by-category")
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))

		_, err = m.DescribeIndex(ctx, "prods", "by-category")
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("tags merge, untag, list copy", func(t *testing.T) {
		require.NoError(t, m.TagResource(ctx, "prods", map[string]string{"env": "dev", "team": "core"}))
		require.NoError(t, m.TagResource(ctx, "prods", map[string]string{"env": "prod", "cost": "low"}))

		tags, err := m.ListTagsOfResource(ctx, "prods")
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"env": "prod", "team": "core", "cost": "low"}, tags)

		// Returned map is a copy: mutating it must not affect stored tags.
		tags["env"] = "hacked"
		tags2, err := m.ListTagsOfResource(ctx, "prods")
		require.NoError(t, err)
		assert.Equal(t, "prod", tags2["env"])

		// Untag removes given keys; unknown keys are ignored.
		require.NoError(t, m.UntagResource(ctx, "prods", []string{"team", "does-not-exist"}))

		tags3, err := m.ListTagsOfResource(ctx, "prods")
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"env": "prod", "cost": "low"}, tags3)

		// Tag ops on a missing container are typed NotFound.
		err = m.TagResource(ctx, "ghost", map[string]string{"a": "b"})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})
}
