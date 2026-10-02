package apimanagement_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/apimanagement"
)

func props(t *testing.T, s *apimanagement.Service) map[string]any {
	t.Helper()

	out := map[string]any{}
	if err := json.Unmarshal(s.Properties, &out); err != nil {
		t.Fatalf("properties: %v", err)
	}

	return out
}

// TestLibraryServiceCarriesDefaultsAndComputed: the provider's service holds
// Azure's defaults and the computed fields, re-derived on every write (a move to
// Consumption drops the dedicated-tier endpoints), and a caller value for a
// defaulted field wins.
func TestLibraryServiceCarriesDefaultsAndComputed(t *testing.T) {
	m, _ := newMock()
	s := create(t, m, "apim1")
	p := props(t, &s)

	want := map[string]any{
		"virtualNetworkType":      "None",
		"publicNetworkAccess":     "Enabled",
		"notificationSenderEmail": "apimgmt-noreply@mail.windowsazure.com",
		"platformVersion":         "stv2",
		"gatewayRegionalUrl":      "https://apim1-eastus-01.regional.azure-api.net",
		"portalUrl":               "https://apim1.portal.azure-api.net",
		"provisioningState":       "Succeeded",
		"createdAtUtc":            "2026-09-01T10:30:00Z",
		"restore":                 false,
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("%s = %v, want %v", k, p[k], v)
		}
	}

	in := devInput()
	in.Properties = json.RawMessage(`{"publisherEmail":"a@b.test","publisherName":"C","virtualNetworkType":"External",` +
		`"provisioningState":"Failed"}`)

	s, _, err := m.CreateOrUpdateService(context.Background(), sub, rg, "apim1", "East US", in)
	if err != nil {
		t.Fatal(err)
	}

	if p := props(t, &s); p["virtualNetworkType"] != "External" || p["provisioningState"] != "Succeeded" {
		t.Errorf("caller default must win and computed must not: %v", p)
	}

	s, err = m.UpdateService(context.Background(), sub, rg, "apim1", &apimanagement.ServiceInput{
		SkuName: sptr("Consumption"), SkuCapacity: i32(0),
	})
	if err != nil {
		t.Fatal(err)
	}

	if p := props(t, &s); p["portalUrl"] != nil || p["platformVersion"] != "mtv1" {
		t.Errorf("Consumption must drop the dedicated endpoints: %v", p)
	}
}

// TestSKUCeilingsAndZones covers each tier's unit ceiling and the Premium-only
// zones rule, on create and on PATCH.
func TestSKUCeilingsAndZones(t *testing.T) {
	ctx := context.Background()

	for sku, maxUnits := range map[string]int32{
		"Developer": 1, "Basic": 2, "Standard": 4, "Premium": 12, "BasicV2": 10, "StandardV2": 10,
	} {
		m, _ := newMock()
		in := devInput()
		in.SkuName, in.SkuCapacity = sptr(sku), i32(maxUnits+1)

		if _, _, err := m.CreateOrUpdateService(ctx, sub, rg, "svc", "eastus", in); !cerrors.IsInvalidArgument(err) {
			t.Errorf("%s capacity %d: err = %v, want InvalidArgument", sku, maxUnits+1, err)
		}

		in.SkuCapacity = i32(maxUnits)
		if _, _, err := m.CreateOrUpdateService(ctx, sub, rg, "svc", "eastus", in); err != nil {
			t.Errorf("%s capacity %d: %v", sku, maxUnits, err)
		}
	}

	m, _ := newMock()
	in := devInput()
	in.SkuName, in.SkuCapacity = sptr("Isolated"), i32(20)

	if _, _, err := m.CreateOrUpdateService(ctx, sub, rg, "iso", "eastus", in); err != nil {
		t.Errorf("Isolated has no emulator ceiling: %v", err)
	}

	in = devInput()
	in.Zones = []string{"1"}

	if _, _, err := m.CreateOrUpdateService(ctx, sub, rg, "zonal", "eastus", in); !cerrors.IsInvalidArgument(err) {
		t.Errorf("zones on Developer: err = %v", err)
	}

	create(t, m, "dev")

	if _, err := m.UpdateService(ctx, sub, rg, "dev", &apimanagement.ServiceInput{Zones: []string{"1"}}); !cerrors.IsInvalidArgument(err) {
		t.Errorf("PATCH zones on Developer: err = %v", err)
	}
}

// TestNameLocationAndIfMatch covers the global name, the immutable location and
// the conditional writes.
func TestNameLocationAndIfMatch(t *testing.T) {
	m, _ := newMock()
	ctx := context.Background()
	s := create(t, m, "apim1")

	if _, _, err := m.CreateOrUpdateService(ctx, "sub2", "rg2", "APIM1", "eastus", devInput()); !errors.Is(err, apimanagement.ErrNameNotAvailable) ||
		!cerrors.IsAlreadyExists(err) {
		t.Errorf("same name elsewhere: err = %v", err)
	}

	if _, _, err := m.CreateOrUpdateService(ctx, sub, rg, "apim1", "westeurope", devInput()); !errors.Is(err, apimanagement.ErrLocationMismatch) {
		t.Errorf("location change: err = %v", err)
	}

	if v := m.CheckNameAvailability(ctx, "apim1"); v.Available || v.Reason != "AlreadyExists" {
		t.Errorf("taken name = %+v", v)
	}

	if v := m.CheckNameAvailability(ctx, "-bad"); v.Available || v.Reason != "Invalid" || v.Message == "" {
		t.Errorf("invalid name = %+v", v)
	}

	stale := devInput()
	stale.IfMatch = `W/"not-it"`

	if _, _, err := m.CreateOrUpdateService(ctx, sub, rg, "apim1", "eastus", stale); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("stale If-Match PUT: err = %v", err)
	}

	if _, _, err := m.CreateOrUpdateService(ctx, sub, rg, "fresh", "eastus", stale); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("If-Match on a missing service: err = %v", err)
	}

	if _, err := m.UpdateService(ctx, sub, rg, "apim1", &apimanagement.ServiceInput{IfMatch: "x"}); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("stale If-Match PATCH: err = %v", err)
	}

	cur := devInput()
	cur.IfMatch = `"` + s.Etag + `"`

	s2, _, err := m.CreateOrUpdateService(ctx, sub, rg, "apim1", "eastus", cur)
	if err != nil || s2.Etag == s.Etag {
		t.Fatalf("current If-Match PUT: %v (etag %q)", err, s2.Etag)
	}

	if _, err := m.DeleteServiceIfMatch(ctx, sub, rg, "apim1", s.Etag); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("stale If-Match DELETE: err = %v", err)
	}

	if existed, err := m.DeleteService(ctx, sub, rg, "apim1"); err != nil || !existed {
		t.Errorf("DELETE: %v %v", existed, err)
	}

	if existed, err := m.DeleteService(ctx, sub, rg, "apim1"); err != nil || existed {
		t.Errorf("second DELETE: %v %v", existed, err)
	}
}

// TestSoftDeleteRetentionPurgeAndRestore covers the soft-deleted lifecycle.
func TestSoftDeleteRetentionPurgeAndRestore(t *testing.T) {
	m, clk := newMock()
	ctx := context.Background()
	create(t, m, "apim1")

	if _, err := m.DeleteService(ctx, sub, rg, "apim1"); err != nil {
		t.Fatal(err)
	}

	d, err := m.GetDeletedService(ctx, sub, "East US", "apim1")
	if err != nil || !d.ScheduledPurgeDate.Equal(epoch.Add(48*time.Hour)) ||
		d.ARMID() != "/subscriptions/sub/providers/Microsoft.ApiManagement/locations/eastus/deletedservices/apim1" {
		t.Fatalf("deleted = %+v %q, %v", d, d.ARMID(), err)
	}

	if _, _, err := m.CreateOrUpdateService(ctx, sub, rg, "apim1", "eastus", devInput()); !errors.Is(err, apimanagement.ErrSoftDeleted) {
		t.Errorf("create over a soft-deleted name: err = %v", err)
	}

	restore := &apimanagement.ServiceInput{Properties: json.RawMessage(`{"restore":true}`)}

	if _, _, err := m.CreateOrUpdateService(ctx, sub, rg, "apim1", "westus", restore); !cerrors.IsNotFound(err) {
		t.Errorf("restore in the wrong location: err = %v", err)
	}

	got, created, err := m.CreateOrUpdateService(ctx, sub, rg, "apim1", "eastus", restore)
	if err != nil || !created || got.SkuName != "Developer" {
		t.Fatalf("restore: %+v %v %v", got, created, err)
	}

	if apis, err := m.ListAPIs(ctx, sub, rg, "apim1"); err != nil || len(apis) != 1 {
		t.Errorf("restore must bring the children back: %v %v", apis, err)
	}

	if _, err := m.DeleteService(ctx, sub, rg, "apim1"); err != nil {
		t.Fatal(err)
	}

	if list, _ := m.ListDeletedServices(ctx, sub); len(list) != 1 {
		t.Errorf("deleted list = %v", list)
	}

	if list, _ := m.ListDeletedServices(ctx, "other"); len(list) != 0 {
		t.Errorf("other subscription's deleted list = %v", list)
	}

	clk.Advance(48 * time.Hour)

	if _, err := m.GetDeletedService(ctx, sub, "eastus", "apim1"); !cerrors.IsNotFound(err) {
		t.Errorf("retention lapsed: err = %v", err)
	}

	if _, err := m.PurgeDeletedService(ctx, sub, "eastus", "apim1"); !cerrors.IsNotFound(err) {
		t.Errorf("purge after lapse: err = %v", err)
	}

	create(t, m, "apim1")

	if err := m.PurgeResourceGroup(ctx, sub, rg); err != nil {
		t.Fatal(err)
	}

	if _, err := m.PurgeDeletedService(ctx, sub, "eastus", "apim1"); err != nil {
		t.Errorf("a group delete soft-deletes; purge: %v", err)
	}
}

// TestChildResources covers the child surface's error paths through the
// library.
func TestChildResources(t *testing.T) {
	m, _ := newMock()
	ctx := context.Background()
	create(t, m, "apim1")

	if _, err := m.ListAPIs(ctx, sub, rg, "missing"); !cerrors.IsNotFound(err) {
		t.Errorf("children of a missing service: %v", err)
	}

	p, err := m.GetProduct(ctx, sub, rg, "apim1", "Starter")
	if err != nil || p.Name != "starter" {
		t.Fatalf("GetProduct: %+v %v", p, err)
	}

	if _, err := m.GetAPI(ctx, sub, rg, "apim1", "nope"); !cerrors.IsNotFound(err) {
		t.Errorf("missing api: %v", err)
	}

	if _, err := m.DeleteProduct(ctx, sub, rg, "apim1", "starter", `"stale"`); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("stale product delete: %v", err)
	}

	if ok, err := m.DeleteProduct(ctx, sub, rg, "apim1", "starter", `"`+p.Etag+`"`); !ok || err != nil {
		t.Errorf("product delete: %v %v", ok, err)
	}

	if ok, err := m.DeleteAPI(ctx, sub, rg, "apim1", "nope", ""); ok || err != nil {
		t.Errorf("missing api delete: %v %v", ok, err)
	}

	for _, tc := range []struct{ value, format string }{
		{"<policies/>", "xml-link"}, {"", "xml"}, {"<a>", "rawxml"},
	} {
		if _, _, err := m.PutPolicy(ctx, sub, rg, "apim1", tc.value, tc.format, ""); !cerrors.IsInvalidArgument(err) {
			t.Errorf("policy %q/%q: err = %v", tc.value, tc.format, err)
		}
	}

	if _, _, err := m.PutPolicy(ctx, sub, rg, "apim1", "<policies/>", "", `"x"`); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("If-Match on a missing policy: %v", err)
	}

	pol, created, err := m.PutPolicy(ctx, sub, rg, "apim1", "<policies/>", "rawxml", "*")
	if err != nil || !created || !strings.Contains(string(pol.Properties), `"rawxml"`) {
		t.Fatalf("PutPolicy rawxml: %s %v %v", pol.Properties, created, err)
	}

	if ok, err := m.DeletePolicy(ctx, sub, rg, "apim1", ""); !ok || err != nil {
		t.Errorf("DeletePolicy: %v %v", ok, err)
	}

	if ok, err := m.DeletePolicy(ctx, sub, rg, "apim1", ""); ok || err != nil {
		t.Errorf("second DeletePolicy: %v %v", ok, err)
	}

	if _, err := m.GetPortalSetting(ctx, sub, rg, "apim1", "nope"); !cerrors.IsNotFound(err) {
		t.Errorf("missing portal setting: %v", err)
	}

	if _, err := m.PutPortalSetting(ctx, sub, rg, "apim1", apimanagement.PortalSignIn, json.RawMessage(`[]`), ""); !cerrors.IsInvalidArgument(err) {
		t.Errorf("non-object portal setting: %v", err)
	}

	if _, err := m.PutPortalSetting(ctx, sub, rg, "apim1", apimanagement.PortalSignIn, json.RawMessage(`{"enabled":true}`), `"x"`); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("stale portal setting: %v", err)
	}

	if key, err := m.DelegationValidationKey(ctx, sub, rg, "apim1"); key != "" || err != nil {
		t.Errorf("default delegation key = %q %v", key, err)
	}

	if _, err := m.GetTenantAccess(ctx, sub, rg, "apim1", "nope"); !cerrors.IsNotFound(err) {
		t.Errorf("missing tenant access: %v", err)
	}

	if _, err := m.UpdateTenantAccess(ctx, sub, rg, "apim1", "access", nil, `"x"`); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("stale tenant access: %v", err)
	}

	git, err := m.GetTenantAccess(ctx, sub, rg, "apim1", "GITACCESS")
	if err != nil || git.PrincipalID != "git" || git.PrimaryKey == "" {
		t.Errorf("gitAccess = %+v %v", git, err)
	}

	in := devInput()
	in.SkuName, in.SkuCapacity = sptr("BasicV2"), i32(1)

	if _, _, err := m.CreateOrUpdateService(ctx, sub, rg, "v2", "eastus", in); err != nil {
		t.Fatal(err)
	}

	for _, err := range []error{
		func() error { _, e := m.GetPortalSetting(ctx, sub, rg, "v2", "signin"); return e }(),
		func() error { _, e := m.GetTenantAccess(ctx, sub, rg, "v2", "access"); return e }(),
	} {
		if !errors.Is(err, apimanagement.ErrTierNotSupported) {
			t.Errorf("v2 tier: err = %v", err)
		}
	}
}

// TestSnapshotRoundTripsChildrenAndDeleted: a snapshot carries the child
// resources and the soft-deleted services, and a restore re-materializes a
// service written before the provider owned the computed fields.
func TestSnapshotRoundTripsChildrenAndDeleted(t *testing.T) {
	m, _ := newMock()
	ctx := context.Background()
	create(t, m, "live")
	create(t, m, "gone")

	if _, _, err := m.PutPolicy(ctx, sub, rg, "live", "<policies/>", "xml", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := m.DeleteService(ctx, sub, rg, "gone"); err != nil {
		t.Fatal(err)
	}

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	r, _ := newMock()
	if err := r.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	if _, err := r.GetPolicy(ctx, sub, rg, "live"); err != nil {
		t.Errorf("policy lost across snapshot: %v", err)
	}

	if _, err := r.GetDeletedService(ctx, sub, "eastus", "gone"); err != nil {
		t.Errorf("soft-deleted service lost across snapshot: %v", err)
	}

	legacy := `{"services":{"/subscriptions/sub/resourcegroups/rg/providers/microsoft.apimanagement/service/old":{"subscription":"sub","resourceGroup":"rg","name":"old","location":"eastus",` +
		`"skuName":"Developer","skuCapacity":1,"properties":{"publisherEmail":"a@b","publisherName":"n"},` +
		`"provisioningState":"Succeeded","etag":"e","createdAt":"2026-01-01T00:00:00Z"}}}`

	l, _ := newMock()
	if err := l.Restore(ctx, json.RawMessage(legacy)); err != nil {
		t.Fatal(err)
	}

	s, err := l.GetService(ctx, "sub", "rg", "old")
	if err != nil || props(t, &s)["gatewayRegionalUrl"] == nil {
		t.Errorf("legacy snapshot not re-materialized: %s %v", s.Properties, err)
	}

	if apis, err := l.ListAPIs(ctx, "sub", "rg", "old"); err != nil || len(apis) != 0 {
		t.Errorf("a legacy service gets default settings, not sample APIs: %v %v", apis, err)
	}

	if _, err := l.GetPortalSetting(ctx, "sub", "rg", "old", "signup"); err != nil {
		t.Errorf("a legacy service gets default portal settings: %v", err)
	}

	if err := l.Restore(ctx, nil); err != nil {
		t.Errorf("empty restore: %v", err)
	}

	if err := l.Restore(ctx, json.RawMessage(`{`)); err == nil {
		t.Error("malformed snapshot must fail")
	}
}

// TestPortalAndTenantWrites covers the successful portal-setting and tenant
// access writes: the etag rotates and the delegation key never leaves via GET.
func TestPortalAndTenantWrites(t *testing.T) {
	m, _ := newMock()
	ctx := context.Background()
	create(t, m, "apim1")

	if products, err := m.ListProducts(ctx, sub, rg, "apim1"); err != nil || len(products) != 2 {
		t.Errorf("products = %v %v", products, err)
	}

	if _, err := m.GetPolicy(ctx, sub, rg, "missing"); !cerrors.IsNotFound(err) {
		t.Errorf("policy of a missing service: %v", err)
	}

	before, err := m.GetPortalSetting(ctx, sub, rg, "apim1", apimanagement.PortalDelegation)
	if err != nil {
		t.Fatal(err)
	}

	after, err := m.PutPortalSetting(ctx, sub, rg, "apim1", apimanagement.PortalDelegation,
		json.RawMessage(`{"url":"https://d.test","validationKey":"k1"}`), `"`+before.Etag+`"`)
	if err != nil || after.Etag == before.Etag || strings.Contains(string(after.Properties), "k1") {
		t.Fatalf("PUT delegation: %s %v", after.Properties, err)
	}

	if got, _ := m.GetPortalSetting(ctx, sub, rg, "apim1", apimanagement.PortalDelegation); strings.Contains(string(got.Properties), "k1") {
		t.Errorf("GET must not reveal the key: %s", got.Properties)
	}

	if key, err := m.DelegationValidationKey(ctx, sub, rg, "apim1"); key != "k1" || err != nil {
		t.Errorf("listSecrets key = %q %v", key, err)
	}

	ta, err := m.GetTenantAccess(ctx, sub, rg, "apim1", apimanagement.TenantAccessName)
	if err != nil || ta.Enabled {
		t.Fatalf("tenant access = %+v %v", ta, err)
	}

	on := true

	upd, err := m.UpdateTenantAccess(ctx, sub, rg, "apim1", apimanagement.TenantAccessName, &on, ta.Etag)
	if err != nil || !upd.Enabled || upd.Etag == ta.Etag {
		t.Errorf("tenant access update = %+v %v", upd, err)
	}
}
