package tablestorage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	driver "github.com/stackshy/cloudemu/v2/services/tablestorage/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(clk), config.WithAccountID("acct")))
}

func seed(t *testing.T, m *Mock, table, pk, rk string, extra driver.Entity) {
	t.Helper()

	e := driver.Entity{"PartitionKey": pk, "RowKey": rk}
	for k, v := range extra {
		e[k] = v
	}

	if _, err := m.InsertEntity(context.Background(), table, pk, rk, e); err != nil {
		t.Fatalf("InsertEntity %s/%s: %v", pk, rk, err)
	}
}

func queryRowKeys(t *testing.T, m *Mock, table, filter string) []string {
	t.Helper()

	res, err := m.QueryEntities(context.Background(), table, driver.QueryOptions{Filter: filter})
	if err != nil {
		t.Fatalf("QueryEntities %q: %v", filter, err)
	}

	rks := make([]string, 0, len(res.Entities))
	for _, e := range res.Entities {
		rks = append(rks, e["RowKey"].(string))
	}

	return rks
}

func TestFilterGrammar(t *testing.T) {
	m := newMock(t)
	if err := m.CreateTable(context.Background(), "t"); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	seed(t, m, "t", "p", "a", driver.Entity{"Age": 30.0, "Active": true, "Created": "2020-06-01T00:00:00Z"})
	seed(t, m, "t", "p", "b", driver.Entity{"Age": 20.0, "Active": false, "Created": "2019-01-01T00:00:00Z"})
	seed(t, m, "t", "p", "c", driver.Entity{"Age": 25.0, "Active": true, "Created": "2021-01-01T00:00:00Z"})

	cases := []struct {
		filter string
		want   []string
	}{
		{"Age gt 25", []string{"a"}},
		{"Age ge 25 and Active eq true", []string{"a", "c"}},
		{"Active eq false", []string{"b"}},
		{"not (Age eq 25)", []string{"a", "b"}},
		{"Age lt 25 or Age gt 25", []string{"a", "b"}},
		{"Created gt datetime'2020-01-01T00:00:00Z'", []string{"a", "c"}},
		{"RowKey ge 'b'", []string{"b", "c"}},
	}

	for _, tc := range cases {
		got := queryRowKeys(t, m, "t", tc.filter)
		if !sameSet(got, tc.want) {
			t.Errorf("filter %q = %v, want %v", tc.filter, got, tc.want)
		}
	}
}

func TestFilterMalformedRejected(t *testing.T) {
	m := newMock(t)
	_ = m.CreateTable(context.Background(), "t")

	for _, bad := range []string{"Age gt", "Age foo 3", "(Age eq 1", "eq 3"} {
		if _, err := m.QueryEntities(context.Background(), "t", driver.QueryOptions{Filter: bad}); err == nil {
			t.Errorf("filter %q accepted, want InvalidArgument", bad)
		} else if !cerrors.IsInvalidArgument(err) {
			t.Errorf("filter %q error = %v, want InvalidArgument", bad, err)
		}
	}
}

func TestConditionalUpdateIfMatch(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	_ = m.CreateTable(ctx, "t")
	seed(t, m, "t", "p", "r", driver.Entity{"V": 1.0})

	ent, err := m.GetEntity(ctx, "t", "p", "r")
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}

	etag := ent["odata.etag"].(string)

	// Stale/incorrect ETag → FailedPrecondition.
	_, err = m.UpdateEntity(ctx, "t", "p", "r", driver.Entity{"V": 2.0}, driver.UpdateModeReplace, "W/\"stale\"")
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("stale If-Match error = %v, want FailedPrecondition", err)
	}

	// Correct ETag → succeeds and rotates the ETag.
	newETag, err := m.UpdateEntity(ctx, "t", "p", "r", driver.Entity{"V": 2.0}, driver.UpdateModeReplace, etag)
	if err != nil {
		t.Fatalf("conditional update with current ETag: %v", err)
	}

	if newETag == etag {
		t.Error("ETag did not change after update")
	}
}

func TestApplyBatchAtomicRollback(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	_ = m.CreateTable(ctx, "t")
	seed(t, m, "t", "p", "dup", nil)

	ops := []driver.BatchOp{
		{Type: driver.BatchInsert, PartitionKey: "p", RowKey: "fresh", Entity: driver.Entity{"PartitionKey": "p", "RowKey": "fresh"}},
		{Type: driver.BatchInsert, PartitionKey: "p", RowKey: "dup", Entity: driver.Entity{"PartitionKey": "p", "RowKey": "dup"}},
	}

	_, err := m.ApplyBatch(ctx, "t", ops)
	if err == nil {
		t.Fatal("ApplyBatch with a conflicting insert succeeded, want an error")
	}

	var batchErr *driver.BatchError
	if !errors.As(err, &batchErr) || batchErr.Index != 1 {
		t.Fatalf("ApplyBatch error = %v, want BatchError at index 1", err)
	}

	// The first insert must have been rolled back.
	if _, err := m.GetEntity(ctx, "t", "p", "fresh"); !cerrors.IsNotFound(err) {
		t.Errorf("entity 'fresh' present after rollback (err=%v), want NotFound", err)
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}

	for _, s := range b {
		seen[s]--
	}

	for _, n := range seen {
		if n != 0 {
			return false
		}
	}

	return true
}

// TestInt64TypedStoreFilter confirms an Edm.Int64 arriving as a wire string with
// its @odata.type companion is stored as a native int64 so $filter compares it
// numerically, and that the companion survives a $select projection.
func TestInt64TypedStoreFilter(t *testing.T) {
	m := newMock(t)
	if err := m.CreateTable(context.Background(), "t"); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	seed(t, m, "t", "p", "big", driver.Entity{"Amount": "500", "Amount@odata.type": edmInt64})
	seed(t, m, "t", "p", "small", driver.Entity{"Amount": "50", "Amount@odata.type": edmInt64})

	got := queryRowKeys(t, m, "t", "Amount gt 100")
	if !sameSet(got, []string{"big"}) {
		t.Errorf("Amount gt 100 = %v, want [big]", got)
	}

	ent, err := m.GetEntity(context.Background(), "t", "p", "big")
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}

	if n, ok := ent["Amount"].(int64); !ok || n != 500 {
		t.Errorf("stored Amount = %v (%T), want int64(500)", ent["Amount"], ent["Amount"])
	}

	res, err := m.QueryEntities(context.Background(), "t", driver.QueryOptions{
		Filter: "PartitionKey eq 'p'",
		Select: "PartitionKey,RowKey,Amount",
	})
	if err != nil {
		t.Fatalf("QueryEntities: %v", err)
	}

	for _, e := range res.Entities {
		if e["Amount@odata.type"] != edmInt64 {
			t.Errorf("projection dropped @odata.type companion: %v", e)
		}
	}
}
