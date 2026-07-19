// Package dynamodb e2e suite tests: DATABASE / aws / portable driver API.
//
// These tests exercise realistic user journeys against the portable
// driver.Database API implemented by the DynamoDB mock. They intentionally
// assert the documented/observed contract of the mock (see survey), including
// a few divergences from real DynamoDB that are called out inline.
//
// NOTE on "conditional writes": the portable driver API has no
// ConditionExpression concept at all — PutItem is a blind upsert and there is
// no ConditionalCheckFailedException path. The closest conditional-write
// analogs at this layer are CreateTable/CreateIndex duplicate-name rejection
// (AlreadyExists) and UpdateItem-on-missing-item (NotFound), which are
// covered below alongside an explicit blind-upsert assertion.
package dynamodb

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// e2eEpoch is the deterministic start time for every fake clock in this file.
var e2eEpoch = time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

// newMock returns a mock wired to a fake clock plus the clock for advancing.
func newMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(e2eEpoch)
	return New(config.NewOptions(config.WithClock(fc))), fc
}

func e2eRequireNoErr(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func e2eRequireCode(t *testing.T, err error, want cerrors.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected %s error, got nil", want)
	}

	if got := cerrors.GetCode(err); got != want {
		t.Fatalf("expected error code %s, got %s (err=%v)", want, got, err)
	}
}

// TestLifecycle walks the full journey: create table -> put items
// with varied attribute types -> get -> query -> update -> "conditional"
// analogs -> batch ops -> delete items -> delete table.
func TestLifecycle(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	// Create table with composite key.
	cfg := driver.TableConfig{Name: "orders", PartitionKey: "pk", SortKey: "sk"}
	e2eRequireNoErr(t, m.CreateTable(ctx, cfg))

	t.Run("describe and list", func(t *testing.T) {
		desc, err := m.DescribeTable(ctx, "orders")
		e2eRequireNoErr(t, err)

		if desc.Name != "orders" || desc.PartitionKey != "pk" || desc.SortKey != "sk" {
			t.Fatalf("DescribeTable mismatch: %+v", desc)
		}

		// DescribeTable returns a copy — mutating it must not leak back.
		desc.PartitionKey = "hacked"
		desc2, err := m.DescribeTable(ctx, "orders")
		e2eRequireNoErr(t, err)

		if desc2.PartitionKey != "pk" {
			t.Fatalf("DescribeTable did not return a copy; got pk field %q", desc2.PartitionKey)
		}

		tables, err := m.ListTables(ctx)
		e2eRequireNoErr(t, err)

		if len(tables) != 1 || tables[0] != "orders" {
			t.Fatalf("ListTables = %v, want [orders]", tables)
		}
	})

	// Varied attribute types: string, empty string, float, int, bool, nil,
	// list, map, and a large-ish (64KB) value.
	large := strings.Repeat("x", 64*1024)
	item := map[string]any{
		"pk":      "user#1",
		"sk":      "order#001",
		"note":    "",
		"total":   99.5,
		"count":   3,
		"shipped": true,
		"coupon":  nil,
		"lines":   []any{"a", 2.0, false},
		"address": map[string]any{"city": "Berlin", "zip": "10115"},
		"blob":    large,
	}

	t.Run("put and get roundtrip", func(t *testing.T) {
		e2eRequireNoErr(t, m.PutItem(ctx, "orders", item))

		got, err := m.GetItem(ctx, "orders", map[string]any{"pk": "user#1", "sk": "order#001"})
		e2eRequireNoErr(t, err)

		if got["note"] != "" {
			t.Fatalf("empty string attr not preserved: %v", got["note"])
		}

		if got["total"] != 99.5 || got["shipped"] != true || got["coupon"] != nil {
			t.Fatalf("scalar attrs not preserved: %+v", got)
		}

		if len(got["blob"].(string)) != 64*1024 {
			t.Fatalf("large value not preserved, len=%d", len(got["blob"].(string)))
		}

		if got["address"].(map[string]any)["city"] != "Berlin" {
			t.Fatalf("nested map not preserved: %+v", got["address"])
		}

		if lines := got["lines"].([]any); len(lines) != 3 || lines[0] != "a" {
			t.Fatalf("list not preserved: %+v", got["lines"])
		}
	})

	t.Run("query by partition with sort condition", func(t *testing.T) {
		for i := 2; i <= 5; i++ {
			e2eRequireNoErr(t, m.PutItem(ctx, "orders", map[string]any{
				"pk": "user#1", "sk": fmt.Sprintf("order#%03d", i), "total": float64(i),
			}))
		}
		// Different partition — must not be returned.
		e2eRequireNoErr(t, m.PutItem(ctx, "orders", map[string]any{"pk": "user#2", "sk": "order#001"}))

		res, err := m.Query(ctx, driver.QueryInput{
			Table:        "orders",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "user#1"},
		})
		e2eRequireNoErr(t, err)

		if res.Count != 5 {
			t.Fatalf("pk-only query Count = %d, want 5", res.Count)
		}

		res, err = m.Query(ctx, driver.QueryInput{
			Table: "orders",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "user#1",
				SortOp: OpBeginsWith, SortVal: "order#00",
			},
		})
		e2eRequireNoErr(t, err)

		if res.Count != 5 {
			t.Fatalf("BEGINS_WITH query Count = %d, want 5", res.Count)
		}

		res, err = m.Query(ctx, driver.QueryInput{
			Table: "orders",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "user#1",
				SortOp: OpBetween, SortVal: "order#002", SortValEnd: "order#004",
			},
		})
		e2eRequireNoErr(t, err)

		if res.Count != 3 {
			t.Fatalf("BETWEEN query Count = %d, want 3", res.Count)
		}

		res, err = m.Query(ctx, driver.QueryInput{
			Table: "orders",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "user#1",
				SortOp: OpGreaterThan, SortVal: "order#003",
			},
		})
		e2eRequireNoErr(t, err)

		if res.Count != 2 {
			t.Fatalf("> query Count = %d, want 2", res.Count)
		}
	})

	t.Run("update SET and REMOVE", func(t *testing.T) {
		updated, err := m.UpdateItem(ctx, driver.UpdateItemInput{
			Table: "orders",
			Key:   map[string]any{"pk": "user#1", "sk": "order#001"},
			Actions: []driver.UpdateAction{
				{Action: "SET", Field: "status", Value: "SHIPPED"},
				{Action: "SET", Field: "total", Value: 120.0},
				{Action: "REMOVE", Field: "coupon"},
			},
		})
		e2eRequireNoErr(t, err)

		if updated["status"] != "SHIPPED" || updated["total"] != 120.0 {
			t.Fatalf("update result wrong: %+v", updated)
		}

		if _, present := updated["coupon"]; present {
			t.Fatalf("REMOVE did not delete field: %+v", updated)
		}

		got, err := m.GetItem(ctx, "orders", map[string]any{"pk": "user#1", "sk": "order#001"})
		e2eRequireNoErr(t, err)

		if got["status"] != "SHIPPED" {
			t.Fatalf("update not persisted: %+v", got)
		}

		// Unsupported action (ADD) is rejected with InvalidArgument.
		_, err = m.UpdateItem(ctx, driver.UpdateItemInput{
			Table:   "orders",
			Key:     map[string]any{"pk": "user#1", "sk": "order#001"},
			Actions: []driver.UpdateAction{{Action: "ADD", Field: "count", Value: 1}},
		})
		e2eRequireCode(t, err, cerrors.InvalidArgument)
	})

	t.Run("conditional write analogs", func(t *testing.T) {
		// Success analog: PutItem is an unconditional upsert; overwriting an
		// existing item never errors (no ConditionalCheckFailed at this layer).
		e2eRequireNoErr(t, m.PutItem(ctx, "orders", map[string]any{
			"pk": "user#1", "sk": "order#001", "total": 1.0,
		}))

		got, err := m.GetItem(ctx, "orders", map[string]any{"pk": "user#1", "sk": "order#001"})
		e2eRequireNoErr(t, err)

		if got["total"] != 1.0 {
			t.Fatalf("blind upsert did not replace item: %+v", got)
		}

		if _, present := got["status"]; present {
			t.Fatalf("PutItem should fully replace, old field survived: %+v", got)
		}

		// Failure analogs: duplicate table create and update-of-missing-item
		// are the typed-error "condition failed" paths the driver does have.
		e2eRequireCode(t, m.CreateTable(ctx, cfg), cerrors.AlreadyExists)

		_, err = m.UpdateItem(ctx, driver.UpdateItemInput{
			Table:   "orders",
			Key:     map[string]any{"pk": "ghost", "sk": "none"},
			Actions: []driver.UpdateAction{{Action: "SET", Field: "a", Value: 1}},
		})
		e2eRequireCode(t, err, cerrors.NotFound)
	})

	t.Run("batch put and batch get", func(t *testing.T) {
		batch := make([]map[string]any, 0, 4)
		for i := 1; i <= 4; i++ {
			batch = append(batch, map[string]any{
				"pk": "batch#1", "sk": fmt.Sprintf("b#%d", i), "n": float64(i),
			})
		}
		e2eRequireNoErr(t, m.BatchPutItems(ctx, "orders", batch))

		got, err := m.BatchGetItems(ctx, "orders", []map[string]any{
			{"pk": "batch#1", "sk": "b#1"},
			{"pk": "batch#1", "sk": "b#3"},
			{"pk": "batch#1", "sk": "b#999"}, // missing — silently skipped
		})
		e2eRequireNoErr(t, err)

		if len(got) != 2 {
			t.Fatalf("BatchGetItems returned %d items, want 2 (missing keys skipped)", len(got))
		}
	})

	t.Run("delete items then table", func(t *testing.T) {
		key := map[string]any{"pk": "user#1", "sk": "order#001"}
		e2eRequireNoErr(t, m.DeleteItem(ctx, "orders", key))

		_, err := m.GetItem(ctx, "orders", key)
		e2eRequireCode(t, err, cerrors.NotFound)

		// Idempotent: deleting again is not an error.
		e2eRequireNoErr(t, m.DeleteItem(ctx, "orders", key))

		e2eRequireNoErr(t, m.DeleteTable(ctx, "orders"))
		_, err = m.DescribeTable(ctx, "orders")
		e2eRequireCode(t, err, cerrors.NotFound)
	})
}

// TestEdgeCases covers typed errors for missing resources,
// duplicate creates, and queries on empty tables.
func TestEdgeCases(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	t.Run("operations on nonexistent table", func(t *testing.T) {
		_, err := m.GetItem(ctx, "nope", map[string]any{"pk": "x"})
		e2eRequireCode(t, err, cerrors.NotFound)

		e2eRequireCode(t, m.PutItem(ctx, "nope", map[string]any{"pk": "x"}), cerrors.NotFound)
		e2eRequireCode(t, m.DeleteItem(ctx, "nope", map[string]any{"pk": "x"}), cerrors.NotFound)
		e2eRequireCode(t, m.DeleteTable(ctx, "nope"), cerrors.NotFound)

		_, err = m.Query(ctx, driver.QueryInput{
			Table:        "nope",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "x"},
		})
		e2eRequireCode(t, err, cerrors.NotFound)

		_, err = m.Scan(ctx, driver.ScanInput{Table: "nope"})
		e2eRequireCode(t, err, cerrors.NotFound)

		_, err = m.BatchGetItems(ctx, "nope", nil)
		e2eRequireCode(t, err, cerrors.NotFound)

		e2eRequireCode(t, m.BatchPutItems(ctx, "nope", nil), cerrors.NotFound)
		e2eRequireCode(t, m.TransactWriteItems(ctx, "nope", nil, nil), cerrors.NotFound)
		e2eRequireCode(t, m.UpdateTTL(ctx, "nope", driver.TTLConfig{}), cerrors.NotFound)

		_, err = m.GetStreamRecords(ctx, "nope", 10, "")
		e2eRequireCode(t, err, cerrors.NotFound)
	})

	e2eRequireNoErr(t, m.CreateTable(ctx, driver.TableConfig{Name: "empty", PartitionKey: "pk"}))

	t.Run("duplicate table create", func(t *testing.T) {
		err := m.CreateTable(ctx, driver.TableConfig{Name: "empty", PartitionKey: "other"})
		e2eRequireCode(t, err, cerrors.AlreadyExists)
	})

	t.Run("query and scan on empty table", func(t *testing.T) {
		res, err := m.Query(ctx, driver.QueryInput{
			Table:        "empty",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "anything"},
		})
		e2eRequireNoErr(t, err)

		if res.Count != 0 || len(res.Items) != 0 || res.NextPageToken != "" {
			t.Fatalf("empty-table query = %+v, want empty result", res)
		}

		sres, err := m.Scan(ctx, driver.ScanInput{Table: "empty"})
		e2eRequireNoErr(t, err)

		if sres.Count != 0 {
			t.Fatalf("empty-table scan Count = %d, want 0", sres.Count)
		}
	})

	t.Run("get and delete missing item", func(t *testing.T) {
		_, err := m.GetItem(ctx, "empty", map[string]any{"pk": "missing"})
		e2eRequireCode(t, err, cerrors.NotFound)

		// Missing item delete is idempotent success.
		e2eRequireNoErr(t, m.DeleteItem(ctx, "empty", map[string]any{"pk": "missing"}))
	})

	t.Run("query unknown index", func(t *testing.T) {
		_, err := m.Query(ctx, driver.QueryInput{
			Table:        "empty",
			IndexName:    "no-such-index",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "x"},
		})
		e2eRequireCode(t, err, cerrors.NotFound)
	})
}

// TestPagination pushes 30 items through token-based pagination on
// both Scan and Query and verifies completeness (no missed or duplicated
// items across pages).
func TestPagination(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	e2eRequireNoErr(t, m.CreateTable(ctx, driver.TableConfig{
		Name: "pages", PartitionKey: "pk", SortKey: "sk",
	}))

	const total = 30

	items := make([]map[string]any, 0, total)
	for i := 0; i < total; i++ {
		items = append(items, map[string]any{
			"pk": "tenant#1", "sk": fmt.Sprintf("item#%03d", i), "n": float64(i),
		})
	}
	e2eRequireNoErr(t, m.BatchPutItems(ctx, "pages", items))

	t.Run("single page fetches all", func(t *testing.T) {
		res, err := m.Scan(ctx, driver.ScanInput{Table: "pages", Limit: 100})
		e2eRequireNoErr(t, err)

		if res.Count != total {
			t.Fatalf("Scan Count = %d, want %d", res.Count, total)
		}

		if res.NextPageToken != "" {
			t.Fatalf("expected no NextPageToken when everything fits, got %q", res.NextPageToken)
		}
	})

	t.Run("scan paginates without dupes or misses", func(t *testing.T) {
		seen := make(map[string]int)
		token := ""
		pages := 0

		for {
			res, err := m.Scan(ctx, driver.ScanInput{Table: "pages", Limit: 10, PageToken: token})
			e2eRequireNoErr(t, err)

			pages++
			if len(res.Items) > 10 {
				t.Fatalf("page %d exceeded limit: %d items", pages, len(res.Items))
			}

			for _, it := range res.Items {
				seen[fmt.Sprintf("%v", it["sk"])]++
			}

			if res.NextPageToken == "" {
				break
			}

			token = res.NextPageToken

			if pages > 10 {
				t.Fatalf("pagination did not terminate after %d pages", pages)
			}
		}

		if pages < 3 {
			t.Fatalf("expected at least 3 pages of 10 for 30 items, got %d", pages)
		}

		var dupes []string

		for k, c := range seen {
			if c > 1 {
				dupes = append(dupes, k)
			}
		}

		if len(dupes) > 0 {
			t.Errorf("scan pagination returned duplicate items: %v", dupes)
		}

		if len(seen) != total {
			t.Errorf("scan pagination returned %d unique items, want %d (items missed)", len(seen), total)
		}
	})

	t.Run("query paginates without dupes or misses", func(t *testing.T) {
		seen := make(map[string]int)
		token := ""
		pages := 0

		for {
			res, err := m.Query(ctx, driver.QueryInput{
				Table:        "pages",
				KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "tenant#1"},
				Limit:        10,
				PageToken:    token,
			})
			e2eRequireNoErr(t, err)

			pages++
			for _, it := range res.Items {
				seen[fmt.Sprintf("%v", it["sk"])]++
			}

			if res.NextPageToken == "" {
				break
			}

			token = res.NextPageToken

			if pages > 10 {
				t.Fatalf("pagination did not terminate after %d pages", pages)
			}
		}

		var dupes []string

		for k, c := range seen {
			if c > 1 {
				dupes = append(dupes, k)
			}
		}

		if len(dupes) > 0 {
			t.Errorf("query pagination returned duplicate items: %v", dupes)
		}

		if len(seen) != total {
			t.Errorf("query pagination returned %d unique items, want %d (items missed)", len(seen), total)
		}
	})
}

// TestUnicode uses non-ASCII partition/sort keys and values.
func TestUnicode(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	e2eRequireNoErr(t, m.CreateTable(ctx, driver.TableConfig{
		Name: "unicode", PartitionKey: "pk", SortKey: "sk",
	}))

	item := map[string]any{
		"pk":   "用户#münchen",
		"sk":   "范围🎉",
		"name": "Ünïcodé — Ω≈ç√ 🚀",
		"city": "Zürich",
	}
	e2eRequireNoErr(t, m.PutItem(ctx, "unicode", item))

	got, err := m.GetItem(ctx, "unicode", map[string]any{"pk": "用户#münchen", "sk": "范围🎉"})
	e2eRequireNoErr(t, err)

	if got["name"] != "Ünïcodé — Ω≈ç√ 🚀" {
		t.Fatalf("unicode value not preserved: %v", got["name"])
	}

	res, err := m.Query(ctx, driver.QueryInput{
		Table: "unicode",
		KeyCondition: driver.KeyCondition{
			PartitionKey: "pk", PartitionVal: "用户#münchen",
			SortOp: OpBeginsWith, SortVal: "范围",
		},
	})
	e2eRequireNoErr(t, err)

	if res.Count != 1 {
		t.Fatalf("unicode BEGINS_WITH query Count = %d, want 1", res.Count)
	}

	sres, err := m.Scan(ctx, driver.ScanInput{
		Table:   "unicode",
		Filters: []driver.ScanFilter{{Field: "city", Op: OpContains, Value: "üri"}},
	})
	e2eRequireNoErr(t, err)

	if sres.Count != 1 {
		t.Fatalf("unicode CONTAINS scan Count = %d, want 1", sres.Count)
	}

	e2eRequireNoErr(t, m.DeleteItem(ctx, "unicode", map[string]any{"pk": "用户#münchen", "sk": "范围🎉"}))

	_, err = m.GetItem(ctx, "unicode", map[string]any{"pk": "用户#münchen", "sk": "范围🎉"})
	e2eRequireCode(t, err, cerrors.NotFound)
}

// TestTTL exercises lazy TTL expiry deterministically via the fake
// clock: config, pre-expiry reads, post-expiry lazy deletion on Get/Query/Scan,
// boundary semantics, and the documented BatchGetItems TTL gap.
func TestTTL(t *testing.T) {
	ctx := context.Background()
	m, fc := newMock()

	e2eRequireNoErr(t, m.CreateTable(ctx, driver.TableConfig{Name: "sessions", PartitionKey: "pk"}))

	e2eRequireNoErr(t, m.UpdateTTL(ctx, "sessions", driver.TTLConfig{
		Enabled: true, AttributeName: "expiresAt",
	}))

	ttlCfg, err := m.DescribeTTL(ctx, "sessions")
	e2eRequireNoErr(t, err)

	if !ttlCfg.Enabled || ttlCfg.AttributeName != "expiresAt" {
		t.Fatalf("DescribeTTL = %+v", ttlCfg)
	}

	expiry := e2eEpoch.Add(60 * time.Second).Unix()

	// Three flavors of TTL values: int64, numeric string, and no TTL attr.
	e2eRequireNoErr(t, m.PutItem(ctx, "sessions", map[string]any{"pk": "s1", "expiresAt": expiry}))
	e2eRequireNoErr(t, m.PutItem(ctx, "sessions", map[string]any{
		"pk": "s2", "expiresAt": fmt.Sprintf("%d", expiry),
	}))
	e2eRequireNoErr(t, m.PutItem(ctx, "sessions", map[string]any{"pk": "s3"})) // immortal

	t.Run("before expiry all visible", func(t *testing.T) {
		for _, pk := range []string{"s1", "s2", "s3"} {
			_, err := m.GetItem(ctx, "sessions", map[string]any{"pk": pk})
			e2eRequireNoErr(t, err)
		}
	})

	t.Run("exactly at expiry still visible", func(t *testing.T) {
		fc.Set(time.Unix(expiry, 0)) // Now().Unix() == ttl, not > ttl

		_, err := m.GetItem(ctx, "sessions", map[string]any{"pk": "s1"})
		e2eRequireNoErr(t, err)
	})

	t.Run("after expiry lazily deleted", func(t *testing.T) {
		fc.Set(time.Unix(expiry+1, 0))

		_, err := m.GetItem(ctx, "sessions", map[string]any{"pk": "s1"})
		e2eRequireCode(t, err, cerrors.NotFound)

		// GetItem physically deleted s1: rewinding the clock cannot revive it.
		fc.Set(e2eEpoch)

		_, err = m.GetItem(ctx, "sessions", map[string]any{"pk": "s1"})
		e2eRequireCode(t, err, cerrors.NotFound)

		fc.Set(time.Unix(expiry+1, 0))
	})

	t.Run("scan and query skip expired", func(t *testing.T) {
		sres, err := m.Scan(ctx, driver.ScanInput{Table: "sessions"})
		e2eRequireNoErr(t, err)

		if sres.Count != 1 { // only s3 survives (s2 numeric-string TTL also expired)
			t.Fatalf("post-expiry scan Count = %d, want 1: %+v", sres.Count, sres.Items)
		}

		qres, err := m.Query(ctx, driver.QueryInput{
			Table:        "sessions",
			KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "s3"},
		})
		e2eRequireNoErr(t, err)

		if qres.Count != 1 {
			t.Fatalf("query for immortal item Count = %d, want 1", qres.Count)
		}
	})

	t.Run("batch get skips TTL check (documented gap)", func(t *testing.T) {
		// Fresh expired item that no lazy path has deleted yet.
		e2eRequireNoErr(t, m.PutItem(ctx, "sessions", map[string]any{
			"pk": "s4", "expiresAt": expiry, // already in the past
		}))

		got, err := m.BatchGetItems(ctx, "sessions", []map[string]any{{"pk": "s4"}})
		e2eRequireNoErr(t, err)

		// Divergence from GetItem: BatchGetItems does NOT apply TTL filtering.
		if len(got) != 1 {
			t.Fatalf("BatchGetItems returned %d items; documented behavior is 1 (no TTL check)", len(got))
		}

		// But a point GetItem on the same key reaps it.
		_, err = m.GetItem(ctx, "sessions", map[string]any{"pk": "s4"})
		e2eRequireCode(t, err, cerrors.NotFound)
	})
}

// TestStreams covers the change-stream journey: enable, generate
// INSERT/MODIFY/REMOVE events, image capture per view type, sequence tokens,
// limits, and the FailedPrecondition guard.
func TestStreams(t *testing.T) {
	ctx := context.Background()
	m, fc := newMock()

	e2eRequireNoErr(t, m.CreateTable(ctx, driver.TableConfig{Name: "events", PartitionKey: "pk"}))

	t.Run("streams disabled -> FailedPrecondition", func(t *testing.T) {
		_, err := m.GetStreamRecords(ctx, "events", 10, "")
		e2eRequireCode(t, err, cerrors.FailedPrecondition)
	})

	e2eRequireNoErr(t, m.UpdateStreamConfig(ctx, "events", driver.StreamConfig{
		Enabled: true, ViewType: ViewNewAndOld,
	}))

	// Writes before enabling produce no events; these four all do.
	e2eRequireNoErr(t, m.PutItem(ctx, "events", map[string]any{"pk": "e1", "v": 1})) // INSERT
	fc.Advance(time.Second)
	e2eRequireNoErr(t, m.PutItem(ctx, "events", map[string]any{"pk": "e1", "v": 2})) // MODIFY
	_, err := m.UpdateItem(ctx, driver.UpdateItemInput{                              // MODIFY
		Table:   "events",
		Key:     map[string]any{"pk": "e1"},
		Actions: []driver.UpdateAction{{Action: "SET", Field: "v", Value: 3}},
	})
	e2eRequireNoErr(t, err)
	e2eRequireNoErr(t, m.DeleteItem(ctx, "events", map[string]any{"pk": "e1"})) // REMOVE
	e2eRequireNoErr(t, m.DeleteItem(ctx, "events", map[string]any{"pk": "e1"})) // no event (miss)

	t.Run("event sequence and images", func(t *testing.T) {
		it, err := m.GetStreamRecords(ctx, "events", 100, "")
		e2eRequireNoErr(t, err)

		if it.ShardID != "shard-000" {
			t.Fatalf("ShardID = %q, want shard-000", it.ShardID)
		}

		if len(it.Records) != 4 {
			t.Fatalf("got %d stream records, want 4: %+v", len(it.Records), it.Records)
		}

		wantTypes := []string{"INSERT", "MODIFY", "MODIFY", "REMOVE"}
		for i, r := range it.Records {
			if r.EventType != wantTypes[i] {
				t.Fatalf("record %d EventType = %s, want %s", i, r.EventType, wantTypes[i])
			}

			if r.SequenceNumber != fmt.Sprintf("%d", i+1) || r.EventID != fmt.Sprintf("event-%d", i+1) {
				t.Fatalf("record %d seq/id = %s/%s", i, r.SequenceNumber, r.EventID)
			}
		}

		if it.Records[0].OldImage != nil || it.Records[0].NewImage["v"] != 1 {
			t.Fatalf("INSERT images wrong: %+v", it.Records[0])
		}

		if it.Records[1].OldImage["v"] != 1 || it.Records[1].NewImage["v"] != 2 {
			t.Fatalf("MODIFY images wrong: %+v", it.Records[1])
		}

		if it.Records[3].NewImage != nil || it.Records[3].OldImage["v"] != 3 {
			t.Fatalf("REMOVE images wrong: %+v", it.Records[3])
		}

		// Timestamps come from the fake clock: first record at epoch,
		// the rest 1s later.
		if !it.Records[0].Timestamp.Equal(e2eEpoch) {
			t.Fatalf("record 0 timestamp = %v, want %v", it.Records[0].Timestamp, e2eEpoch)
		}

		if !it.Records[1].Timestamp.Equal(e2eEpoch.Add(time.Second)) {
			t.Fatalf("record 1 timestamp = %v", it.Records[1].Timestamp)
		}
	})

	t.Run("token resumption and limits", func(t *testing.T) {
		it, err := m.GetStreamRecords(ctx, "events", 2, "")
		e2eRequireNoErr(t, err)

		if len(it.Records) != 2 || it.NextToken != "2" {
			t.Fatalf("limited read = %d records, token %q; want 2, \"2\"", len(it.Records), it.NextToken)
		}

		it, err = m.GetStreamRecords(ctx, "events", 100, it.NextToken)
		e2eRequireNoErr(t, err)

		if len(it.Records) != 2 || it.Records[0].SequenceNumber != "3" || it.NextToken != "" {
			t.Fatalf("resumed read wrong: %+v", it)
		}
	})

	t.Run("keys only view", func(t *testing.T) {
		e2eRequireNoErr(t, m.CreateTable(ctx, driver.TableConfig{Name: "keysonly", PartitionKey: "pk"}))
		e2eRequireNoErr(t, m.UpdateStreamConfig(ctx, "keysonly", driver.StreamConfig{
			Enabled: true, ViewType: ViewKeysOnly,
		}))
		e2eRequireNoErr(t, m.PutItem(ctx, "keysonly", map[string]any{"pk": "k1", "secret": "hidden"}))

		it, err := m.GetStreamRecords(ctx, "keysonly", 10, "")
		e2eRequireNoErr(t, err)

		if len(it.Records) != 1 {
			t.Fatalf("got %d records, want 1", len(it.Records))
		}

		r := it.Records[0]
		if r.NewImage != nil || r.OldImage != nil {
			t.Fatalf("KEYS_ONLY captured images: %+v", r)
		}

		if r.Keys["pk"] != "k1" {
			t.Fatalf("Keys wrong: %+v", r.Keys)
		}
	})
}

// TestScanFilters covers AND-combined scan filters including
// numeric vs lexicographic comparison behavior.
func TestScanFilters(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	e2eRequireNoErr(t, m.CreateTable(ctx, driver.TableConfig{Name: "products", PartitionKey: "pk"}))

	products := []map[string]any{
		{"pk": "p1", "name": "widget-small", "price": 5, "active": true},
		{"pk": "p2", "name": "widget-large", "price": 25, "active": true},
		{"pk": "p3", "name": "gadget", "price": 100, "active": false},
		{"pk": "p4", "name": "widget-medium", "price": 10, "active": true},
	}
	e2eRequireNoErr(t, m.BatchPutItems(ctx, "products", products))

	t.Run("numeric comparison not lexicographic", func(t *testing.T) {
		// price > 9 must match 25, 100, 10 (lexicographic would drop "10" and "100").
		res, err := m.Scan(ctx, driver.ScanInput{
			Table:   "products",
			Filters: []driver.ScanFilter{{Field: "price", Op: OpGreaterThan, Value: 9}},
		})
		e2eRequireNoErr(t, err)

		if res.Count != 3 {
			t.Fatalf("price > 9 Count = %d, want 3: %+v", res.Count, res.Items)
		}
	})

	t.Run("AND combined filters", func(t *testing.T) {
		res, err := m.Scan(ctx, driver.ScanInput{
			Table: "products",
			Filters: []driver.ScanFilter{
				{Field: "name", Op: OpBeginsWith, Value: "widget"},
				{Field: "price", Op: OpLessEqual, Value: 10},
				{Field: "active", Op: OpEqual, Value: true},
			},
		})
		e2eRequireNoErr(t, err)

		if res.Count != 2 { // p1 (5) and p4 (10)
			t.Fatalf("combined filter Count = %d, want 2: %+v", res.Count, res.Items)
		}
	})

	t.Run("not equal and contains", func(t *testing.T) {
		res, err := m.Scan(ctx, driver.ScanInput{
			Table: "products",
			Filters: []driver.ScanFilter{
				{Field: "name", Op: OpContains, Value: "dget"},
				{Field: "active", Op: OpNotEqual, Value: true},
			},
		})
		e2eRequireNoErr(t, err)

		if res.Count != 1 { // only gadget
			t.Fatalf("contains+ne Count = %d, want 1: %+v", res.Count, res.Items)
		}
	})
}

// TestNumericSortAndKeyCollision documents two key-handling
// behaviors from the survey: numeric-aware sort comparisons on Query, and the
// %v-format key collision between numeric 25 and string "25".
func TestNumericSortAndKeyCollision(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	t.Run("numeric sort key comparison", func(t *testing.T) {
		e2eRequireNoErr(t, m.CreateTable(ctx, driver.TableConfig{
			Name: "scores", PartitionKey: "pk", SortKey: "score",
		}))

		for _, s := range []int{1, 2, 5, 10, 25} {
			e2eRequireNoErr(t, m.PutItem(ctx, "scores", map[string]any{"pk": "game#1", "score": s}))
		}

		res, err := m.Query(ctx, driver.QueryInput{
			Table: "scores",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "game#1",
				SortOp: OpGreaterThan, SortVal: 2,
			},
		})
		e2eRequireNoErr(t, err)

		if res.Count != 3 { // 5, 10, 25 — numeric compare, not lexicographic
			t.Fatalf("score > 2 Count = %d, want 3: %+v", res.Count, res.Items)
		}

		res, err = m.Query(ctx, driver.QueryInput{
			Table: "scores",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "pk", PartitionVal: "game#1",
				SortOp: OpBetween, SortVal: 2, SortValEnd: 10,
			},
		})
		e2eRequireNoErr(t, err)

		if res.Count != 3 { // 2, 5, 10 inclusive
			t.Fatalf("BETWEEN 2 AND 10 Count = %d, want 3", res.Count)
		}
	})

	t.Run("numeric and string pk collide via %v formatting", func(t *testing.T) {
		e2eRequireNoErr(t, m.CreateTable(ctx, driver.TableConfig{Name: "collide", PartitionKey: "pk"}))

		e2eRequireNoErr(t, m.PutItem(ctx, "collide", map[string]any{"pk": 25, "src": "int"}))
		e2eRequireNoErr(t, m.PutItem(ctx, "collide", map[string]any{"pk": "25", "src": "string"}))

		// Documented mock behavior: both key as "25", second write wins.
		res, err := m.Scan(ctx, driver.ScanInput{Table: "collide"})
		e2eRequireNoErr(t, err)

		if res.Count != 1 {
			t.Fatalf("expected key collision to leave 1 item, got %d", res.Count)
		}

		got, err := m.GetItem(ctx, "collide", map[string]any{"pk": 25})
		e2eRequireNoErr(t, err)

		if got["src"] != "string" {
			t.Fatalf("expected last write to win on colliding key, got %+v", got)
		}
	})
}

// TestGSILifecycle covers index create/query/describe/list/delete
// plus typed errors.
func TestGSILifecycle(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	e2eRequireNoErr(t, m.CreateTable(ctx, driver.TableConfig{
		Name: "users", PartitionKey: "pk",
	}))

	info, err := m.CreateIndex(ctx, "users", driver.GSIConfig{
		Name: "by-email", PartitionKey: "email",
	})
	e2eRequireNoErr(t, err)

	if info.Status != "ACTIVE" || info.PartitionKey != "email" {
		t.Fatalf("CreateIndex info = %+v", info)
	}

	t.Run("index typed errors", func(t *testing.T) {
		_, err := m.CreateIndex(ctx, "users", driver.GSIConfig{Name: "by-email", PartitionKey: "email"})
		e2eRequireCode(t, err, cerrors.AlreadyExists)

		_, err = m.CreateIndex(ctx, "users", driver.GSIConfig{Name: "", PartitionKey: "x"})
		e2eRequireCode(t, err, cerrors.InvalidArgument)

		_, err = m.DescribeIndex(ctx, "users", "nope")
		e2eRequireCode(t, err, cerrors.NotFound)

		e2eRequireCode(t, m.DeleteIndex(ctx, "users", "nope"), cerrors.NotFound)
	})

	t.Run("query via GSI", func(t *testing.T) {
		e2eRequireNoErr(t, m.BatchPutItems(ctx, "users", []map[string]any{
			{"pk": "u1", "email": "a@example.com"},
			{"pk": "u2", "email": "b@example.com"},
			{"pk": "u3", "email": "a@example.com"},
		}))

		res, err := m.Query(ctx, driver.QueryInput{
			Table:     "users",
			IndexName: "by-email",
			KeyCondition: driver.KeyCondition{
				PartitionKey: "email", PartitionVal: "a@example.com",
			},
		})
		e2eRequireNoErr(t, err)

		if res.Count != 2 {
			t.Fatalf("GSI query Count = %d, want 2", res.Count)
		}
	})

	t.Run("list and delete index", func(t *testing.T) {
		idxs, err := m.ListIndexes(ctx, "users")
		e2eRequireNoErr(t, err)

		if len(idxs) != 1 || idxs[0].Name != "by-email" || idxs[0].Status != "ACTIVE" {
			t.Fatalf("ListIndexes = %+v", idxs)
		}

		e2eRequireNoErr(t, m.DeleteIndex(ctx, "users", "by-email"))

		_, err = m.Query(ctx, driver.QueryInput{
			Table:        "users",
			IndexName:    "by-email",
			KeyCondition: driver.KeyCondition{PartitionKey: "email", PartitionVal: "a@example.com"},
		})
		e2eRequireCode(t, err, cerrors.NotFound)
	})
}

// TestTransactAndTags covers TransactWriteItems ordering (puts
// before deletes) and the tagging lifecycle.
func TestTransactAndTags(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	e2eRequireNoErr(t, m.CreateTable(ctx, driver.TableConfig{Name: "tx", PartitionKey: "pk"}))
	e2eRequireNoErr(t, m.PutItem(ctx, "tx", map[string]any{"pk": "old", "v": 0}))

	t.Run("transact puts then deletes", func(t *testing.T) {
		err := m.TransactWriteItems(ctx, "tx",
			[]map[string]any{
				{"pk": "a", "v": 1},
				{"pk": "b", "v": 2},
				{"pk": "c", "v": 3}, // put then deleted in the same transaction
			},
			[]map[string]any{
				{"pk": "old"},
				{"pk": "c"},
			},
		)
		e2eRequireNoErr(t, err)

		res, err := m.Scan(ctx, driver.ScanInput{Table: "tx"})
		e2eRequireNoErr(t, err)

		if res.Count != 2 { // a, b survive; old and c deleted
			t.Fatalf("post-transact Count = %d, want 2: %+v", res.Count, res.Items)
		}

		_, err = m.GetItem(ctx, "tx", map[string]any{"pk": "c"})
		e2eRequireCode(t, err, cerrors.NotFound)

		_, err = m.GetItem(ctx, "tx", map[string]any{"pk": "old"})
		e2eRequireCode(t, err, cerrors.NotFound)
	})

	t.Run("tag lifecycle", func(t *testing.T) {
		e2eRequireNoErr(t, m.TagResource(ctx, "tx", map[string]string{"env": "dev", "team": "core"}))
		e2eRequireNoErr(t, m.TagResource(ctx, "tx", map[string]string{"env": "prod"})) // merge-overwrite

		tags, err := m.ListTagsOfResource(ctx, "tx")
		e2eRequireNoErr(t, err)

		if tags["env"] != "prod" || tags["team"] != "core" || len(tags) != 2 {
			t.Fatalf("tags after merge = %v", tags)
		}

		// Returned map is a copy.
		tags["env"] = "hacked"
		tags2, err := m.ListTagsOfResource(ctx, "tx")
		e2eRequireNoErr(t, err)

		if tags2["env"] != "prod" {
			t.Fatalf("ListTagsOfResource leaked internal map: %v", tags2)
		}

		// Unknown keys ignored on untag.
		e2eRequireNoErr(t, m.UntagResource(ctx, "tx", []string{"team", "does-not-exist"}))

		tags3, err := m.ListTagsOfResource(ctx, "tx")
		e2eRequireNoErr(t, err)

		if len(tags3) != 1 || tags3["env"] != "prod" {
			t.Fatalf("tags after untag = %v", tags3)
		}

		e2eRequireCode(t, m.TagResource(ctx, "ghost", map[string]string{"a": "b"}), cerrors.NotFound)

		_, err = m.ListTagsOfResource(ctx, "ghost")
		e2eRequireCode(t, err, cerrors.NotFound)
	})
}
