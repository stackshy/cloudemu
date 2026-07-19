package eventarc_test

import (
	"context"
	"testing"

	eventarc "google.golang.org/api/eventarc/v1"
)

// TestSDKListOrderingDeterministic locks the #259 ordering fix at the wire
// level: Triggers.List must return the same sequence of trigger names on
// every call, regardless of the order the triggers were created in.
func TestSDKListOrderingDeterministic(t *testing.T) {
	svc := newEventarcService(t)
	ctx := context.Background()

	// Create five triggers in a deliberately non-sorted order.
	for _, id := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		trigger := &eventarc.Trigger{
			EventFilters: []*eventarc.EventFilter{
				{Attribute: "type", Value: "google.cloud.storage.object.v1.finalized"},
			},
			Destination: &eventarc.Destination{
				CloudRun: &eventarc.CloudRun{Service: "svc-" + id, Region: testLocation},
			},
		}

		if _, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).
			TriggerId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("Triggers.Create(%s): %v", id, err)
		}
	}

	listTriggers := func() []string {
		var names []string

		call := svc.Projects.Locations.Triggers.List(parent()).Context(ctx)
		if err := call.Pages(ctx, func(page *eventarc.ListTriggersResponse) error {
			for _, tr := range page.Triggers {
				names = append(names, tr.Name)
			}
			return nil
		}); err != nil {
			t.Fatalf("Triggers.List: %v", err)
		}

		return names
	}

	first := listTriggers()
	if len(first) != 5 {
		t.Fatalf("got %d triggers, want 5: %v", len(first), first)
	}

	for i := 0; i < 4; i++ {
		got := listTriggers()
		if len(got) != len(first) {
			t.Fatalf("list #%d returned %d triggers, want %d: %v", i+2, len(got), len(first), got)
		}

		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("list #%d order diverged at index %d: got %q, want %q (full: %v vs %v)",
					i+2, j, got[j], first[j], got, first)
			}
		}
	}
}
