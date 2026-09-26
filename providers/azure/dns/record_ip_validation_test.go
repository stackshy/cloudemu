package dns

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// TestRecordRejectsBadAddress checks that create, update and the atomic upsert
// reject an A or AAAA value that is not an address of the right family, and
// store nothing.
func TestRecordRejectsBadAddress(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	zoneID := createTestZone(t, m)

	if _, err := m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"192.0.2.1"},
	}); err != nil {
		t.Fatalf("good CreateRecord: %v", err)
	}

	bad := []driver.RecordConfig{
		{ZoneID: zoneID, Name: "bad4", Type: "A", TTL: 300, Values: []string{"192.0.2.2", "999.1.1.1"}},
		{ZoneID: zoneID, Name: "bad6", Type: "AAAA", TTL: 300, Values: []string{"192.0.2.2"}},
	}

	for _, cfg := range bad {
		if _, err := m.CreateRecord(ctx, cfg); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("CreateRecord(%s): got %v, want InvalidArgument", cfg.Name, err)
		}

		if _, _, err := m.UpsertRecordAtomic(ctx, cfg, "", ""); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("UpsertRecordAtomic(%s): got %v, want InvalidArgument", cfg.Name, err)
		}

		if _, err := m.GetRecord(ctx, zoneID, cfg.Name, cfg.Type); err == nil {
			t.Fatalf("record %s stored although rejected", cfg.Name)
		}
	}

	_, err := m.UpdateRecord(ctx, driver.RecordConfig{
		ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"not-an-ip"},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("UpdateRecord: got %v, want InvalidArgument", err)
	}

	got, err := m.GetRecord(ctx, zoneID, "www", "A")
	if err != nil || len(got.Values) != 1 || got.Values[0] != "192.0.2.1" {
		t.Fatalf("stored record changed by a rejected update: %+v, %v", got, err)
	}
}
