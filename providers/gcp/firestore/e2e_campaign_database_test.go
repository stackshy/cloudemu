package firestore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// E2E campaign cell: DATABASE / gcp / portable.
//
// These tests exercise the Firestore mock exclusively through the portable
// driver.Database API (no HTTP/SDK layer), simulating a realistic user
// journey: collection lifecycle, document CRUD, queries with sort
// conditions, indexes, batch/transactional writes, TTL with a fake clock,
// change streams, labels, and edge cases documented in the survey.
//
// Note on "conditional writes": the portable driver API has no
// ConditionExpression concept (survey: "No conditional writes anywhere").
// The condition-like semantics that DO exist and are tested here are:
//   - UpdateItem requires the document to exist (success vs NotFound),
//   - CreateTable / CreateIndex fail with AlreadyExists on duplicates.

func newE2EMock(t *testing.T) (*Mock, *config.FakeClock) {
	t.Helper()

	clk := config.NewFakeClock(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("e2e-project"))

	return New(opts), clk
}

// asDatabase guarantees we only use the portable interface surface.
func asDatabase(m *Mock) driver.Database { return m }

func TestE2ECampaign_Lifecycle(t *testing.T) {
	ctx := context.Background()
	m, _ := newE2EMock(t)
	db := asDatabase(m)

	const coll = "orders"

	// Create collection with partition + sort key.
	require.NoError(t, db.CreateTable(ctx, driver.TableConfig{
		Name:         coll,
		PartitionKey: "customerId",
		SortKey:      "orderId",
	}))

	// Describe returns a copy of the config.
	cfg, err := db.DescribeTable(ctx, coll)
	require.NoError(t, err)
	assert.Equal(t, coll, cfg.Name)
	assert.Equal(t, "customerId", cfg.PartitionKey)
	assert.Equal(t, "orderId", cfg.SortKey)

	// Mutating the returned copy must not affect the stored config.
	cfg.PartitionKey = "hacked"
	cfg2, err := db.DescribeTable(ctx, coll)
	require.NoError(t, err)
	assert.Equal(t, "customerId", cfg2.PartitionKey)

	names, err := db.ListTables(ctx)
	require.NoError(t, err)
	assert.Contains(t, names, coll)

	// Put items with varied attribute types, empty strings, large-ish values.
	largeVal := strings.Repeat("x", 64*1024) // 64 KiB payload
	items := []map[string]any{
		{
			"customerId": "cust-1",
			"orderId":    "2026-001",
			"total":      float64(99.5),
			"count":      int(3),
			"paid":       true,
			"note":       "", // empty string value
			"nothing":    nil,
			"tags":       []any{"priority", "gift"},
			"address":    map[string]any{"city": "Berlin", "zip": "10115"},
			"blob":       largeVal,
		},
		{"customerId": "cust-1", "orderId": "2026-002", "total": float64(10), "paid": false},
		{"customerId": "cust-1", "orderId": "2026-010", "total": float64(250), "paid": true},
		{"customerId": "cust-2", "orderId": "2026-003", "total": float64(42), "paid": true},
	}
	for _, it := range items {
		require.NoError(t, db.PutItem(ctx, coll, it))
	}

	// Get round-trips every attribute type.
	got, err := db.GetItem(ctx, coll, map[string]any{"customerId": "cust-1", "orderId": "2026-001"})
	require.NoError(t, err)
	assert.Equal(t, float64(99.5), got["total"])
	assert.Equal(t, 3, got["count"])
	assert.Equal(t, true, got["paid"])
	assert.Equal(t, "", got["note"])
	assert.Nil(t, got["nothing"])
	assert.Equal(t, []any{"priority", "gift"}, got["tags"])
	assert.Equal(t, map[string]any{"city": "Berlin", "zip": "10115"}, got["address"])
	assert.Len(t, got["blob"], 64*1024)

	// Query by partition key only.
	res, err := db.Query(ctx, driver.QueryInput{
		Table:        coll,
		KeyCondition: driver.KeyCondition{PartitionKey: "customerId", PartitionVal: "cust-1"},
	})
	require.NoError(t, err)
	assert.Equal(t, 3, res.Count)

	// Query with sort condition: BEGINS_WITH.
	res, err = db.Query(ctx, driver.QueryInput{
		Table: coll,
		KeyCondition: driver.KeyCondition{
			PartitionKey: "customerId", PartitionVal: "cust-1",
			SortOp: OpBeginsWith, SortVal: "2026-00",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Count)

	// Query with sort condition: BETWEEN (lexicographic on non-numeric strings).
	res, err = db.Query(ctx, driver.QueryInput{
		Table: coll,
		KeyCondition: driver.KeyCondition{
			PartitionKey: "customerId", PartitionVal: "cust-1",
			SortOp: OpBetween, SortVal: "2026-001", SortValEnd: "2026-003",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Count) // 2026-001, 2026-002

	// Query with sort condition: >=.
	res, err = db.Query(ctx, driver.QueryInput{
		Table: coll,
		KeyCondition: driver.KeyCondition{
			PartitionKey: "customerId", PartitionVal: "cust-1",
			SortOp: OpGreaterEqual, SortVal: "2026-002",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Count) // 2026-002, 2026-010

	// Update (SET + REMOVE) on an existing document — the "condition succeeds" path.
	updated, err := db.UpdateItem(ctx, driver.UpdateItemInput{
		Table: coll,
		Key:   map[string]any{"customerId": "cust-1", "orderId": "2026-002"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "paid", Value: true},
			{Action: "SET", Field: "shippedAt", Value: "2026-07-18"},
			{Action: "REMOVE", Field: "total"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, true, updated["paid"])
	assert.Equal(t, "2026-07-18", updated["shippedAt"])
	_, hasTotal := updated["total"]
	assert.False(t, hasTotal)

	// The "condition fails" path: UpdateItem on a missing document → NotFound.
	_, err = db.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   coll,
		Key:     map[string]any{"customerId": "cust-9", "orderId": "nope"},
		Actions: []driver.UpdateAction{{Action: "SET", Field: "paid", Value: true}},
	})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	// Unsupported update action → InvalidArgument.
	_, err = db.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   coll,
		Key:     map[string]any{"customerId": "cust-1", "orderId": "2026-001"},
		Actions: []driver.UpdateAction{{Action: "ADD", Field: "count", Value: 1}},
	})
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))

	// Batch put + batch get.
	batch := []map[string]any{
		{"customerId": "cust-3", "orderId": "b-1", "total": float64(1)},
		{"customerId": "cust-3", "orderId": "b-2", "total": float64(2)},
	}
	require.NoError(t, db.BatchPutItems(ctx, coll, batch))

	fetched, err := db.BatchGetItems(ctx, coll, []map[string]any{
		{"customerId": "cust-3", "orderId": "b-1"},
		{"customerId": "cust-3", "orderId": "b-2"},
		{"customerId": "cust-3", "orderId": "b-missing"}, // silently skipped
	})
	require.NoError(t, err)
	assert.Len(t, fetched, 2)

	// Delete item, verify gone, delete again (idempotent — no error).
	key := map[string]any{"customerId": "cust-2", "orderId": "2026-003"}
	require.NoError(t, db.DeleteItem(ctx, coll, key))

	_, err = db.GetItem(ctx, coll, key)
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
	require.NoError(t, db.DeleteItem(ctx, coll, key))

	// Delete collection; subsequent ops must fail typed NotFound.
	require.NoError(t, db.DeleteTable(ctx, coll))

	_, err = db.GetItem(ctx, coll, key)
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestE2ECampaign_EdgeCases(t *testing.T) {
	ctx := context.Background()
	m, _ := newE2EMock(t)
	db := asDatabase(m)

	const coll = "edge"
	require.NoError(t, db.CreateTable(ctx, driver.TableConfig{Name: coll, PartitionKey: "id"}))

	// Duplicate table create → AlreadyExists.
	err := db.CreateTable(ctx, driver.TableConfig{Name: coll, PartitionKey: "id"})
	require.Error(t, err)
	assert.True(t, cerrors.IsAlreadyExists(err))

	// Operations against a table that does not exist → typed NotFound.
	_, err = db.GetItem(ctx, "ghost", map[string]any{"id": "x"})
	assert.True(t, cerrors.IsNotFound(err))
	err = db.PutItem(ctx, "ghost", map[string]any{"id": "x"})
	assert.True(t, cerrors.IsNotFound(err))
	err = db.DeleteItem(ctx, "ghost", map[string]any{"id": "x"})
	assert.True(t, cerrors.IsNotFound(err))
	err = db.DeleteTable(ctx, "ghost")
	assert.True(t, cerrors.IsNotFound(err))
	_, err = db.DescribeTable(ctx, "ghost")
	assert.True(t, cerrors.IsNotFound(err))
	_, err = db.Query(ctx, driver.QueryInput{
		Table:        "ghost",
		KeyCondition: driver.KeyCondition{PartitionKey: "id", PartitionVal: "x"},
	})
	assert.True(t, cerrors.IsNotFound(err))
	_, err = db.Scan(ctx, driver.ScanInput{Table: "ghost"})
	assert.True(t, cerrors.IsNotFound(err))

	// Get a missing document in an existing collection → NotFound.
	_, err = db.GetItem(ctx, coll, map[string]any{"id": "missing"})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	// Query on an empty collection → zero items, no error.
	res, err := db.Query(ctx, driver.QueryInput{
		Table:        coll,
		KeyCondition: driver.KeyCondition{PartitionKey: "id", PartitionVal: "anything"},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Count)
	assert.Empty(t, res.Items)
	assert.Empty(t, res.NextPageToken)

	// Scan on an empty collection.
	sres, err := db.Scan(ctx, driver.ScanInput{Table: coll})
	require.NoError(t, err)
	assert.Equal(t, 0, sres.Count)

	// Unicode keys and values round-trip.
	uni := map[string]any{
		"id":    "ユーザー#1-Ω",
		"name":  "Grüße 世界 🚀",
		"emoji": "🔥🔥🔥",
	}
	require.NoError(t, db.PutItem(ctx, coll, uni))

	got, err := db.GetItem(ctx, coll, map[string]any{"id": "ユーザー#1-Ω"})
	require.NoError(t, err)
	assert.Equal(t, "Grüße 世界 🚀", got["name"])
	assert.Equal(t, "🔥🔥🔥", got["emoji"])

	res, err = db.Query(ctx, driver.QueryInput{
		Table:        coll,
		KeyCondition: driver.KeyCondition{PartitionKey: "id", PartitionVal: "ユーザー#1-Ω"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Count)

	// Empty-string partition key value is accepted and addressable.
	require.NoError(t, db.PutItem(ctx, coll, map[string]any{"id": "", "v": "empty-pk"}))
	got, err = db.GetItem(ctx, coll, map[string]any{"id": ""})
	require.NoError(t, err)
	assert.Equal(t, "empty-pk", got["v"])
}

// Survey-documented quirk: item identity is fmt.Sprintf("%v", pk), so
// numeric 25 and string "25" collide on the same document key.
func TestE2ECampaign_NumericStringKeyCollision(t *testing.T) {
	ctx := context.Background()
	m, _ := newE2EMock(t)
	db := asDatabase(m)

	const coll = "collide"
	require.NoError(t, db.CreateTable(ctx, driver.TableConfig{Name: coll, PartitionKey: "id"}))

	require.NoError(t, db.PutItem(ctx, coll, map[string]any{"id": 25, "src": "int"}))
	require.NoError(t, db.PutItem(ctx, coll, map[string]any{"id": "25", "src": "string"}))

	// The string write overwrote the int write: same %v-formatted key.
	got, err := db.GetItem(ctx, coll, map[string]any{"id": 25})
	require.NoError(t, err)
	assert.Equal(t, "string", got["src"])

	sres, err := db.Scan(ctx, driver.ScanInput{Table: coll})
	require.NoError(t, err)
	assert.Equal(t, 1, sres.Count)
}

func TestE2ECampaign_PaginationThrough30Items(t *testing.T) {
	ctx := context.Background()
	m, _ := newE2EMock(t)
	db := asDatabase(m)

	const coll = "feed"
	require.NoError(t, db.CreateTable(ctx, driver.TableConfig{
		Name: coll, PartitionKey: "userId", SortKey: "postId",
	}))

	const total = 30
	for i := 0; i < total; i++ {
		require.NoError(t, db.PutItem(ctx, coll, map[string]any{
			"userId": "u1",
			"postId": fmt.Sprintf("post-%03d", i),
			"seq":    float64(i),
		}))
	}

	// Query pagination: page size 7, walk the offset-token until exhausted.
	seen := make(map[string]bool)
	token := ""
	pages := 0

	for {
		res, err := db.Query(ctx, driver.QueryInput{
			Table:        coll,
			KeyCondition: driver.KeyCondition{PartitionKey: "userId", PartitionVal: "u1"},
			Limit:        7,
			PageToken:    token,
		})
		require.NoError(t, err)
		require.LessOrEqual(t, res.Count, 7)

		for _, it := range res.Items {
			id, _ := it["postId"].(string)
			assert.False(t, seen[id], "duplicate item across pages: %s", id)
			seen[id] = true
		}

		pages++
		require.LessOrEqual(t, pages, 10, "pagination did not terminate")

		if res.NextPageToken == "" {
			break
		}

		token = res.NextPageToken
	}

	assert.Len(t, seen, total)
	assert.Equal(t, 5, pages) // ceil(30/7)

	// Scan pagination with a filter that matches everything.
	seenScan := make(map[string]bool)
	token = ""
	pages = 0

	for {
		res, err := db.Scan(ctx, driver.ScanInput{
			Table:     coll,
			Filters:   []driver.ScanFilter{{Field: "userId", Op: OpEqual, Value: "u1"}},
			Limit:     11,
			PageToken: token,
		})
		require.NoError(t, err)

		for _, it := range res.Items {
			id, _ := it["postId"].(string)
			assert.False(t, seenScan[id], "duplicate item across scan pages: %s", id)
			seenScan[id] = true
		}

		pages++
		require.LessOrEqual(t, pages, 10, "scan pagination did not terminate")

		if res.NextPageToken == "" {
			break
		}

		token = res.NextPageToken
	}

	assert.Len(t, seenScan, total)

	// Default limit is 100 → a single unpaginated query returns everything.
	res, err := db.Query(ctx, driver.QueryInput{
		Table:        coll,
		KeyCondition: driver.KeyCondition{PartitionKey: "userId", PartitionVal: "u1"},
	})
	require.NoError(t, err)
	assert.Equal(t, total, res.Count)
	assert.Empty(t, res.NextPageToken)
}

func TestE2ECampaign_ScanFilters(t *testing.T) {
	ctx := context.Background()
	m, _ := newE2EMock(t)
	db := asDatabase(m)

	const coll = "products"
	require.NoError(t, db.CreateTable(ctx, driver.TableConfig{Name: coll, PartitionKey: "sku"}))

	require.NoError(t, db.BatchPutItems(ctx, coll, []map[string]any{
		{"sku": "a-1", "category": "book", "price": float64(9), "title": "Go in Action"},
		{"sku": "a-2", "category": "book", "price": float64(30), "title": "Go Systems"},
		{"sku": "b-1", "category": "toy", "price": float64(15), "title": "Robot Kit"},
	}))

	// AND-combined filters: category = book AND price > 10.
	res, err := db.Scan(ctx, driver.ScanInput{
		Table: coll,
		Filters: []driver.ScanFilter{
			{Field: "category", Op: OpEqual, Value: "book"},
			{Field: "price", Op: OpGreaterThan, Value: 10},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.Count)
	assert.Equal(t, "a-2", res.Items[0]["sku"])

	// CONTAINS on a string field.
	res, err = db.Scan(ctx, driver.ScanInput{
		Table:   coll,
		Filters: []driver.ScanFilter{{Field: "title", Op: OpContains, Value: "Robot"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Count)

	// BEGINS_WITH on the key field.
	res, err = db.Scan(ctx, driver.ScanInput{
		Table:   coll,
		Filters: []driver.ScanFilter{{Field: "sku", Op: OpBeginsWith, Value: "a-"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Count)

	// != filter.
	res, err = db.Scan(ctx, driver.ScanInput{
		Table:   coll,
		Filters: []driver.ScanFilter{{Field: "category", Op: OpNotEqual, Value: "book"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Count)

	// Numeric comparison is numeric (not lexicographic) when both sides parse:
	// price 9 < 15 even though "9" > "15" lexicographically.
	res, err = db.Scan(ctx, driver.ScanInput{
		Table:   coll,
		Filters: []driver.ScanFilter{{Field: "price", Op: OpLessThan, Value: 15}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.Count)
	assert.Equal(t, "a-1", res.Items[0]["sku"])
}

func TestE2ECampaign_TTLWithFakeClock(t *testing.T) {
	ctx := context.Background()
	m, clk := newE2EMock(t)
	db := asDatabase(m)

	const coll = "sessions"
	require.NoError(t, db.CreateTable(ctx, driver.TableConfig{Name: coll, PartitionKey: "sid"}))

	// Enable TTL on "expiresAt" and verify DescribeTTL echoes it back.
	require.NoError(t, db.UpdateTTL(ctx, coll, driver.TTLConfig{Enabled: true, AttributeName: "expiresAt"}))

	ttlCfg, err := db.DescribeTTL(ctx, coll)
	require.NoError(t, err)
	assert.True(t, ttlCfg.Enabled)
	assert.Equal(t, "expiresAt", ttlCfg.AttributeName)

	now := clk.Now().Unix()
	require.NoError(t, db.PutItem(ctx, coll, map[string]any{
		"sid": "s1", "user": "alice", "expiresAt": now + 60,
	}))
	require.NoError(t, db.PutItem(ctx, coll, map[string]any{
		"sid": "s2", "user": "bob", "expiresAt": float64(now + 3600),
	}))
	require.NoError(t, db.PutItem(ctx, coll, map[string]any{
		"sid": "s3", "user": "carol", // no TTL attribute → never expires
	}))
	require.NoError(t, db.PutItem(ctx, coll, map[string]any{
		"sid": "s4", "user": "dave", "expiresAt": fmt.Sprintf("%d", now+60), // numeric string accepted
	}))

	// Before expiry: everything is visible.
	got, err := db.GetItem(ctx, coll, map[string]any{"sid": "s1"})
	require.NoError(t, err)
	assert.Equal(t, "alice", got["user"])

	sres, err := db.Scan(ctx, driver.ScanInput{Table: coll})
	require.NoError(t, err)
	assert.Equal(t, 4, sres.Count)

	// Advance past s1/s4 expiry but not s2.
	clk.Advance(2 * time.Minute)

	// BatchGetItems does NOT check TTL (survey) — expired-but-not-reaped
	// items are still returned. Do this before GetItem lazily deletes s1.
	batch, err := db.BatchGetItems(ctx, coll, []map[string]any{{"sid": "s1"}})
	require.NoError(t, err)
	assert.Len(t, batch, 1, "BatchGetItems is documented to skip the TTL check")

	// GetItem sees expiry and lazily deletes.
	_, err = db.GetItem(ctx, coll, map[string]any{"sid": "s1"})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	// After lazy deletion the item is gone even for TTL-blind BatchGetItems.
	batch, err = db.BatchGetItems(ctx, coll, []map[string]any{{"sid": "s1"}})
	require.NoError(t, err)
	assert.Empty(t, batch)

	// Numeric-string TTL also expires.
	_, err = db.GetItem(ctx, coll, map[string]any{"sid": "s4"})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	// Scan and Query exclude expired items; unexpired + no-TTL remain.
	sres, err = db.Scan(ctx, driver.ScanInput{Table: coll})
	require.NoError(t, err)
	assert.Equal(t, 2, sres.Count) // s2, s3

	qres, err := db.Query(ctx, driver.QueryInput{
		Table:        coll,
		KeyCondition: driver.KeyCondition{PartitionKey: "sid", PartitionVal: "s2"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, qres.Count)

	// Advance beyond s2 expiry; s3 (no TTL attribute) survives forever.
	clk.Advance(2 * time.Hour)

	_, err = db.GetItem(ctx, coll, map[string]any{"sid": "s2"})
	assert.True(t, cerrors.IsNotFound(err))

	got, err = db.GetItem(ctx, coll, map[string]any{"sid": "s3"})
	require.NoError(t, err)
	assert.Equal(t, "carol", got["user"])

	// Disabling TTL makes remaining expired values visible again (lazy reaping
	// only happens while enabled) — s3 stays, and a freshly written expired
	// item is readable with TTL off.
	require.NoError(t, db.UpdateTTL(ctx, coll, driver.TTLConfig{Enabled: false, AttributeName: "expiresAt"}))
	require.NoError(t, db.PutItem(ctx, coll, map[string]any{
		"sid": "s5", "expiresAt": now - 1000,
	}))

	got, err = db.GetItem(ctx, coll, map[string]any{"sid": "s5"})
	require.NoError(t, err)
	assert.NotNil(t, got)
}

func TestE2ECampaign_Streams(t *testing.T) {
	ctx := context.Background()
	m, clk := newE2EMock(t)
	db := asDatabase(m)

	const coll = "audited"
	require.NoError(t, db.CreateTable(ctx, driver.TableConfig{Name: coll, PartitionKey: "id"}))

	// Streams disabled → FailedPrecondition.
	_, err := db.GetStreamRecords(ctx, coll, 10, "")
	require.Error(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(err))

	require.NoError(t, db.UpdateStreamConfig(ctx, coll, driver.StreamConfig{
		Enabled: true, ViewType: ViewNewAndOld,
	}))

	start := clk.Now()

	// INSERT, MODIFY (put over existing), MODIFY (update), REMOVE.
	require.NoError(t, db.PutItem(ctx, coll, map[string]any{"id": "d1", "v": 1}))
	clk.Advance(time.Second)
	require.NoError(t, db.PutItem(ctx, coll, map[string]any{"id": "d1", "v": 2}))
	_, err = db.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   coll,
		Key:     map[string]any{"id": "d1"},
		Actions: []driver.UpdateAction{{Action: "SET", Field: "v", Value: 3}},
	})
	require.NoError(t, err)
	require.NoError(t, db.DeleteItem(ctx, coll, map[string]any{"id": "d1"}))

	it, err := db.GetStreamRecords(ctx, coll, 100, "")
	require.NoError(t, err)
	require.Len(t, it.Records, 4)
	assert.Empty(t, it.NextToken)

	types := []string{}
	for i, r := range it.Records {
		types = append(types, r.EventType)
		assert.Equal(t, fmt.Sprintf("%d", i+1), r.SequenceNumber, "sequence numbers are '1','2',...")
		assert.Equal(t, fmt.Sprintf("event-%d", i+1), r.EventID)
		assert.Equal(t, coll, r.Table)
		assert.Equal(t, "d1", r.Keys["id"])
	}
	assert.Equal(t, []string{"INSERT", "MODIFY", "MODIFY", "REMOVE"}, types)

	// NEW_AND_OLD_IMAGES captured correctly.
	assert.Nil(t, it.Records[0].OldImage)
	assert.Equal(t, 1, it.Records[0].NewImage["v"])
	assert.Equal(t, 1, it.Records[1].OldImage["v"])
	assert.Equal(t, 2, it.Records[1].NewImage["v"])
	assert.Equal(t, 2, it.Records[2].OldImage["v"])
	assert.Equal(t, 3, it.Records[2].NewImage["v"])
	assert.Equal(t, 3, it.Records[3].OldImage["v"])
	assert.Nil(t, it.Records[3].NewImage)

	// Timestamps come from the injected fake clock.
	assert.Equal(t, start, it.Records[0].Timestamp)
	assert.Equal(t, start.Add(time.Second), it.Records[1].Timestamp)

	// Token-based resume: read 2, then continue from the token.
	first, err := db.GetStreamRecords(ctx, coll, 2, "")
	require.NoError(t, err)
	require.Len(t, first.Records, 2)
	require.NotEmpty(t, first.NextToken)
	assert.Equal(t, "2", first.NextToken)

	rest, err := db.GetStreamRecords(ctx, coll, 10, first.NextToken)
	require.NoError(t, err)
	require.Len(t, rest.Records, 2)
	assert.Equal(t, "3", rest.Records[0].SequenceNumber)
	assert.Empty(t, rest.NextToken)

	// Batch and transactional writes also record stream events.
	require.NoError(t, db.BatchPutItems(ctx, coll, []map[string]any{{"id": "d2", "v": 1}}))
	require.NoError(t, db.TransactWriteItems(ctx, coll,
		[]map[string]any{{"id": "d3", "v": 1}},
		[]map[string]any{{"id": "d2"}},
	))

	it, err = db.GetStreamRecords(ctx, coll, 100, "4")
	require.NoError(t, err)
	require.Len(t, it.Records, 3)
	assert.Equal(t, "INSERT", it.Records[0].EventType) // batch put d2
	assert.Equal(t, "INSERT", it.Records[1].EventType) // transact put d3
	assert.Equal(t, "REMOVE", it.Records[2].EventType) // transact delete d2
}

func TestE2ECampaign_TransactWriteItems(t *testing.T) {
	ctx := context.Background()
	m, _ := newE2EMock(t)
	db := asDatabase(m)

	const coll = "accounts"
	require.NoError(t, db.CreateTable(ctx, driver.TableConfig{Name: coll, PartitionKey: "id"}))

	require.NoError(t, db.PutItem(ctx, coll, map[string]any{"id": "old", "balance": float64(50)}))

	// Puts then deletes under one lock.
	require.NoError(t, db.TransactWriteItems(ctx, coll,
		[]map[string]any{
			{"id": "a", "balance": float64(100)},
			{"id": "b", "balance": float64(200)},
		},
		[]map[string]any{{"id": "old"}},
	))

	got, err := db.GetItem(ctx, coll, map[string]any{"id": "a"})
	require.NoError(t, err)
	assert.Equal(t, float64(100), got["balance"])

	_, err = db.GetItem(ctx, coll, map[string]any{"id": "old"})
	assert.True(t, cerrors.IsNotFound(err))

	// A put and delete of the SAME key in one transaction: puts apply first,
	// deletes second, so the item ends up deleted.
	require.NoError(t, db.TransactWriteItems(ctx, coll,
		[]map[string]any{{"id": "c", "balance": float64(5)}},
		[]map[string]any{{"id": "c"}},
	))
	_, err = db.GetItem(ctx, coll, map[string]any{"id": "c"})
	assert.True(t, cerrors.IsNotFound(err))

	// Unknown table → NotFound.
	err = db.TransactWriteItems(ctx, "ghost", []map[string]any{{"id": "x"}}, nil)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestE2ECampaign_Indexes(t *testing.T) {
	ctx := context.Background()
	m, _ := newE2EMock(t)
	db := asDatabase(m)

	const coll = "tickets"
	require.NoError(t, db.CreateTable(ctx, driver.TableConfig{
		Name: coll, PartitionKey: "id",
	}))

	require.NoError(t, db.BatchPutItems(ctx, coll, []map[string]any{
		{"id": "t1", "assignee": "alice", "priority": float64(1)},
		{"id": "t2", "assignee": "alice", "priority": float64(5)},
		{"id": "t3", "assignee": "bob", "priority": float64(3)},
	}))

	// Query against a not-yet-created index → NotFound.
	_, err := db.Query(ctx, driver.QueryInput{
		Table:        coll,
		IndexName:    "by-assignee",
		KeyCondition: driver.KeyCondition{PartitionKey: "assignee", PartitionVal: "alice"},
	})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	// Create the index; Status is always ACTIVE.
	info, err := db.CreateIndex(ctx, coll, driver.GSIConfig{
		Name: "by-assignee", PartitionKey: "assignee", SortKey: "priority",
	})
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", info.Status)

	// Empty index name → InvalidArgument.
	_, err = db.CreateIndex(ctx, coll, driver.GSIConfig{Name: "", PartitionKey: "x"})
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))

	// Duplicate index name → AlreadyExists.
	_, err = db.CreateIndex(ctx, coll, driver.GSIConfig{Name: "by-assignee", PartitionKey: "assignee"})
	require.Error(t, err)
	assert.True(t, cerrors.IsAlreadyExists(err))

	// Query via the GSI, with a numeric sort condition on the GSI sort key.
	res, err := db.Query(ctx, driver.QueryInput{
		Table:     coll,
		IndexName: "by-assignee",
		KeyCondition: driver.KeyCondition{
			PartitionKey: "assignee", PartitionVal: "alice",
			SortOp: OpGreaterThan, SortVal: 2,
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.Count)
	assert.Equal(t, "t2", res.Items[0]["id"])

	// Describe / List.
	di, err := db.DescribeIndex(ctx, coll, "by-assignee")
	require.NoError(t, err)
	assert.Equal(t, "assignee", di.PartitionKey)
	assert.Equal(t, "priority", di.SortKey)
	assert.Equal(t, "ACTIVE", di.Status)

	list, err := db.ListIndexes(ctx, coll)
	require.NoError(t, err)
	require.Len(t, list, 1)

	// Delete, then Describe/Delete again → NotFound.
	require.NoError(t, db.DeleteIndex(ctx, coll, "by-assignee"))

	_, err = db.DescribeIndex(ctx, coll, "by-assignee")
	assert.True(t, cerrors.IsNotFound(err))
	err = db.DeleteIndex(ctx, coll, "by-assignee")
	assert.True(t, cerrors.IsNotFound(err))

	list, err = db.ListIndexes(ctx, coll)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestE2ECampaign_Labels(t *testing.T) {
	ctx := context.Background()
	m, _ := newE2EMock(t)
	db := asDatabase(m)

	const coll = "labeled"
	require.NoError(t, db.CreateTable(ctx, driver.TableConfig{Name: coll, PartitionKey: "id"}))

	// Fresh collection → empty label map.
	tags, err := db.ListTagsOfResource(ctx, coll)
	require.NoError(t, err)
	assert.Empty(t, tags)

	// Merge-overwrite semantics.
	require.NoError(t, db.TagResource(ctx, coll, map[string]string{"env": "dev", "team": "core"}))
	require.NoError(t, db.TagResource(ctx, coll, map[string]string{"env": "prod", "owner": "nitin"}))

	tags, err = db.ListTagsOfResource(ctx, coll)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"env": "prod", "team": "core", "owner": "nitin"}, tags)

	// Returned map is a copy — mutating it must not affect stored labels.
	tags["env"] = "mutated"
	tags2, err := db.ListTagsOfResource(ctx, coll)
	require.NoError(t, err)
	assert.Equal(t, "prod", tags2["env"])

	// Untag removes keys; unknown keys are ignored.
	require.NoError(t, db.UntagResource(ctx, coll, []string{"team", "does-not-exist"}))

	tags, err = db.ListTagsOfResource(ctx, coll)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"env": "prod", "owner": "nitin"}, tags)

	// Tagging a missing collection → NotFound.
	err = db.TagResource(ctx, "ghost", map[string]string{"a": "b"})
	assert.True(t, cerrors.IsNotFound(err))
	_, err = db.ListTagsOfResource(ctx, "ghost")
	assert.True(t, cerrors.IsNotFound(err))
}
