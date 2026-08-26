package functions

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
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
	created, wasCreated, err := m.CreateSiteFunction(ctx, "app1", SiteFunction{Name: "fn1", Language: "python"})
	if err != nil {
		t.Fatalf("create function: %v", err)
	}

	if !wasCreated {
		t.Fatal("first CreateSiteFunction reported created=false, want true")
	}

	if created.Keys["default"] == "" {
		t.Fatalf("function default key not generated: %+v", created)
	}

	// A re-PUT with no keys preserves the generated key and reports created=false.
	reput, wasCreated, err := m.CreateSiteFunction(ctx, "app1", SiteFunction{Name: "fn1", Language: "python"})
	if err != nil {
		t.Fatalf("re-put function: %v", err)
	}

	if wasCreated {
		t.Fatal("re-PUT CreateSiteFunction reported created=true, want false")
	}

	if reput.Keys["default"] != created.Keys["default"] {
		t.Fatalf("re-PUT rotated the function key: %q -> %q", created.Keys["default"], reput.Keys["default"])
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
	if _, _, err := m.CreateSiteFunction(ctx, "ghost", SiteFunction{Name: "fn"}); !cerrors.IsNotFound(err) {
		t.Fatalf("create on missing site = %v, want NotFound", err)
	}
}

// TestGetSiteMetaScoped covers the deep-sweep BLOCKER: a site must only be
// visible through the subscription/resource group it was created in.
func TestGetSiteMetaScoped(t *testing.T) {
	m := newMetaMock()
	ctx := context.Background()

	if _, err := m.UpsertSiteMeta(ctx, SiteMeta{Name: "app1", Subscription: "sub1", ResourceGroup: "rgA"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if _, err := m.GetSiteMeta(ctx, "sub1", "rgA", "app1"); err != nil {
		t.Fatalf("get in owning rg: %v", err)
	}

	if _, err := m.GetSiteMeta(ctx, "sub1", "rgB", "app1"); !cerrors.IsNotFound(err) {
		t.Fatalf("get from wrong rg = %v, want NotFound", err)
	}

	if _, err := m.GetSiteMeta(ctx, "sub2", "rgA", "app1"); !cerrors.IsNotFound(err) {
		t.Fatalf("get from wrong subscription = %v, want NotFound", err)
	}
}

// TestDeleteSiteMetaScoped covers the deep-sweep BLOCKER: DELETE against the
// wrong resource group must not remove another resource group's site.
func TestDeleteSiteMetaScoped(t *testing.T) {
	m := newMetaMock()
	ctx := context.Background()

	if _, err := m.UpsertSiteMeta(ctx, SiteMeta{Name: "app1", Subscription: "sub1", ResourceGroup: "rgA"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := m.DeleteSiteMeta(ctx, "sub1", "rgB", "app1"); err != nil {
		t.Fatalf("delete from wrong rg returned error: %v", err)
	}

	if _, err := m.GetSiteMeta(ctx, "sub1", "rgA", "app1"); err != nil {
		t.Fatalf("site deleted from wrong rg's request: %v", err)
	}

	if err := m.DeleteSiteMeta(ctx, "sub1", "rgA", "app1"); err != nil {
		t.Fatalf("delete in owning rg: %v", err)
	}

	if _, err := m.GetSiteMeta(ctx, "sub1", "rgA", "app1"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete = %v, want NotFound", err)
	}
}

// TestGetFunctionScopedAndDeleteFunctionScoped covers the deep-sweep BLOCKER
// end to end: the portable function record is only reachable/deletable
// through the resource group its site was created in.
func TestGetFunctionScopedAndDeleteFunctionScoped(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "app1", Runtime: "dotnet6"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := m.UpsertSiteMeta(ctx, SiteMeta{Name: "app1", Subscription: "sub1", ResourceGroup: "rgA"}); err != nil {
		t.Fatalf("upsert site meta: %v", err)
	}

	if _, err := m.GetFunctionScoped(ctx, "sub1", "rgB", "app1"); !cerrors.IsNotFound(err) {
		t.Fatalf("get from wrong rg = %v, want NotFound", err)
	}

	info, err := m.GetFunctionScoped(ctx, "sub1", "rgA", "app1")
	if err != nil {
		t.Fatalf("get from owning rg: %v", err)
	}

	if info.Name != "app1" {
		t.Fatalf("info.Name = %q, want app1", info.Name)
	}

	if err := m.DeleteFunctionScoped(ctx, "sub1", "rgB", "app1"); !cerrors.IsNotFound(err) {
		t.Fatalf("delete from wrong rg = %v, want NotFound", err)
	}

	// The function must still exist — a delete against the wrong resource
	// group must not have removed it.
	if _, err := m.GetFunctionScoped(ctx, "sub1", "rgA", "app1"); err != nil {
		t.Fatalf("function removed by wrong-rg delete: %v", err)
	}

	if err := m.DeleteFunctionScoped(ctx, "sub1", "rgA", "app1"); err != nil {
		t.Fatalf("delete from owning rg: %v", err)
	}

	if _, err := m.GetFunctionScoped(ctx, "sub1", "rgA", "app1"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete = %v, want NotFound", err)
	}

	if _, err := m.GetFunction(ctx, "app1"); !cerrors.IsNotFound(err) {
		t.Fatalf("underlying function survived scoped delete: %v", err)
	}
}

// TestUpdateAppSettingsScoped covers the tractable HIGH finding: PUT
// config/appsettings must persist the settings and must not touch a
// same-named site in a different resource group.
func TestUpdateAppSettingsScoped(t *testing.T) {
	m := newMetaMock()
	ctx := context.Background()

	if _, err := m.UpsertSiteMeta(ctx, SiteMeta{
		Name: "app1", Subscription: "sub1", ResourceGroup: "rgA",
		Location: "eastus", AppSettings: map[string]string{"FOO": "orig"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	updated, err := m.UpdateAppSettings(ctx, "sub1", "rgA", "app1", map[string]string{"FOO": "bar", "BAZ": "qux"})
	if err != nil {
		t.Fatalf("update app settings: %v", err)
	}

	if updated.AppSettings["FOO"] != "bar" || updated.AppSettings["BAZ"] != "qux" {
		t.Fatalf("app settings not updated: %+v", updated.AppSettings)
	}

	// Every other field must survive untouched (a PUT config/appsettings
	// replaces only the app settings, not the whole site).
	if updated.Location != "eastus" {
		t.Fatalf("Location changed by app-settings update: %q", updated.Location)
	}

	if _, err := m.UpdateAppSettings(ctx, "sub1", "rgB", "app1", map[string]string{"FOO": "hijacked"}); !cerrors.IsNotFound(err) {
		t.Fatalf("update from wrong rg = %v, want NotFound", err)
	}

	unchanged, err := m.GetSiteMeta(ctx, "sub1", "rgA", "app1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if unchanged.AppSettings["FOO"] != "bar" {
		t.Fatalf("wrong-rg update leaked through: %+v", unchanged.AppSettings)
	}
}
