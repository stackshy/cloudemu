package clouddns_test

import (
	"context"
	"testing"

	dns "google.golang.org/api/dns/v1"
)

// TestSDKListOrderingDeterministic locks the #259 ordering fix at the wire
// level: ManagedZones.List must return the same sequence of zone names on
// every call, regardless of the order the zones were created in.
func TestSDKListOrderingDeterministic(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	// Create five zones in a deliberately non-sorted order.
	for _, id := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
			Name:    id,
			DnsName: id + ".example.com.",
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("ManagedZones.Create(%s): %v", id, err)
		}
	}

	listZones := func() []string {
		var names []string

		call := svc.ManagedZones.List(testProject).Context(ctx)
		if err := call.Pages(ctx, func(page *dns.ManagedZonesListResponse) error {
			for _, z := range page.ManagedZones {
				names = append(names, z.Name)
			}
			return nil
		}); err != nil {
			t.Fatalf("ManagedZones.List: %v", err)
		}

		return names
	}

	first := listZones()
	if len(first) != 5 {
		t.Fatalf("got %d zones, want 5: %v", len(first), first)
	}

	for i := 0; i < 4; i++ {
		got := listZones()
		if len(got) != len(first) {
			t.Fatalf("list #%d returned %d zones, want %d: %v", i+2, len(got), len(first), got)
		}

		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("list #%d order diverged at index %d: got %q, want %q (full: %v vs %v)",
					i+2, j, got[j], first[j], got, first)
			}
		}
	}
}
