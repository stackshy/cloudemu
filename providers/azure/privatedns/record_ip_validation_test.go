package privatedns_test

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/privatedns/driver"
)

// TestRecordRejectsBadAddress checks that an A or AAAA record set whose entry
// is not an address of the right family is rejected and nothing is stored.
func TestRecordRejectsBadAddress(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdatePrivateZone(ctx, rg, zone, driver.PrivateZone{})
	requireNoError(t, err, "create zone")

	tests := []struct {
		name  string
		rtype string
		data  map[string]any
	}{
		{"A not an IP", driver.RecordTypeA, map[string]any{"aRecords": []any{
			map[string]any{"ipv4Address": "10.0.0.1"}, map[string]any{"ipv4Address": "999.1.1.1"},
		}}},
		{"A missing address", driver.RecordTypeA, map[string]any{"aRecords": []any{map[string]any{}}}},
		{"AAAA holding IPv4", "AAAA", map[string]any{"aaaaRecords": []map[string]any{{"ipv6Address": "10.0.0.1"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, cerr := m.CreateOrUpdateRecordSet(ctx, rg, zone, tt.rtype, "bad", driver.RecordSet{RecordData: tt.data})
			if !cerrors.IsInvalidArgument(cerr) {
				t.Fatalf("got %v, want InvalidArgument", cerr)
			}

			if _, gerr := m.GetRecordSet(ctx, rg, zone, tt.rtype, "bad"); gerr == nil {
				t.Fatal("record set stored although rejected")
			}
		})
	}

	good := map[string]any{"aaaaRecords": []any{map[string]any{"ipv6Address": "fd00::1"}}}
	_, _, err = m.CreateOrUpdateRecordSet(ctx, rg, zone, "AAAA", "good", driver.RecordSet{RecordData: good})
	requireNoError(t, err, "good AAAA")
}
