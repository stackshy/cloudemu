package dns

import (
	"context"
	"sync"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	driver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// TestUpsertRecordAtomicIfNoneMatchCreateOnly locks Azure DNS's create-only
// precondition: If-None-Match:"*" must succeed the first time a record set is
// written and fail FailedPrecondition on a second call at the same key, since
// the record set already exists by then.
func TestUpsertRecordAtomicIfNoneMatchCreateOnly(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	if err != nil {
		t.Fatalf("create zone: %v", err)
	}

	cfg := driver.RecordConfig{ZoneID: zone.ID, Name: "www", Type: "A", TTL: 300, Values: []string{"192.0.2.1"}}

	info, created, err := m.UpsertRecordAtomic(ctx, cfg, "", wildcardETag)
	if err != nil {
		t.Fatalf("first create-only upsert: %v", err)
	}
	if !created {
		t.Fatal("first upsert should report created=true")
	}
	if info.ETag == "" {
		t.Fatal("created record set has empty ETag")
	}

	_, _, err = m.UpsertRecordAtomic(ctx, cfg, "", wildcardETag)
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("second create-only upsert: err=%v, want FailedPrecondition", err)
	}
}

// TestUpsertRecordAtomicIfMatch locks Azure DNS's conditional-update
// precondition: a stale If-Match is rejected FailedPrecondition without
// mutating the record, and the current etag succeeds and mints a new one.
func TestUpsertRecordAtomicIfMatch(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	if err != nil {
		t.Fatalf("create zone: %v", err)
	}

	cfg := driver.RecordConfig{ZoneID: zone.ID, Name: "www", Type: "A", TTL: 300, Values: []string{"192.0.2.1"}}

	initial, _, err := m.UpsertRecordAtomic(ctx, cfg, "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A stale If-Match must be rejected and must not change the stored record.
	cfg.Values = []string{"192.0.2.99"}

	_, _, err = m.UpsertRecordAtomic(ctx, cfg, "not-the-current-etag", "")
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("stale If-Match: err=%v, want FailedPrecondition", err)
	}

	unchanged, err := m.GetRecord(ctx, zone.ID, "www", "A")
	if err != nil {
		t.Fatalf("GetRecord after rejected update: %v", err)
	}
	if unchanged.ETag != initial.ETag || unchanged.Values[0] != "192.0.2.1" {
		t.Fatalf("record mutated despite stale If-Match: %+v", unchanged)
	}

	// The current etag must succeed and mint a new, different one.
	updated, _, err := m.UpsertRecordAtomic(ctx, cfg, initial.ETag, "")
	if err != nil {
		t.Fatalf("current If-Match update: %v", err)
	}
	if updated.ETag == initial.ETag {
		t.Fatal("successful update did not rotate the ETag")
	}
	if updated.Values[0] != "192.0.2.99" {
		t.Fatalf("update did not apply: %+v", updated)
	}

	// If-Match against a record set that does not exist at all is NotFound, not
	// FailedPrecondition.
	missingCfg := driver.RecordConfig{ZoneID: zone.ID, Name: "missing", Type: "A", TTL: 300, Values: []string{"192.0.2.1"}}
	if _, _, err = m.UpsertRecordAtomic(ctx, missingCfg, "some-etag", ""); !cerrors.IsNotFound(err) {
		t.Fatalf("If-Match on missing record: err=%v, want NotFound", err)
	}
}

// TestDeleteRecordAtomicIfMatch locks Azure DNS RecordSets.Delete's optional
// If-Match precondition: a stale etag is rejected and the record survives; the
// current etag deletes it.
func TestDeleteRecordAtomicIfMatch(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	if err != nil {
		t.Fatalf("create zone: %v", err)
	}

	info, err := m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zone.ID, Name: "www", Type: "A", TTL: 300, Values: []string{"192.0.2.1"},
	})
	if err != nil {
		t.Fatalf("create record: %v", err)
	}

	if err = m.DeleteRecordAtomic(ctx, zone.ID, "www", "A", "stale-etag"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("stale If-Match delete: err=%v, want FailedPrecondition", err)
	}

	if _, err = m.GetRecord(ctx, zone.ID, "www", "A"); err != nil {
		t.Fatalf("record should survive a rejected delete: %v", err)
	}

	if err = m.DeleteRecordAtomic(ctx, zone.ID, "www", "A", info.ETag); err != nil {
		t.Fatalf("current If-Match delete: %v", err)
	}

	if _, err = m.GetRecord(ctx, zone.ID, "www", "A"); !cerrors.IsNotFound(err) {
		t.Fatalf("record should be gone after delete: err=%v", err)
	}
}

// TestUpsertRecordAtomicConcurrencyCreateOnly is the concurrency regression for
// the ETag CAS: N goroutines race UpsertRecordAtomic with If-None-Match:"*" at
// the same record-set key starting from an empty store. Exactly one may
// succeed — the whole point of the create-only precondition — and every loser
// must see FailedPrecondition, never a silent double-create or a lost update.
// Run with -race -count=20 to catch the TOCTOU class of bug a separate
// check-then-write (rather than one lock covering both) would allow.
func TestUpsertRecordAtomicConcurrencyCreateOnly(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	if err != nil {
		t.Fatalf("create zone: %v", err)
	}

	const goroutines = 32

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		succeeds int
		fails    int
	)

	for range goroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()

			cfg := driver.RecordConfig{
				ZoneID: zone.ID, Name: "race", Type: "A", TTL: 300,
				Values: []string{"192.0.2.1"},
			}

			_, _, uerr := m.UpsertRecordAtomic(ctx, cfg, "", wildcardETag)

			mu.Lock()
			defer mu.Unlock()

			switch {
			case uerr == nil:
				succeeds++
			case cerrors.IsFailedPrecondition(uerr):
				fails++
			default:
				t.Errorf("unexpected error: %v", uerr)
			}
		}()
	}

	wg.Wait()

	if succeeds != 1 {
		t.Fatalf("succeeds = %d, want exactly 1", succeeds)
	}
	if fails != goroutines-1 {
		t.Fatalf("fails = %d, want %d", fails, goroutines-1)
	}

	rec, err := m.GetRecord(ctx, zone.ID, "race", "A")
	if err != nil {
		t.Fatalf("GetRecord after race: %v", err)
	}
	if rec.ETag == "" {
		t.Fatal("winning record has empty ETag")
	}

	zoneAfter, err := m.GetZone(ctx, zone.ID)
	if err != nil {
		t.Fatalf("GetZone after race: %v", err)
	}
	if zoneAfter.RecordCount != 1 {
		t.Fatalf("zone record count = %d, want 1 (no double-count from a racing loser)", zoneAfter.RecordCount)
	}
}
