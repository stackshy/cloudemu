package loganalytics

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

func TestWorkspacesSameNameInTwoGroupsSurviveSnapshot(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	for _, rg := range []string{"rg1", "rg2"} {
		cfg := driver.LogGroupConfig{Name: "law", Scope: scope.Scope{Subscription: "s1", ResourceGroup: rg}}
		if _, err := src.CreateLogGroup(ctx, cfg); err != nil {
			t.Fatalf("create in %s: %v", rg, err)
		}
	}

	data, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	for _, rg := range []string{"rg1", "rg2"} {
		info, err := dst.GetLogGroupScoped(ctx, "s1", rg, "law")
		if err != nil {
			t.Fatalf("get %s after restore: %v", rg, err)
		}

		if info.Scope.ResourceGroup != rg {
			t.Errorf("%s resolved to group in %q", rg, info.Scope.ResourceGroup)
		}
	}

	if err := dst.DeleteLogGroupScoped(ctx, "s1", "rg1", "law"); err != nil {
		t.Fatal(err)
	}

	if _, err := dst.GetLogGroupScoped(ctx, "s1", "rg2", "law"); err != nil {
		t.Errorf("rg2 workspace gone after rg1 delete: %v", err)
	}
}

// A snapshot taken before workspaces were keyed by scope stores them by name.
func TestRestoreMigratesNameKeyedWorkspaces(t *testing.T) {
	ctx := context.Background()

	legacy := map[string]any{"groups": map[string]any{
		"law": map[string]any{"info": driver.LogGroupInfo{
			Name: "law", Scope: scope.Scope{ResourceGroup: "rg1"},
		}},
	}}

	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}

	m := newTestMock()
	if err := m.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	if _, err := m.GetLogGroupScoped(ctx, m.opts.AccountID, "rg1", "law"); err != nil {
		t.Errorf("scoped get after legacy restore: %v", err)
	}

	if _, err := m.GetLogGroup(ctx, "law"); err != nil {
		t.Errorf("name-only get after legacy restore: %v", err)
	}
}
