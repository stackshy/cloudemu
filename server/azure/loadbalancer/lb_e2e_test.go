package loadbalancer_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

func lbChildID(child, name string) string {
	return "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Network/loadBalancers/lb-full/" + child + "/" + name
}

// Finding #9: PUT is a full replace — children omitted from the body are
// removed rather than accumulating.
func TestSDKLBFullReplace(t *testing.T) {
	client := newLBClient(t)
	ctx := context.Background()

	twoPools := armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			BackendAddressPools: []*armnetwork.BackendAddressPool{
				{Name: to.Ptr("pool-a")}, {Name: to.Ptr("pool-b")},
			},
		},
	}

	p, err := client.BeginCreateOrUpdate(ctx, testRG, "lb-full", twoPools, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := p.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll: %v", err)
	}

	onePool := armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			BackendAddressPools: []*armnetwork.BackendAddressPool{{Name: to.Ptr("pool-a")}},
		},
	}

	p2, err := client.BeginCreateOrUpdate(ctx, testRG, "lb-full", onePool, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if _, err := p2.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll update: %v", err)
	}

	got, err := client.Get(ctx, testRG, "lb-full", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Properties == nil || len(got.Properties.BackendAddressPools) != 1 {
		t.Fatalf("pools after replace = %+v, want 1 (pool-a only)", got.Properties)
	}

	if *got.Properties.BackendAddressPools[0].Name != "pool-a" {
		t.Fatalf("remaining pool = %v, want pool-a", got.Properties.BackendAddressPools[0].Name)
	}
}

// Findings #10 (rule name + independent backend port), #11 (frontend name/IP),
// #16 (SKU echoed), #18 (probes), #19 (etag), #14 (location).
func TestSDKLBFrontendRuleProbe(t *testing.T) {
	client := newLBClient(t)
	ctx := context.Background()

	body := armnetwork.LoadBalancer{
		Location: to.Ptr("westus2"),
		SKU:      &armnetwork.LoadBalancerSKU{Name: to.Ptr(armnetwork.LoadBalancerSKUNameBasic)},
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			FrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{{
				Name: to.Ptr("my-frontend"),
				Properties: &armnetwork.FrontendIPConfigurationPropertiesFormat{
					PrivateIPAddress:          to.Ptr("10.0.0.9"),
					PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
				},
			}},
			BackendAddressPools: []*armnetwork.BackendAddressPool{{Name: to.Ptr("web-pool")}},
			Probes: []*armnetwork.Probe{{
				Name: to.Ptr("probe-lb"),
				Properties: &armnetwork.ProbePropertiesFormat{
					Protocol:          to.Ptr(armnetwork.ProbeProtocolHTTP),
					Port:              to.Ptr(int32(80)),
					RequestPath:       to.Ptr("/health"),
					IntervalInSeconds: to.Ptr(int32(15)),
					NumberOfProbes:    to.Ptr(int32(2)),
				},
			}},
			LoadBalancingRules: []*armnetwork.LoadBalancingRule{{
				Name: to.Ptr("http-rule"),
				Properties: &armnetwork.LoadBalancingRulePropertiesFormat{
					Protocol:                to.Ptr(armnetwork.TransportProtocolTCP),
					FrontendPort:            to.Ptr(int32(80)),
					BackendPort:             to.Ptr(int32(8080)),
					EnableFloatingIP:        to.Ptr(true),
					FrontendIPConfiguration: &armnetwork.SubResource{ID: to.Ptr(lbChildID("frontendIPConfigurations", "my-frontend"))},
					BackendAddressPool:      &armnetwork.SubResource{ID: to.Ptr(lbChildID("backendAddressPools", "web-pool"))},
					Probe:                   &armnetwork.SubResource{ID: to.Ptr(lbChildID("probes", "probe-lb"))},
				},
			}},
		},
	}

	p, err := client.BeginCreateOrUpdate(ctx, testRG, "lb-full", body, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := p.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll: %v", err)
	}

	got, err := client.Get(ctx, testRG, "lb-full", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Location == nil || *got.Location != "westus2" {
		t.Fatalf("location = %v, want westus2", got.Location)
	}

	if got.SKU == nil || got.SKU.Name == nil || *got.SKU.Name != armnetwork.LoadBalancerSKUNameBasic {
		t.Fatalf("sku = %+v, want Basic", got.SKU)
	}

	if got.Etag == nil || !isWeakETag(*got.Etag) {
		t.Fatalf("etag = %v, want weak-validator form W/\"...\"", got.Etag)
	}

	assertFrontend(t, &got.LoadBalancer)
	assertRule(t, &got.LoadBalancer)
	assertProbe(t, &got.LoadBalancer)
}

func assertFrontend(t *testing.T, lb *armnetwork.LoadBalancer) {
	t.Helper()

	if len(lb.Properties.FrontendIPConfigurations) != 1 {
		t.Fatalf("frontends = %d, want 1", len(lb.Properties.FrontendIPConfigurations))
	}

	fe := lb.Properties.FrontendIPConfigurations[0]
	if fe.Name == nil || *fe.Name != "my-frontend" {
		t.Fatalf("frontend name = %v, want my-frontend", fe.Name)
	}

	if fe.Properties == nil || fe.Properties.PrivateIPAddress == nil || *fe.Properties.PrivateIPAddress != "10.0.0.9" {
		t.Fatalf("frontend privateIP = %+v, want 10.0.0.9", fe.Properties)
	}
}

func assertRule(t *testing.T, lb *armnetwork.LoadBalancer) {
	t.Helper()

	if len(lb.Properties.LoadBalancingRules) != 1 {
		t.Fatalf("rules = %d, want 1", len(lb.Properties.LoadBalancingRules))
	}

	rule := lb.Properties.LoadBalancingRules[0]
	if rule.Name == nil || *rule.Name != "http-rule" {
		t.Fatalf("rule name = %v, want http-rule (not regenerated)", rule.Name)
	}

	if rule.Properties == nil || *rule.Properties.FrontendPort != 80 || *rule.Properties.BackendPort != 8080 {
		t.Fatalf("rule ports = %+v, want frontend 80 / backend 8080", rule.Properties)
	}

	if rule.Properties.EnableFloatingIP == nil || !*rule.Properties.EnableFloatingIP {
		t.Fatalf("rule enableFloatingIP = %v, want echoed back as true", rule.Properties.EnableFloatingIP)
	}

	if rule.Properties.FrontendIPConfiguration == nil || rule.Properties.FrontendIPConfiguration.ID == nil ||
		!hasSuffix(*rule.Properties.FrontendIPConfiguration.ID, "/frontendIPConfigurations/my-frontend") {
		t.Fatalf("rule frontend ref = %+v, want named my-frontend", rule.Properties.FrontendIPConfiguration)
	}

	if rule.Properties.Probe == nil || rule.Properties.Probe.ID == nil ||
		!hasSuffix(*rule.Properties.Probe.ID, "/probes/probe-lb") {
		t.Fatalf("rule probe ref = %+v, want probe-lb", rule.Properties.Probe)
	}
}

func assertProbe(t *testing.T, lb *armnetwork.LoadBalancer) {
	t.Helper()

	if len(lb.Properties.Probes) != 1 {
		t.Fatalf("probes = %d, want 1 first-class probe", len(lb.Properties.Probes))
	}

	pr := lb.Properties.Probes[0]
	if pr.Name == nil || *pr.Name != "probe-lb" {
		t.Fatalf("probe name = %v, want probe-lb", pr.Name)
	}

	if pr.Properties == nil || pr.Properties.Port == nil || *pr.Properties.Port != 80 ||
		pr.Properties.RequestPath == nil || *pr.Properties.RequestPath != "/health" {
		t.Fatalf("probe props = %+v, want port 80 path /health", pr.Properties)
	}
}

// Finding #15: a protocol change on an existing rule is applied (full replace).
func TestSDKLBRuleProtocolUpdate(t *testing.T) {
	client := newLBClient(t)
	ctx := context.Background()

	mk := func(proto armnetwork.TransportProtocol) armnetwork.LoadBalancer {
		return armnetwork.LoadBalancer{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.LoadBalancerPropertiesFormat{
				LoadBalancingRules: []*armnetwork.LoadBalancingRule{{
					Name: to.Ptr("r1"),
					Properties: &armnetwork.LoadBalancingRulePropertiesFormat{
						Protocol:     to.Ptr(proto),
						FrontendPort: to.Ptr(int32(80)),
						BackendPort:  to.Ptr(int32(80)),
					},
				}},
			},
		}
	}

	for _, proto := range []armnetwork.TransportProtocol{
		armnetwork.TransportProtocolTCP, armnetwork.TransportProtocolUDP,
	} {
		p, err := client.BeginCreateOrUpdate(ctx, testRG, "lb-proto", mk(proto), nil)
		if err != nil {
			t.Fatalf("put %v: %v", proto, err)
		}

		if _, err := p.PollUntilDone(ctx, nil); err != nil {
			t.Fatalf("poll %v: %v", proto, err)
		}
	}

	got, err := client.Get(ctx, testRG, "lb-proto", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	rule := got.Properties.LoadBalancingRules[0]
	if rule.Properties == nil || rule.Properties.Protocol == nil ||
		*rule.Properties.Protocol != armnetwork.TransportProtocolUDP {
		t.Fatalf("rule protocol = %+v, want Udp after update", rule.Properties)
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// isWeakETag reports whether s is in the weak-validator form W/"..." that the
// real ARM API emits for Microsoft.Network resources.
func isWeakETag(s string) bool {
	return len(s) >= 4 && s[:3] == `W/"` && s[len(s)-1] == '"'
}
