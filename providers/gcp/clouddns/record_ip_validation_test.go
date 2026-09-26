package clouddns

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// TestRecordRejectsBadAddress checks that create and update reject an A or
// AAAA rrdata that is not an address of the right family, and store nothing.
func TestRecordRejectsBadAddress(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	if _, err = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zone.ID, Name: "www.example.com.", Type: "A", TTL: 300, Values: []string{"192.0.2.1"},
	}); err != nil {
		t.Fatalf("good CreateRecord: %v", err)
	}

	bad := []driver.RecordConfig{
		{ZoneID: zone.ID, Name: "bad4.example.com.", Type: "A", TTL: 300, Values: []string{"bogus"}},
		{ZoneID: zone.ID, Name: "bad6.example.com.", Type: "AAAA", TTL: 300, Values: []string{"192.0.2.2"}},
	}

	for _, cfg := range bad {
		if _, cerr := m.CreateRecord(ctx, cfg); !cerrors.IsInvalidArgument(cerr) {
			t.Fatalf("CreateRecord(%s): got %v, want InvalidArgument", cfg.Name, cerr)
		}

		if _, gerr := m.GetRecord(ctx, zone.ID, cfg.Name, cfg.Type); gerr == nil {
			t.Fatalf("record %s stored although rejected", cfg.Name)
		}
	}

	_, err = m.UpdateRecord(ctx, driver.RecordConfig{
		ZoneID: zone.ID, Name: "www.example.com.", Type: "A", TTL: 300, Values: []string{"2001:db8::1"},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("UpdateRecord: got %v, want InvalidArgument", err)
	}

	got, err := m.GetRecord(ctx, zone.ID, "www.example.com.", "A")
	if err != nil || len(got.Values) != 1 || got.Values[0] != "192.0.2.1" {
		t.Fatalf("stored record changed by a rejected update: %+v, %v", got, err)
	}
}
