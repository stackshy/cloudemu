package cache

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cache/driver"
)

func mustCreate(t *testing.T, m *Mock, name string) {
	t.Helper()

	if _, err := m.CreateCache(context.Background(), driver.CacheConfig{Name: name, Engine: "redis"}); err != nil {
		t.Fatalf("CreateCache(%s): %v", name, err)
	}
}

func TestPatchScheduleValidation(t *testing.T) {
	m, _ := newTestMock()
	mustCreate(t, m, "c1")

	tests := []struct {
		name    string
		entries []driver.PatchScheduleEntry
		want    errors.Code
	}{
		{"empty", nil, errors.InvalidArgument},
		{"bad day", []driver.PatchScheduleEntry{{DayOfWeek: "Funday", StartHourUTC: 1}}, errors.InvalidArgument},
		{"hour 24", []driver.PatchScheduleEntry{{DayOfWeek: "Monday", StartHourUTC: 24}}, errors.InvalidArgument},
		{"negative hour", []driver.PatchScheduleEntry{{DayOfWeek: "Monday", StartHourUTC: -1}}, errors.InvalidArgument},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.SetPatchSchedule(context.Background(), "c1", tc.entries)
			if errors.GetCode(err) != tc.want {
				t.Fatalf("err = %v, want code %v", err, tc.want)
			}
		})
	}

	if _, err := m.SetPatchSchedule(context.Background(), "missing", []driver.PatchScheduleEntry{
		{DayOfWeek: "Monday"},
	}); !errors.IsNotFound(err) {
		t.Fatalf("missing cache: err = %v, want NotFound", err)
	}
}

func TestPatchScheduleDefaultsAndCreatedFlag(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	mustCreate(t, m, "c1")

	created, err := m.SetPatchSchedule(ctx, "c1", []driver.PatchScheduleEntry{{DayOfWeek: "weekend", StartHourUTC: 2}})
	if err != nil || !created {
		t.Fatalf("first set: created=%v err=%v, want true", created, err)
	}

	created, err = m.SetPatchSchedule(ctx, "c1", []driver.PatchScheduleEntry{{DayOfWeek: "Sunday", StartHourUTC: 3}})
	if err != nil || created {
		t.Fatalf("second set: created=%v err=%v, want false", created, err)
	}

	got, err := m.GetPatchSchedule(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}

	want := []driver.PatchScheduleEntry{{DayOfWeek: "Sunday", StartHourUTC: 3, MaintenanceWindow: "PT5H"}}
	if !slices.Equal(got, want) {
		t.Fatalf("schedule = %+v, want %+v", got, want)
	}
}

func TestFirewallRuleValidation(t *testing.T) {
	m, _ := newTestMock()
	mustCreate(t, m, "c1")

	tests := []struct {
		name string
		rule driver.FirewallRule
		ok   bool
	}{
		{"single address", driver.FirewallRule{Name: "a", StartIP: "10.0.0.1", EndIP: "10.0.0.1"}, true},
		{"start above end", driver.FirewallRule{Name: "b", StartIP: "10.0.0.9", EndIP: "10.0.0.1"}, false},
		{"ipv6", driver.FirewallRule{Name: "c", StartIP: "::1", EndIP: "::2"}, false},
		{"garbage", driver.FirewallRule{Name: "d", StartIP: "x", EndIP: "10.0.0.1"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.PutFirewallRule(context.Background(), "c1", tc.rule)
			if tc.ok != (err == nil) {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}

			if !tc.ok && !errors.IsInvalidArgument(err) {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestChildrenRemovedWithCache(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	mustCreate(t, m, "c1")

	if _, err := m.SetPatchSchedule(ctx, "c1", []driver.PatchScheduleEntry{{DayOfWeek: "Monday"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.PutFirewallRule(ctx, "c1", driver.FirewallRule{Name: "r", StartIP: "1.1.1.1", EndIP: "1.1.1.2"}); err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteCache(ctx, "c1"); err != nil {
		t.Fatal(err)
	}

	mustCreate(t, m, "c1")

	if _, err := m.GetPatchSchedule(ctx, "c1"); !errors.IsNotFound(err) {
		t.Errorf("patch schedule survived delete: err = %v", err)
	}

	rules, err := m.ListFirewallRules(ctx, "c1")
	if err != nil || len(rules) != 0 {
		t.Errorf("rules after recreate = %v (err %v), want none", rules, err)
	}
}

func TestSnapshotRoundTripChildren(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()
	mustCreate(t, src, "c1")

	entries := []driver.PatchScheduleEntry{{DayOfWeek: "Saturday", StartHourUTC: 5, MaintenanceWindow: "PT6H"}}
	if _, err := src.SetPatchSchedule(ctx, "c1", entries); err != nil {
		t.Fatal(err)
	}

	rule := driver.FirewallRule{Name: "r1", StartIP: "10.0.0.1", EndIP: "10.0.0.5"}
	if _, err := src.PutFirewallRule(ctx, "c1", rule); err != nil {
		t.Fatal(err)
	}

	data, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	dst, _ := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	gotPatch, err := dst.GetPatchSchedule(ctx, "c1")
	if err != nil || !slices.Equal(gotPatch, entries) {
		t.Errorf("restored schedule = %+v (err %v), want %+v", gotPatch, err, entries)
	}

	gotRule, err := dst.GetFirewallRule(ctx, "c1", "r1")
	if err != nil || gotRule != rule {
		t.Errorf("restored rule = %+v (err %v), want %+v", gotRule, err, rule)
	}
}

// TestChildWritesNotLostUnderConcurrency runs firewall PUTs alongside key
// regeneration and cache updates. Every mutation goes through caches.Update,
// so no rule is lost to a stale copy-and-swap. Run with -race.
func TestChildWritesNotLostUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	mustCreate(t, m, "c1")

	const n = 50

	var wg sync.WaitGroup

	for i := range n {
		wg.Add(3)

		go func() {
			defer wg.Done()

			rule := driver.FirewallRule{Name: fmt.Sprintf("r%02d", i), StartIP: "10.0.0.1", EndIP: "10.0.0.2"}
			if _, err := m.PutFirewallRule(ctx, "c1", rule); err != nil {
				t.Error(err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, _, err := m.RegenerateCacheKey(ctx, "c1", "Primary"); err != nil {
				t.Error(err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.UpdateCache(ctx, driver.CacheConfig{Name: "c1", Tags: map[string]string{"i": "x"}}); err != nil {
				t.Error(err)
			}
		}()
	}

	wg.Wait()

	rules, err := m.ListFirewallRules(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}

	if len(rules) != n {
		t.Fatalf("rules = %d, want %d", len(rules), n)
	}
}
