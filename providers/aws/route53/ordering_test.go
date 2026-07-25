package route53

import (
	"context"
	"testing"

	driver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// TestListOrderingDeterministic locks the #259 ordering guarantee: list
// endpoints return the same, defined order on every call (map iteration
// randomness must never reach the wire).
func TestListOrderingDeterministic(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	for _, name := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		if _, err := m.CreateZone(ctx, driver.ZoneConfig{Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	first, err := m.ListZones(ctx, scope.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 5 {
		t.Fatalf("list returned %d items, want 5", len(first))
	}

	for range 5 {
		again, err := m.ListZones(ctx, scope.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range first {
			if again[i].Name != first[i].Name {
				t.Fatalf("list order changed between calls: %v vs %v", again[i].Name, first[i].Name)
			}
		}
	}
}

// TestListRecordsOrderingDeterministic locks the #259 ordering guarantee for
// record listing: ListRecords must return the same, defined order on every
// call (map iteration randomness must never reach the wire).
func TestListRecordsOrderingDeterministic(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	if err != nil {
		t.Fatalf("create zone: %v", err)
	}

	for _, name := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		if _, err := m.CreateRecord(ctx, driver.RecordConfig{
			ZoneID: zone.ID, Name: name + ".example.com", Type: "A", TTL: 300,
			Values: []string{"192.0.2.1"},
		}); err != nil {
			t.Fatalf("create record %s: %v", name, err)
		}
	}

	first, err := m.ListRecords(ctx, zone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 5 {
		t.Fatalf("list returned %d records, want >=5", len(first))
	}

	for range 5 {
		again, err := m.ListRecords(ctx, zone.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(again) != len(first) {
			t.Fatalf("list length changed: %d vs %d", len(again), len(first))
		}
		for i := range first {
			if again[i].Name != first[i].Name || again[i].Type != first[i].Type {
				t.Fatalf("record order changed between calls at %d: %v vs %v", i, again[i], first[i])
			}
		}
	}
}

// TestGetRecordWeightedDeterministic locks that GetRecord resolves a name+type
// with several weighted records to the same one every call — the lowest set ID
// in sorted order — instead of a map-order-random pick (#259).
func TestGetRecordWeightedDeterministic(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	if err != nil {
		t.Fatalf("create zone: %v", err)
	}

	weight := 10
	for _, sid := range []string{"west", "east", "central"} {
		if _, err := m.CreateRecord(ctx, driver.RecordConfig{
			ZoneID: zone.ID, Name: "api.example.com", Type: "A", TTL: 60,
			Values: []string{"192.0.2.1"}, SetID: sid, Weight: &weight,
		}); err != nil {
			t.Fatalf("create weighted %s: %v", sid, err)
		}
	}

	first, err := m.GetRecord(ctx, zone.ID, "api.example.com", "A")
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	for range 5 {
		again, err := m.GetRecord(ctx, zone.ID, "api.example.com", "A")
		if err != nil {
			t.Fatal(err)
		}
		if again.SetID != first.SetID {
			t.Fatalf("GetRecord returned different weighted record across calls: %q vs %q", again.SetID, first.SetID)
		}
	}
	if first.SetID != "central" {
		t.Fatalf("GetRecord set ID = %q, want lowest in sorted order (central)", first.SetID)
	}
}
