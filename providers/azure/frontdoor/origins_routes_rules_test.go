package frontdoor_test

import (
	"context"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/frontdoor"
	"github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// The rules below are the Microsoft.Cdn 2024-02-01 schema
// (Microsoft.Cdn/stable/2024-02-01/afdx.json) and Azure's documented dependency
// refusals. They are enforced by the provider, so the Go library and the wire
// server behave the same.

func putOrigin(ctx context.Context, m *frontdoor.Mock, name string, props map[string]any) error {
	_, _, err := m.CreateOrUpdateOrigin(ctx, rg, profile, originGroup, name, driver.AzureFrontDoorOrigin{Properties: props})

	return err
}

func putRoute(ctx context.Context, m *frontdoor.Mock, name string, props map[string]any) error {
	_, _, err := m.CreateOrUpdateRoute(ctx, rg, profile, endpoint, name,
		driver.AzureFrontDoorRoute{OriginGroup: originGroup, Properties: props})

	return err
}

func TestOriginValidation(t *testing.T) {
	m, ctx := seedChain(t)

	bad := map[string]map[string]any{
		"no hostName":            {"weight": 10},
		"empty hostName":         {"hostName": ""},
		"weight 0":               {"hostName": "h", "weight": 0},
		"weight 1001":            {"hostName": "h", "weight": 1001},
		"priority 6":             {"hostName": "h", "priority": 6},
		"priority 0 (float)":     {"hostName": "h", "priority": float64(0)},
		"httpPort 65536":         {"hostName": "h", "httpPort": 65536},
		"httpsPort fractional":   {"hostName": "h", "httpsPort": 443.5},
		"httpPort string":        {"hostName": "h", "httpPort": "80"},
		"enabledState bogus":     {"hostName": "h", "enabledState": "Maybe"},
		"enforceCert not a bool": {"hostName": "h", "enforceCertificateNameCheck": "yes"},
	}

	for name, props := range bad {
		if err := putOrigin(ctx, m, "o-bad", props); !cerrors.IsInvalidArgument(err) {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}

	// The same rules apply to the merge a PATCH produces.
	if _, err := m.UpdateOrigin(ctx, rg, profile, originGroup, origin, map[string]any{"weight": 0}); !cerrors.IsInvalidArgument(err) {
		t.Errorf("patch weight 0: err = %v, want InvalidArgument", err)
	}

	if _, err := m.UpdateOrigin(ctx, rg, profile, originGroup, origin, map[string]any{"hostName": nil}); !cerrors.IsInvalidArgument(err) {
		t.Errorf("patch hostName null: err = %v, want InvalidArgument", err)
	}

	// Go-typed integers in range are accepted.
	requireNoError(t, putOrigin(ctx, m, "o-ok", map[string]any{
		"hostName": "h", "weight": int32(1000), "priority": int64(5), "httpPort": 1, "httpsPort": 65535,
	}), "in-range origin")
}

func TestOriginDefaults(t *testing.T) {
	m, ctx := seedChain(t)

	o, err := m.GetOrigin(ctx, rg, profile, originGroup, origin)
	requireNoError(t, err, "get origin")

	want := map[string]any{"httpPort": 80, "httpsPort": 443, "enforceCertificateNameCheck": true, "enabledState": "Enabled"}
	for k, v := range want {
		if o.Properties[k] != v {
			t.Errorf("origin %s = %v, want %v", k, o.Properties[k], v)
		}
	}

	// A supplied value is kept, not overwritten by the default.
	requireNoError(t, putOrigin(ctx, m, "o-2", map[string]any{
		"hostName": "h", "httpPort": 8080, "enforceCertificateNameCheck": false,
	}), "origin with explicit values")

	o, err = m.GetOrigin(ctx, rg, profile, originGroup, "o-2")
	requireNoError(t, err, "get o-2")

	if o.Properties["httpPort"] != 8080 || o.Properties["enforceCertificateNameCheck"] != false {
		t.Errorf("explicit values overwritten: %v", o.Properties)
	}
}

func TestRouteValidation(t *testing.T) {
	m, ctx := seedChain(t)

	bad := map[string]map[string]any{
		"forwardingProtocol bogus":  {"forwardingProtocol": "bogus"},
		"httpsRedirect bogus":       {"httpsRedirect": "Sometimes"},
		"linkToDefaultDomain bogus": {"linkToDefaultDomain": 1},
		"enabledState bogus":        {"enabledState": "On"},
		"protocol Ftp":              {"supportedProtocols": []any{"Http", "Ftp"}},
		"protocols not a list":      {"supportedProtocols": "Http"},
		"pattern without slash":     {"patternsToMatch": []any{"api/*"}},
		"pattern not a string":      {"patternsToMatch": []any{7}},
		"customDomains reference": {"customDomains": []any{map[string]any{
			"id": "/subscriptions/s/resourceGroups/rg-1/providers/Microsoft.Cdn/profiles/profile-1/customDomains/www",
		}}},
		"ruleSets reference": {"ruleSets": []any{map[string]any{
			"id": "/subscriptions/s/resourceGroups/rg-1/providers/Microsoft.Cdn/profiles/profile-1/ruleSets/rs",
		}}},
		"customDomains not a list": {"customDomains": "www"},
	}

	for name, props := range bad {
		if err := putRoute(ctx, m, "r-bad", props); !cerrors.IsInvalidArgument(err) {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}

	// Empty reference lists are what Terraform sends when none are set.
	requireNoError(t, putRoute(ctx, m, "r-ok", map[string]any{
		"customDomains": []any{}, "ruleSets": []any{}, "patternsToMatch": []any{"/api/*"},
	}), "route with empty reference lists")

	// A PATCH merge is re-validated.
	patch := driver.AzureFrontDoorRoute{Properties: map[string]any{"patternsToMatch": []any{"nope"}}}
	if _, err := m.UpdateRoute(ctx, rg, profile, endpoint, route, patch); !cerrors.IsInvalidArgument(err) {
		t.Errorf("patch bad pattern: err = %v, want InvalidArgument", err)
	}
}

func TestRouteDefaults(t *testing.T) {
	m, ctx := seedChain(t)

	r, err := m.GetRoute(ctx, rg, profile, endpoint, route)
	requireNoError(t, err, "get route")

	want := map[string]any{
		"forwardingProtocol": "MatchRequest", "httpsRedirect": "Disabled",
		"linkToDefaultDomain": "Disabled", "enabledState": "Enabled",
	}
	for k, v := range want {
		if r.Properties[k] != v {
			t.Errorf("route %s = %v, want %v", k, r.Properties[k], v)
		}
	}

	protocols, _ := r.Properties["supportedProtocols"].([]any)
	if len(protocols) != 2 || protocols[0] != "Http" || protocols[1] != "Https" {
		t.Errorf("route supportedProtocols = %v, want [Http Https]", r.Properties["supportedProtocols"])
	}
}

func TestLastEnabledOriginOfRoutedGroupIsKept(t *testing.T) {
	m, ctx := seedChain(t)

	// origin-1 is the only origin of og-1, which route-1 forwards to.
	err := m.DeleteOrigin(ctx, rg, profile, originGroup, origin)
	if !cerrors.IsFailedPrecondition(err) || !strings.Contains(cerrors.Message(err), "Cannot disable or delete the last origin") {
		t.Fatalf("delete last origin err = %v, want FailedPrecondition with Azure's message", err)
	}

	_, err = m.UpdateOrigin(ctx, rg, profile, originGroup, origin, map[string]any{"enabledState": "Disabled"})
	if !cerrors.IsFailedPrecondition(err) {
		t.Errorf("patch-disable last origin err = %v, want FailedPrecondition", err)
	}

	err = putOrigin(ctx, m, origin, map[string]any{"hostName": "app.example.net", "enabledState": "Disabled"})
	if !cerrors.IsFailedPrecondition(err) {
		t.Errorf("put-disable last origin err = %v, want FailedPrecondition", err)
	}

	// A disabled second origin does not count as a remaining enabled one.
	requireNoError(t, putOrigin(ctx, m, "o-off", map[string]any{"hostName": "b", "enabledState": "Disabled"}),
		"add disabled origin")

	if err := m.DeleteOrigin(ctx, rg, profile, originGroup, origin); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("delete with only a disabled sibling err = %v, want FailedPrecondition", err)
	}

	// Removing a disabled origin never strands the group.
	requireNoError(t, m.DeleteOrigin(ctx, rg, profile, originGroup, "o-off"), "delete disabled origin")

	// With a second enabled origin, the first can be disabled and then deleted.
	requireNoError(t, putOrigin(ctx, m, "o-2", map[string]any{"hostName": "c"}), "add enabled origin")

	_, err = m.UpdateOrigin(ctx, rg, profile, originGroup, origin, map[string]any{"enabledState": "Disabled"})
	requireNoError(t, err, "disable non-last origin")
	requireNoError(t, m.DeleteOrigin(ctx, rg, profile, originGroup, origin), "delete non-last origin")

	// The refusal is state-driven: the survivor is protected again.
	if err := m.DeleteOrigin(ctx, rg, profile, originGroup, "o-2"); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("delete new last origin err = %v, want FailedPrecondition", err)
	}
}

func TestRouteDomainProtocolPathConflict(t *testing.T) {
	m, ctx := seedChain(t)

	onDefault := func(patterns ...any) map[string]any {
		return map[string]any{"linkToDefaultDomain": "Enabled", "patternsToMatch": patterns}
	}

	requireNoError(t, putRoute(ctx, m, "r1", onDefault("/*")), "create r1")

	err := putRoute(ctx, m, "r2", onDefault("/*"))
	if !cerrors.IsFailedPrecondition(err) ||
		!strings.Contains(cerrors.Message(err), "The route domains, paths and protocols configuration has a conflict") {
		t.Fatalf("duplicate route err = %v, want FailedPrecondition with Azure's message", err)
	}

	// Overlap on one protocol is still a conflict.
	props := onDefault("/*")
	props["supportedProtocols"] = []any{"Https"}

	if err := putRoute(ctx, m, "r2", props); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("https-only duplicate err = %v, want FailedPrecondition", err)
	}

	// A different path, or a route not on the default domain, does not conflict.
	requireNoError(t, putRoute(ctx, m, "r2", onDefault("/api/*")), "create r2 on another path")
	requireNoError(t, putRoute(ctx, m, "r3", map[string]any{"patternsToMatch": []any{"/*"}}), "route off default domain")

	// Re-putting r1 with its own claims is not a conflict with itself.
	requireNoError(t, putRoute(ctx, m, "r1", onDefault("/*")), "replace r1")

	// A PATCH that moves r2 onto r1's path is refused after the merge.
	patch := driver.AzureFrontDoorRoute{Properties: map[string]any{"patternsToMatch": []any{"/*"}}}
	if _, err := m.UpdateRoute(ctx, rg, profile, endpoint, "r2", patch); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("patch into conflict err = %v, want FailedPrecondition", err)
	}

	// The same path on another endpoint is a different domain.
	_, _, err = m.CreateOrUpdateEndpoint(ctx, rg, profile, "ep-2", driver.AzureFrontDoorEndpoint{})
	requireNoError(t, err, "create ep-2")

	_, _, err = m.CreateOrUpdateRoute(ctx, rg, profile, "ep-2", "r1",
		driver.AzureFrontDoorRoute{OriginGroup: originGroup, Properties: onDefault("/*")})
	requireNoError(t, err, "same path on another endpoint")
}

func TestUpdateRouteRepointsOriginGroup(t *testing.T) {
	m, ctx := seedChain(t)

	_, err := m.UpdateRoute(ctx, rg, profile, endpoint, route, driver.AzureFrontDoorRoute{OriginGroup: "missing"})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("repoint at missing group err = %v, want InvalidArgument", err)
	}

	_, _, err = m.CreateOrUpdateOriginGroup(ctx, rg, profile, "og-2", driver.AzureFrontDoorOriginGroup{})
	requireNoError(t, err, "create og-2")

	r, err := m.UpdateRoute(ctx, rg, profile, endpoint, route, driver.AzureFrontDoorRoute{OriginGroup: "OG-2"})
	requireNoError(t, err, "repoint at og-2")

	if r.OriginGroup != "og-2" {
		t.Errorf("repointed OriginGroup = %q, want the stored casing og-2", r.OriginGroup)
	}

	// An empty OriginGroup keeps the stored one; unpatched properties survive.
	r, err = m.UpdateRoute(ctx, rg, profile, endpoint, route,
		driver.AzureFrontDoorRoute{Properties: map[string]any{"httpsRedirect": "Enabled"}})
	requireNoError(t, err, "patch httpsRedirect")

	if r.OriginGroup != "og-2" || r.Properties["httpsRedirect"] != "Enabled" {
		t.Errorf("patched route = %+v", r)
	}

	if p, _ := r.Properties["patternsToMatch"].([]any); len(p) != 1 || p[0] != "/*" {
		t.Errorf("patch dropped patternsToMatch: %v", r.Properties)
	}

	// og-1 is free now, so its last origin may be deleted.
	requireNoError(t, m.DeleteOrigin(ctx, rg, profile, originGroup, origin), "delete origin of unrouted group")

	if _, err := m.UpdateRoute(ctx, rg, profile, endpoint, "missing", driver.AzureFrontDoorRoute{}); !cerrors.IsNotFound(err) {
		t.Errorf("patch missing route err = %v, want NotFound", err)
	}

	if _, err := m.UpdateOrigin(ctx, rg, profile, originGroup, "missing", nil); !cerrors.IsNotFound(err) {
		t.Errorf("patch missing origin err = %v, want NotFound", err)
	}
}

func TestETagRotatesOnWrite(t *testing.T) {
	m, ctx := seedChain(t)

	o1, err := m.GetOrigin(ctx, rg, profile, originGroup, origin)
	requireNoError(t, err, "get origin")

	o2, _, err := m.CreateOrUpdateOrigin(ctx, rg, profile, originGroup, origin,
		driver.AzureFrontDoorOrigin{Properties: map[string]any{"hostName": "x"}})
	requireNoError(t, err, "replace origin")

	o3, err := m.UpdateOrigin(ctx, rg, profile, originGroup, origin, map[string]any{"weight": 5})
	requireNoError(t, err, "patch origin")

	o4, err := m.GetOrigin(ctx, rg, profile, originGroup, origin)
	requireNoError(t, err, "re-get origin")

	if o1.ETag == "" || o1.ETag == o2.ETag || o2.ETag == o3.ETag || o3.ETag != o4.ETag {
		t.Errorf("origin etags = %q, %q, %q, %q: want set, rotating on write, stable on read",
			o1.ETag, o2.ETag, o3.ETag, o4.ETag)
	}

	r1, err := m.GetRoute(ctx, rg, profile, endpoint, route)
	requireNoError(t, err, "get route")

	r2, err := m.UpdateRoute(ctx, rg, profile, endpoint, route,
		driver.AzureFrontDoorRoute{Properties: map[string]any{"httpsRedirect": "Enabled"}})
	requireNoError(t, err, "patch route")

	if r1.ETag == "" || r1.ETag == r2.ETag {
		t.Errorf("route etags = %q, %q: want set and rotating", r1.ETag, r2.ETag)
	}
}

func TestStoredCasingIsKept(t *testing.T) {
	m, ctx := seedChain(t)

	// Addressed in a different casing, a replace keeps the name it was created with
	// and the parent names come from the stored parents.
	o, _, err := m.CreateOrUpdateOrigin(ctx, strings.ToUpper(rg), strings.ToUpper(profile),
		strings.ToUpper(originGroup), strings.ToUpper(origin),
		driver.AzureFrontDoorOrigin{Properties: map[string]any{"hostName": "x"}})
	requireNoError(t, err, "replace origin in upper case")

	if o.Name != origin || o.OriginGroup != originGroup || o.Profile != profile || o.ResourceGroup != rg {
		t.Errorf("origin casing = %+v", o)
	}

	r, _, err := m.CreateOrUpdateRoute(ctx, rg, strings.ToUpper(profile), strings.ToUpper(endpoint),
		strings.ToUpper(route), driver.AzureFrontDoorRoute{OriginGroup: strings.ToUpper(originGroup)})
	requireNoError(t, err, "replace route in upper case")

	if r.Name != route || r.Endpoint != endpoint || r.Profile != profile || r.OriginGroup != originGroup {
		t.Errorf("route casing = %+v", r)
	}
}
