package databricks

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/databricks/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"), config.WithAccountID("sub-1"))

	return New(opts)
}

func validConfig() driver.WorkspaceConfig {
	return driver.WorkspaceConfig{
		Name:                   "ws-1",
		ResourceGroup:          "rg-1",
		Location:               "eastus",
		ManagedResourceGroupID: "/subscriptions/sub-1/resourceGroups/managed",
	}
}

func TestCreateWorkspace(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*driver.WorkspaceConfig)
		expectErr bool
	}{
		{name: "success", mutate: func(*driver.WorkspaceConfig) {}},
		{name: "missing name", mutate: func(c *driver.WorkspaceConfig) { c.Name = "" }, expectErr: true},
		{name: "missing resource group", mutate: func(c *driver.WorkspaceConfig) { c.ResourceGroup = "" }, expectErr: true},
		{name: "missing location", mutate: func(c *driver.WorkspaceConfig) { c.Location = "" }, expectErr: true},
		{name: "missing managed rg", mutate: func(c *driver.WorkspaceConfig) { c.ManagedResourceGroupID = "" }, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			cfg := validConfig()
			tc.mutate(&cfg)

			ws, err := m.CreateWorkspace(context.Background(), cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, driver.StateSucceeded, ws.ProvisioningState)
			assertEqual(t, "standard", ws.SKUName)
			assertNotEmpty(t, ws.ID)
			assertNotEmpty(t, ws.WorkspaceURL)
			assertNotEmpty(t, ws.WorkspaceID)
		})
	}
}

func TestCreateWorkspaceIdempotent(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	first, err := m.CreateWorkspace(ctx, validConfig())
	requireNoError(t, err)

	second, err := m.CreateWorkspace(ctx, validConfig())
	requireNoError(t, err)

	assertEqual(t, first.ID, second.ID)
	assertEqual(t, first.WorkspaceID, second.WorkspaceID)
}

func TestCreateOrUpdateAppliesMutableFields(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	first, err := m.CreateWorkspace(ctx, validConfig())
	requireNoError(t, err)

	// PUT again with changed tags + SKU — ARM create-or-update must apply them.
	cfg := validConfig()
	cfg.SKUName = "premium"
	cfg.Tags = map[string]string{"env": "prod"}

	updated, err := m.CreateWorkspace(ctx, cfg)
	requireNoError(t, err)

	assertEqual(t, "premium", updated.SKUName)
	assertEqual(t, "prod", updated.Tags["env"])

	// Identity fields are preserved across the update.
	assertEqual(t, first.ID, updated.ID)
	assertEqual(t, first.WorkspaceID, updated.WorkspaceID)
	assertEqual(t, first.CreatedAt, updated.CreatedAt)

	// The change is durable.
	got, err := m.GetWorkspace(ctx, "rg-1", "ws-1")
	requireNoError(t, err)
	assertEqual(t, "premium", got.SKUName)
	assertEqual(t, "prod", got.Tags["env"])
}

func TestGetAndDeleteWorkspace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateWorkspace(ctx, validConfig())
	requireNoError(t, err)

	got, err := m.GetWorkspace(ctx, "rg-1", "ws-1")
	requireNoError(t, err)
	assertEqual(t, "ws-1", got.Name)

	_, err = m.GetWorkspace(ctx, "rg-1", "missing")
	assertError(t, err, true)

	requireNoError(t, m.DeleteWorkspace(ctx, "rg-1", "ws-1"))
	assertError(t, m.DeleteWorkspace(ctx, "rg-1", "ws-1"), true)
}

func TestUpdateWorkspaceTags(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateWorkspace(ctx, validConfig())
	requireNoError(t, err)

	ws, err := m.UpdateWorkspaceTags(ctx, "rg-1", "ws-1", map[string]string{"env": "prod"})
	requireNoError(t, err)
	assertEqual(t, "prod", ws.Tags["env"])

	_, err = m.UpdateWorkspaceTags(ctx, "rg-1", "missing", nil)
	assertError(t, err, true)
}

func TestListWorkspaces(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	mk := func(name, rg string) {
		cfg := validConfig()
		cfg.Name = name
		cfg.ResourceGroup = rg
		_, err := m.CreateWorkspace(ctx, cfg)
		requireNoError(t, err)
	}

	mk("a", "rg-1")
	mk("b", "rg-1")
	mk("c", "rg-2")

	rg1, err := m.ListWorkspacesByResourceGroup(ctx, "rg-1")
	requireNoError(t, err)
	assertEqual(t, 2, len(rg1))

	all, err := m.ListWorkspaces(ctx)
	requireNoError(t, err)
	assertEqual(t, 3, len(all))
}

func TestTagsCopiedOnCreate(t *testing.T) {
	m := newTestMock()
	cfg := validConfig()
	cfg.Tags = map[string]string{"k": "original"}

	ws, err := m.CreateWorkspace(context.Background(), cfg)
	requireNoError(t, err)

	cfg.Tags["k"] = "mutated"

	assertEqual(t, "original", ws.Tags["k"])
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error, got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}
