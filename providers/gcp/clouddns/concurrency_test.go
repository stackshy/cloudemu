package clouddns

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// TestCreateRecordConcurrentSameKeyExactlyOneWins locks the fix for a
// Get-then-naked-mutate race: CreateRecord used to check m.records.Has(key)
// and then call m.records.Set(key, rec) as two separate operations, so two
// concurrent callers targeting the same name+type could both observe
// "absent" and both write, silently losing one of the two record sets
// instead of the second returning AlreadyExists. CreateRecord now goes
// through the store's atomic SetIfAbsent, so under a race exactly one
// caller must succeed. Must stay clean under `go test -race`.
func TestCreateRecordConcurrentSameKeyExactlyOneWins(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	const workers = 16

	var (
		wg      sync.WaitGroup
		oks     atomic.Int32
		exists  atomic.Int32
		unknown atomic.Int32
	)

	for w := range workers {
		wg.Add(1)

		go func(w int) {
			defer wg.Done()

			_, err := m.CreateRecord(ctx, driver.RecordConfig{
				ZoneID: zone.ID, Name: "www.example.com", Type: "A", TTL: 300,
				Values: []string{"192.0.2." + strconv.Itoa(w)},
			})

			switch {
			case err == nil:
				oks.Add(1)
			case isAlreadyExists(err):
				exists.Add(1)
			default:
				unknown.Add(1)
			}
		}(w)
	}

	wg.Wait()

	if unknown.Load() != 0 {
		t.Fatalf("unexpected error kind from %d calls", unknown.Load())
	}

	if oks.Load() != 1 {
		t.Fatalf("successful creates = %d, want exactly 1 (got %d AlreadyExists)", oks.Load(), exists.Load())
	}

	if exists.Load() != workers-1 {
		t.Fatalf("AlreadyExists count = %d, want %d", exists.Load(), workers-1)
	}

	records, err := m.ListRecords(ctx, zone.ID)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}

	found := 0
	for i := range records {
		if records[i].Name == "www.example.com" && records[i].Type == "A" {
			found++
		}
	}

	if found != 1 {
		t.Fatalf("stored record sets for www.example.com/A = %d, want 1", found)
	}

	zoneInfo, err := m.GetZone(ctx, zone.ID)
	if err != nil {
		t.Fatalf("GetZone: %v", err)
	}

	if zoneInfo.RecordCount != 1 {
		t.Fatalf("RecordCount = %d, want 1 (no double-counted create)", zoneInfo.RecordCount)
	}
}

// TestUpdateRecordConcurrentWithDeleteNeverResurrects locks the fix for the
// second Get-then-naked-mutate race: UpdateRecord used to Get the record to
// check it exists and then unconditionally Set the new value, so a
// DeleteRecord landing in that window would be silently undone by the
// in-flight update. UpdateRecord now goes through the store's atomic Update,
// which only writes if the key is still present. Must stay clean under
// `go test -race`.
func TestUpdateRecordConcurrentWithDeleteNeverResurrects(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	const iters = 200

	for i := range iters {
		name := "churn.example.com"

		if _, err := m.CreateRecord(ctx, driver.RecordConfig{
			ZoneID: zone.ID, Name: name, Type: "A", TTL: 300, Values: []string{"192.0.2.1"},
		}); err != nil {
			t.Fatalf("CreateRecord iter %d: %v", i, err)
		}

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = m.UpdateRecord(ctx, driver.RecordConfig{
				ZoneID: zone.ID, Name: name, Type: "A", TTL: 600, Values: []string{"192.0.2.2"},
			})
		}()

		go func() {
			defer wg.Done()

			_ = m.DeleteRecord(ctx, zone.ID, name, "A")
		}()

		wg.Wait()

		// Whatever the outcome (delete-then-update racing to NotFound, or
		// update-then-delete), the record must not be left resurrected with a
		// dangling entry the zone's RecordCount disagrees with.
		rec, getErr := m.GetRecord(ctx, zone.ID, name, "A")

		records, listErr := m.ListRecords(ctx, zone.ID)
		if listErr != nil {
			t.Fatalf("ListRecords iter %d: %v", i, listErr)
		}

		present := getErr == nil
		if present != (rec != nil) {
			t.Fatalf("iter %d: GetRecord/rec mismatch", i)
		}

		count := 0
		for j := range records {
			if records[j].Name == name && records[j].Type == "A" {
				count++
			}
		}

		if present && count != 1 {
			t.Fatalf("iter %d: record present but ListRecords shows %d entries", i, count)
		}

		if !present && count != 0 {
			t.Fatalf("iter %d: record absent but ListRecords shows %d entries", i, count)
		}

		// Clean up whichever state won, for the next iteration.
		_ = m.DeleteRecord(ctx, zone.ID, name, "A")
	}
}

// isAlreadyExists reports whether err's message indicates the cerrors
// AlreadyExists case CreateRecord returns on a key collision.
func isAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}
