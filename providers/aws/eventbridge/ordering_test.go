package eventbridge

import (
	"context"
	"testing"

	driver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// TestListOrderingDeterministic locks the #259 ordering guarantee: list
// endpoints return the same, defined order on every call (map iteration
// randomness must never reach the wire).
func TestListOrderingDeterministic(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	for _, name := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		if _, err := m.CreateEventBus(ctx, driver.EventBusConfig{Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	first, err := m.ListEventBuses(ctx, scope.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	// EventBridge pre-creates the "default" bus, so 5 created + 1 default.
	if len(first) < 5 {
		t.Fatalf("list returned %d items, want >= 5", len(first))
	}

	for range 5 {
		again, err := m.ListEventBuses(ctx, scope.Scope{})
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
