package memorystore_test

import (
	"context"
	"testing"

	redis "google.golang.org/api/redis/v1"
)

// TestSDKListOrderingDeterministic locks the #259 ordering fix at the wire
// level: Instances.List must return the same sequence of instance names on
// every call, regardless of the order the instances were created in.
func TestSDKListOrderingDeterministic(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	// Create five instances in a deliberately non-sorted order.
	for _, id := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
			Tier:         "BASIC",
			MemorySizeGb: 1,
		}).InstanceId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("Instances.Create(%s): %v", id, err)
		}
	}

	listInstances := func() []string {
		var names []string

		call := svc.Projects.Locations.Instances.List(parent()).Context(ctx)
		if err := call.Pages(ctx, func(page *redis.ListInstancesResponse) error {
			for _, in := range page.Instances {
				names = append(names, in.Name)
			}
			return nil
		}); err != nil {
			t.Fatalf("Instances.List: %v", err)
		}

		return names
	}

	first := listInstances()
	if len(first) != 5 {
		t.Fatalf("got %d instances, want 5: %v", len(first), first)
	}

	for i := 0; i < 4; i++ {
		got := listInstances()
		if len(got) != len(first) {
			t.Fatalf("list #%d returned %d instances, want %d: %v", i+2, len(got), len(first), got)
		}

		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("list #%d order diverged at index %d: got %q, want %q (full: %v vs %v)",
					i+2, j, got[j], first[j], got, first)
			}
		}
	}
}
