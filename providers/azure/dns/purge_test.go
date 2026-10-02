package dns

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

func TestPurgeResourceGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zones := map[string]scope.Scope{
		"gone.example.com":     {Subscription: "s1", ResourceGroup: "Cas1"},
		"kept.example.com":     {Subscription: "s1", ResourceGroup: "cas10"},
		"unscoped.example.com": {},
	}

	ids := map[string]string{}

	for name, sc := range zones {
		z, err := m.CreateZone(ctx, driver.ZoneConfig{Name: name, Scope: sc})
		if err != nil {
			t.Fatal(err)
		}

		ids[name] = z.ID

		_, err = m.CreateRecord(ctx, driver.RecordConfig{ZoneID: z.ID, Name: "www", Type: "A", TTL: 60, Values: []string{"10.0.0.1"}})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := m.PurgeResourceGroup(ctx, "s1", "cas1"); err != nil {
		t.Fatalf("PurgeResourceGroup: %v", err)
	}

	if _, err := m.GetZone(ctx, ids["gone.example.com"]); err == nil {
		t.Error("zone in cas1 survived the purge")
	}

	if recs, _ := m.ListRecords(ctx, ids["gone.example.com"]); len(recs) != 0 {
		t.Errorf("purged zone left %d record set(s)", len(recs))
	}

	for _, name := range []string{"kept.example.com", "unscoped.example.com"} {
		if _, err := m.GetZone(ctx, ids[name]); err != nil {
			t.Errorf("zone %s was purged with cas1: %v", name, err)
		}
	}
}
