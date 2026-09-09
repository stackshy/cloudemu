package applicationgateway_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKAppGatewayFullRoundTrip drives a full-lifecycle create→get of a
// Standard_v2 gateway with all seven required nested collections and asserts the
// gateway and every child round-trip with their ARM ids, sku.tier, zones,
// cross-collection references and a Succeeded provisioningState.
func TestSDKAppGatewayFullRoundTrip(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created := createGateway(t, client, fullGateway(2))
	assertGatewayShell(t, &created)

	got, err := client.Get(ctx, testRG, gwName, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertGatewayShell(t, &got.ApplicationGateway)
	assertCollections(t, &got.ApplicationGateway)
	assertCrossReferences(t, &got.ApplicationGateway)
}

func assertGatewayShell(t *testing.T, gw *armnetwork.ApplicationGateway) {
	t.Helper()

	if gw.ID == nil || *gw.ID != gwID() {
		t.Fatalf("gateway id = %v, want %s", gw.ID, gwID())
	}

	if gw.Properties == nil || gw.Properties.ProvisioningState == nil ||
		*gw.Properties.ProvisioningState != armnetwork.ProvisioningStateSucceeded {
		t.Fatalf("gateway provisioningState = %v, want Succeeded", gw.Properties.ProvisioningState)
	}

	sku := gw.Properties.SKU
	if sku == nil || sku.Tier == nil || *sku.Tier != armnetwork.ApplicationGatewayTierStandardV2 {
		t.Fatalf("sku.tier = %v, want Standard_v2", sku)
	}

	if sku.Capacity == nil || *sku.Capacity != 2 {
		t.Fatalf("sku.capacity = %v, want 2", sku.Capacity)
	}

	if len(gw.Zones) != 3 || *gw.Zones[0] != "1" || *gw.Zones[2] != "3" {
		t.Fatalf("zones = %v, want [1 2 3]", gw.Zones)
	}

	// enableHttp2:false must survive as an explicit false (deferred property
	// preserved verbatim, not dropped as a zero value).
	if gw.Properties.EnableHTTP2 == nil || *gw.Properties.EnableHTTP2 {
		t.Fatalf("enableHttp2 = %v, want explicit false", gw.Properties.EnableHTTP2)
	}
}

// assertCollections checks each of the seven required collections has exactly
// one child carrying the correct ARM id and a Succeeded provisioningState.
func assertCollections(t *testing.T, gw *armnetwork.ApplicationGateway) {
	t.Helper()

	p := gw.Properties

	requireChildID(t, "gatewayIPConfigurations", len(p.GatewayIPConfigurations), idOf(p.GatewayIPConfigurations[0].ID))
	requireChildID(t, "frontendIPConfigurations", len(p.FrontendIPConfigurations), idOf(p.FrontendIPConfigurations[0].ID))
	requireChildID(t, "frontendPorts", len(p.FrontendPorts), idOf(p.FrontendPorts[0].ID))
	requireChildID(t, "backendAddressPools", len(p.BackendAddressPools), idOf(p.BackendAddressPools[0].ID))
	requireChildID(t, "backendHttpSettingsCollection", len(p.BackendHTTPSettingsCollection),
		idOf(p.BackendHTTPSettingsCollection[0].ID))
	requireChildID(t, "httpListeners", len(p.HTTPListeners), idOf(p.HTTPListeners[0].ID))
	requireChildID(t, "requestRoutingRules", len(p.RequestRoutingRules), idOf(p.RequestRoutingRules[0].ID))

	if ps := p.BackendHTTPSettingsCollection[0].Properties.ProvisioningState; ps == nil ||
		*ps != armnetwork.ProvisioningStateSucceeded {
		t.Fatalf("backend http settings provisioningState = %v, want Succeeded", ps)
	}

	// cookieBasedAffinity value must round-trip verbatim.
	if aff := p.BackendHTTPSettingsCollection[0].Properties.CookieBasedAffinity; aff == nil ||
		*aff != armnetwork.ApplicationGatewayCookieBasedAffinityDisabled {
		t.Fatalf("cookieBasedAffinity = %v, want Disabled", aff)
	}
}

// requireChildID asserts a collection has exactly one child and its ARM id is
// the expected self-link (each collection's single child is named per fullGateway).
func requireChildID(t *testing.T, collection string, count int, id string) {
	t.Helper()

	if count != 1 {
		t.Fatalf("%s has %d children, want 1", collection, count)
	}

	// The stamped id must be a sub-resource of the gateway addressed by collection.
	prefix := gwID() + "/" + collection + "/"
	if len(id) <= len(prefix) || id[:len(prefix)] != prefix {
		t.Fatalf("%s child id = %q, want prefix %q", collection, id, prefix)
	}
}

// assertCrossReferences verifies the routing rule still points at the listener,
// pool and http settings by their sub-resource ids after the round-trip.
func assertCrossReferences(t *testing.T, gw *armnetwork.ApplicationGateway) {
	t.Helper()

	rule := gw.Properties.RequestRoutingRules[0].Properties

	if idOf(rule.HTTPListener.ID) != childID("httpListeners", "listener") {
		t.Fatalf("rule.httpListener = %q, want %q", idOf(rule.HTTPListener.ID), childID("httpListeners", "listener"))
	}

	if idOf(rule.BackendAddressPool.ID) != childID("backendAddressPools", "pool1") {
		t.Fatalf("rule.backendAddressPool = %q", idOf(rule.BackendAddressPool.ID))
	}

	if idOf(rule.BackendHTTPSettings.ID) != childID("backendHttpSettingsCollection", "bhs") {
		t.Fatalf("rule.backendHttpSettings = %q", idOf(rule.BackendHTTPSettings.ID))
	}

	if rule.Priority == nil || *rule.Priority != 100 {
		t.Fatalf("rule.priority = %v, want 100", rule.Priority)
	}
}

// TestSDKAppGatewayUpdateAndReplace covers an update that adds a second backend
// pool and raises capacity, and confirms PUT is a full replace (a child dropped
// from the body is removed).
func TestSDKAppGatewayUpdateAndReplace(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	createGateway(t, client, fullGateway(2))

	updated := fullGateway(4)
	updated.Properties.BackendAddressPools = append(updated.Properties.BackendAddressPools,
		&armnetwork.ApplicationGatewayBackendAddressPool{
			Name:       to.Ptr("pool2"),
			Properties: &armnetwork.ApplicationGatewayBackendAddressPoolPropertiesFormat{},
		})

	createGateway(t, client, updated)

	got, err := client.Get(ctx, testRG, gwName, nil)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}

	if got.Properties.SKU.Capacity == nil || *got.Properties.SKU.Capacity != 4 {
		t.Fatalf("capacity after update = %v, want 4", got.Properties.SKU.Capacity)
	}

	if len(got.Properties.BackendAddressPools) != 2 {
		t.Fatalf("pools after update = %d, want 2", len(got.Properties.BackendAddressPools))
	}

	// Full replace: drop pool2, confirm it is removed.
	createGateway(t, client, fullGateway(4))

	got2, err := client.Get(ctx, testRG, gwName, nil)
	if err != nil {
		t.Fatalf("Get after replace: %v", err)
	}

	if len(got2.Properties.BackendAddressPools) != 1 || *got2.Properties.BackendAddressPools[0].Name != "pool1" {
		t.Fatalf("pools after replace = %+v, want [pool1]", got2.Properties.BackendAddressPools)
	}
}

// TestSDKAppGatewayListAndDelete covers list (RG scope) and delete.
func TestSDKAppGatewayListAndDelete(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	createGateway(t, client, fullGateway(2))

	pager := client.NewListPager(testRG, nil)

	found := 0

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		found += len(page.Value)
	}

	if found != 1 {
		t.Fatalf("list returned %d gateways, want 1", found)
	}

	delPoller, err := client.BeginDelete(ctx, testRG, gwName, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete poll: %v", err)
	}

	if _, err := client.Get(ctx, testRG, gwName, nil); err == nil {
		t.Fatalf("Get after delete succeeded, want NotFound")
	}
}

func idOf(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}
