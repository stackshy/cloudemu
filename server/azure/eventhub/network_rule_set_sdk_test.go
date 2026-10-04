package eventhub_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub"
)

// TestSDKEventHubNamespaceNetworkRuleSet covers networkRuleSets/default, which
// azurerm_eventhub_namespace reads on every refresh, Basic SKU included.
func TestSDKEventHubNamespaceNetworkRuleSet(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	nc, err := armeventhub.NewNamespacesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	createNamespace(t, ctx, nc)

	// azurerm's namespace Read dereferences maximumThroughputUnits.
	ns, err := nc.Get(ctx, rgName, nsName, nil)
	if err != nil || ns.Properties.MaximumThroughputUnits == nil || *ns.Properties.MaximumThroughputUnits != 0 {
		t.Fatalf("namespace must report maximumThroughputUnits 0: %v %+v", err, ns.Properties)
	}

	got, err := nc.GetNetworkRuleSet(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("GET fresh rule set: %v", err)
	}

	p := got.Properties
	if *got.Type != "Microsoft.EventHub/Namespaces/NetworkRuleSets" || *p.DefaultAction != armeventhub.DefaultActionAllow ||
		*p.PublicNetworkAccess != armeventhub.PublicNetworkAccessFlagEnabled || len(p.IPRules) != 0 {
		t.Fatalf("not the default rule set: %s %+v", *got.Type, p)
	}

	subnet := "/subscriptions/" + subID + "/resourceGroups/" + rgName +
		"/providers/Microsoft.Network/virtualNetworks/vn/subnets/s"

	if _, err := nc.CreateOrUpdateNetworkRuleSet(ctx, rgName, nsName, armeventhub.NetworkRuleSet{
		Properties: &armeventhub.NetworkRuleSetProperties{
			DefaultAction: to.Ptr(armeventhub.DefaultActionDeny),
			VirtualNetworkRules: []*armeventhub.NWRuleSetVirtualNetworkRules{{
				Subnet:                           &armeventhub.Subnet{ID: to.Ptr(subnet)},
				IgnoreMissingVnetServiceEndpoint: to.Ptr(true),
			}},
		},
	}, nil); err != nil {
		t.Fatalf("PUT rule set: %v", err)
	}

	list, err := nc.ListNetworkRuleSet(ctx, rgName, nsName, nil)
	if err != nil || len(list.Value) != 1 {
		t.Fatalf("list rule sets: %v", err)
	}

	v := list.Value[0].Properties
	if *v.DefaultAction != armeventhub.DefaultActionDeny || len(v.VirtualNetworkRules) != 1 ||
		*v.VirtualNetworkRules[0].Subnet.ID != subnet || !*v.VirtualNetworkRules[0].IgnoreMissingVnetServiceEndpoint {
		t.Fatalf("rule set did not round-trip: %+v", v)
	}

	base := ts.URL + "/subscriptions/" + subID + "/resourceGroups/" + rgName +
		"/providers/Microsoft.EventHub/namespaces/"

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, base + nsName + "/networkRuleSets/foo", http.StatusNotFound},
		{http.MethodDelete, base + nsName + "/networkRuleSets/default", http.StatusMethodNotAllowed},
		{http.MethodGet, base + "nope/networkRuleSets/default", http.StatusNotFound},
		{http.MethodPut, base + nsName + "/networkRuleSets/default", http.StatusBadRequest},
	} {
		req, _ := http.NewRequestWithContext(ctx, tc.method, tc.path+"?api-version=2024-01-01",
			strings.NewReader(`{"properties":{"defaultAction":"Nope"}}`))

		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}

		_ = resp.Body.Close()

		if resp.StatusCode != tc.want {
			t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
}
