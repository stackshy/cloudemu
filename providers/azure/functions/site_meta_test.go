package functions

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func newMetaMock() *Mock {
	return New(config.NewOptions(config.WithAccountID("test-sub")))
}

func TestUpsertSiteMetaGeneratesAndPreservesKeys(t *testing.T) {
	m := newMetaMock()
	ctx := context.Background()

	created, err := m.UpsertSiteMeta(ctx, SiteMeta{
		Name: "app1", Subscription: "sub1", ResourceGroup: "rgA",
		Location: "westus2", AppSettings: map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatalf("upsert create: %v", err)
	}

	if created.MasterKey == "" || created.HostFunctionKeys["default"] == "" {
		t.Fatalf("keys not generated: %+v", created)
	}

	if created.ProvisioningState != "Succeeded" {
		t.Fatalf("provisioningState = %q", created.ProvisioningState)
	}

	masterKey := created.MasterKey

	// An update keeps the generated keys and updates the mutable fields.
	updated, err := m.UpsertSiteMeta(ctx, SiteMeta{
		Name: "app1", Subscription: "sub1", ResourceGroup: "rgA",
		Location: "eastus", AppSettings: map[string]string{"FOO": "baz"},
	})
	if err != nil {
		t.Fatalf("upsert update: %v", err)
	}

	if updated.MasterKey != masterKey {
		t.Fatalf("master key changed on update: %q -> %q", masterKey, updated.MasterKey)
	}

	if updated.Location != "eastus" || updated.AppSettings["FOO"] != "baz" {
		t.Fatalf("mutable fields not updated: %+v", updated)
	}
}

func TestListSiteMetaFiltersByScope(t *testing.T) {
	m := newMetaMock()
	ctx := context.Background()

	put := func(name, rg string) {
		if _, err := m.UpsertSiteMeta(ctx, SiteMeta{Name: name, Subscription: "sub1", ResourceGroup: rg}); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}
	put("a1", "rgA")
	put("a2", "rgA")
	put("b1", "rgB")

	inRG, err := m.ListSiteMeta(ctx, "sub1", "rgA")
	if err != nil {
		t.Fatalf("list rgA: %v", err)
	}

	if len(inRG) != 2 {
		t.Fatalf("rgA list = %d, want 2", len(inRG))
	}

	subWide, err := m.ListSiteMeta(ctx, "sub1", "")
	if err != nil {
		t.Fatalf("list sub-wide: %v", err)
	}

	if len(subWide) != 3 {
		t.Fatalf("sub-wide list = %d, want 3", len(subWide))
	}
}

func TestSiteFunctionsLifecycle(t *testing.T) {
	m := newMetaMock()
	ctx := context.Background()

	if _, err := m.UpsertSiteMeta(ctx, SiteMeta{Name: "app1", Subscription: "sub1", ResourceGroup: "rgA"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// A fresh app has no functions and a missing one is NotFound.
	fns, err := m.ListSiteFunctions(ctx, "app1")
	if err != nil {
		t.Fatalf("list functions: %v", err)
	}

	if len(fns) != 0 {
		t.Fatalf("fresh app functions = %d, want 0", len(fns))
	}

	if _, err := m.GetSiteFunction(ctx, "app1", "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("get missing function = %v, want NotFound", err)
	}

	// Create one and read it back with a generated default key.
	created, err := m.CreateSiteFunction(ctx, "app1", SiteFunction{Name: "fn1", Language: "python"})
	if err != nil {
		t.Fatalf("create function: %v", err)
	}

	if created.Keys["default"] == "" {
		t.Fatalf("function default key not generated: %+v", created)
	}

	keys, err := m.FunctionKeys(ctx, "app1", "fn1")
	if err != nil {
		t.Fatalf("function keys: %v", err)
	}

	if keys["default"] != created.Keys["default"] {
		t.Fatalf("function keys mismatch")
	}

	if err := m.DeleteSiteFunction(ctx, "app1", "fn1"); err != nil {
		t.Fatalf("delete function: %v", err)
	}

	if _, err := m.GetSiteFunction(ctx, "app1", "fn1"); !cerrors.IsNotFound(err) {
		t.Fatalf("get deleted function = %v, want NotFound", err)
	}

	// Operations on a missing site are NotFound.
	if _, err := m.CreateSiteFunction(ctx, "ghost", SiteFunction{Name: "fn"}); !cerrors.IsNotFound(err) {
		t.Fatalf("create on missing site = %v, want NotFound", err)
	}
}
