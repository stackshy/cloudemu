package privatedns_test

import (
	"net/http"
	"testing"
)

func TestLinkToMissingVNetRejected(t *testing.T) {
	ts := newServer(t)

	if code, _ := doJSON(t, ts, http.MethodPut, zonePath(zoneName),
		map[string]any{"location": "global"}); code/100 != 2 {
		t.Fatalf("zone create: %d", code)
	}

	vnetBase := "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Network/virtualNetworks/"
	linkPath := zonePath(zoneName) + "/virtualNetworkLinks/link-x"
	link := func(vnet string) map[string]any {
		return map[string]any{
			"location": "global",
			"properties": map[string]any{
				"virtualNetwork":      map[string]any{"id": vnetBase + vnet},
				"registrationEnabled": false,
			},
		}
	}

	code, body := doJSON(t, ts, http.MethodPut, linkPath, link("ghost"))
	if code != http.StatusBadRequest {
		t.Fatalf("link to missing vnet: %d %v, want 400", code, body)
	}

	if code, _ = doJSON(t, ts, http.MethodPut, vnetBase+"real", map[string]any{
		"location":   "eastus",
		"properties": map[string]any{"addressSpace": map[string]any{"addressPrefixes": []string{"10.0.0.0/16"}}},
	}); code/100 != 2 {
		t.Fatalf("vnet create: %d", code)
	}

	if code, body = doJSON(t, ts, http.MethodPut, linkPath, link("real")); code != http.StatusCreated {
		t.Fatalf("link to existing vnet: %d %v, want 201", code, body)
	}
}
