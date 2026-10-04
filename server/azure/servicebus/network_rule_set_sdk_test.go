package servicebus_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus/v2"
)

func assertDefaultRuleSet(t *testing.T, p *armservicebus.NetworkRuleSetProperties) {
	t.Helper()

	if p == nil || *p.DefaultAction != armservicebus.DefaultActionAllow ||
		*p.PublicNetworkAccess != armservicebus.PublicNetworkAccessFlagEnabled ||
		*p.TrustedServiceAccessEnabled || p.IPRules == nil || len(p.IPRules) != 0 ||
		p.VirtualNetworkRules == nil || len(p.VirtualNetworkRules) != 0 {
		t.Fatalf("not the default rule set: %+v", p)
	}
}

// TestSDKNamespaceNetworkRuleSet covers networkRuleSets/default, which
// azurerm_servicebus_namespace reads on every refresh.
func TestSDKNamespaceNetworkRuleSet(t *testing.T) {
	ts := pubsubServer(t)
	nc := newClientFactory(t, ts).NewNamespacesClient()
	ctx := context.Background()

	createNS(t, nc, rgName, nsName, nil)

	got, err := nc.GetNetworkRuleSet(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("GET fresh rule set: %v", err)
	}

	if *got.Name != "default" || *got.Type != "Microsoft.ServiceBus/Namespaces/NetworkRuleSets" ||
		*got.ID != nsURL()+"/networkRuleSets/default" {
		t.Fatalf("rule set identity: %s %s %s", *got.ID, *got.Name, *got.Type)
	}

	assertDefaultRuleSet(t, got.Properties)

	put, err := nc.CreateOrUpdateNetworkRuleSet(ctx, rgName, nsName, armservicebus.NetworkRuleSet{
		Properties: &armservicebus.NetworkRuleSetProperties{
			DefaultAction: to.Ptr(armservicebus.DefaultActionDeny),
			IPRules: []*armservicebus.NWRuleSetIPRules{{
				IPMask: to.Ptr("10.0.0.0/24"), Action: to.Ptr(armservicebus.NetworkRuleIPActionAllow),
			}},
			TrustedServiceAccessEnabled: to.Ptr(true),
		},
	}, nil)
	if err != nil || *put.Properties.DefaultAction != armservicebus.DefaultActionDeny {
		t.Fatalf("PUT rule set: %v", err)
	}

	got, err = nc.GetNetworkRuleSet(ctx, rgName, nsName, nil)
	if err != nil || len(got.Properties.IPRules) != 1 || *got.Properties.IPRules[0].IPMask != "10.0.0.0/24" ||
		!*got.Properties.TrustedServiceAccessEnabled {
		t.Fatalf("rule set did not round-trip: %v %+v", err, got.Properties)
	}

	// azurerm's reset sends only defaultAction; every other key is a default.
	if _, err := nc.CreateOrUpdateNetworkRuleSet(ctx, rgName, nsName, armservicebus.NetworkRuleSet{
		Properties: &armservicebus.NetworkRuleSetProperties{DefaultAction: to.Ptr(armservicebus.DefaultActionAllow)},
	}, nil); err != nil {
		t.Fatalf("reset rule set: %v", err)
	}

	got, err = nc.GetNetworkRuleSet(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("GET after reset: %v", err)
	}

	assertDefaultRuleSet(t, got.Properties)

	page, err := nc.NewListNetworkRuleSetsPager(rgName, nsName, nil).NextPage(ctx)
	if err != nil || len(page.Value) != 1 || *page.Value[0].Name != "default" {
		t.Fatalf("list rule sets: %v %+v", err, page.Value)
	}

	// The rule set lives on the namespace record, so a delete and recreate
	// starts from the defaults again.
	if _, err := nc.CreateOrUpdateNetworkRuleSet(ctx, rgName, nsName, armservicebus.NetworkRuleSet{
		Properties: &armservicebus.NetworkRuleSetProperties{DefaultAction: to.Ptr(armservicebus.DefaultActionDeny)},
	}, nil); err != nil {
		t.Fatalf("PUT before delete: %v", err)
	}

	dp, err := nc.BeginDelete(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("delete namespace: %v", err)
	}

	if _, err := dp.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete namespace poll: %v", err)
	}

	createNS(t, nc, rgName, nsName, nil)

	got, err = nc.GetNetworkRuleSet(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("GET after recreate: %v", err)
	}

	assertDefaultRuleSet(t, got.Properties)
}

func TestNamespaceNetworkRuleSetErrors(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	base := nsURL() + "/networkRuleSets/"
	missing := "/subscriptions/" + subID + "/resourceGroups/" + rgName +
		"/providers/Microsoft.ServiceBus/namespaces/nope/networkRuleSets/default"

	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{"bad defaultAction", http.MethodPut, base + "default", `{"properties":{"defaultAction":"Maybe"}}`, 400},
		{"bad publicNetworkAccess", http.MethodPut, base + "default", `{"properties":{"publicNetworkAccess":"x"}}`, 400},
		{"bad ip action", http.MethodPut, base + "default", `{"properties":{"ipRules":[{"ipMask":"1.2.3.4","action":"Deny"}]}}`, 400},
		{"other name", http.MethodGet, base + "foo", "", 404},
		{"delete", http.MethodDelete, base + "default", "", 405},
		{"patch", http.MethodPatch, base + "default", `{}`, 405},
		{"missing namespace", http.MethodGet, missing, "", 404},
		// azurerm's entity authorization rule create polls the geo-DR list.
		{"dr configs list", http.MethodGet, nsURL() + "/disasterRecoveryConfigs", "", 200},
		{"dr config item", http.MethodGet, nsURL() + "/disasterRecoveryConfigs/x", "", 404},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := doRequest(t, srv, tc.method, tc.path+apiVer, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.want, readBody(t, resp))
			}

			_ = resp.Body.Close()
		})
	}
}
