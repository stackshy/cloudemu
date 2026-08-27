package loadbalancer_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// lbSubClients bundles every load-balancer sub-resource client used below,
// all pointed at the same in-memory server as the whole-LB client so a test
// can drive both against one shared driver instance. srv carries the raw
// endpoint/HTTPClient for sub-resources (probes, loadBalancingRules) whose
// real armnetwork client has no PUT/DELETE method to call at all.
type lbSubClients struct {
	srv   lbServer
	lb    *armnetwork.LoadBalancersClient
	pools *armnetwork.LoadBalancerBackendAddressPoolsClient
	nat   *armnetwork.InboundNatRulesClient
	rules *armnetwork.LoadBalancerLoadBalancingRulesClient
	probe *armnetwork.LoadBalancerProbesClient
}

func newLBSubClients(t *testing.T) lbSubClients {
	t.Helper()

	srv := newLBServer(t)
	opts := srv.Opts

	lb, err := armnetwork.NewLoadBalancersClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewLoadBalancersClient: %v", err)
	}

	pools, err := armnetwork.NewLoadBalancerBackendAddressPoolsClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewLoadBalancerBackendAddressPoolsClient: %v", err)
	}

	nat, err := armnetwork.NewInboundNatRulesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewInboundNatRulesClient: %v", err)
	}

	rules, err := armnetwork.NewLoadBalancerLoadBalancingRulesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewLoadBalancerLoadBalancingRulesClient: %v", err)
	}

	probe, err := armnetwork.NewLoadBalancerProbesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewLoadBalancerProbesClient: %v", err)
	}

	return lbSubClients{srv: srv, lb: lb, pools: pools, nat: nat, rules: rules, probe: probe}
}

// rawRequest issues method against a load-balancer sub-resource path directly
// (bypassing the SDK, which has no client call for a standalone
// PUT/DELETE on a Get/List-only sub-resource such as probes or
// loadBalancingRules) and returns the response status code.
func (c lbSubClients) rawRequest(t *testing.T, method, lbName, child, name string) int {
	t.Helper()

	url := c.srv.Endpoint + "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Network/loadBalancers/" + lbName + "/" + child + "/" + name +
		"?api-version=2023-09-01"

	req, err := http.NewRequestWithContext(context.Background(), method, url, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	resp, err := c.srv.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	return resp.StatusCode
}

// seedTwoPoolLB creates lbName with two named backend pools, a probe and a
// frontend so a standalone child PUT/GET/DELETE has siblings whose survival
// each test can assert on.
func seedTwoPoolLB(t *testing.T, lb *armnetwork.LoadBalancersClient, lbName string) {
	t.Helper()

	ctx := context.Background()

	poller, err := lb.BeginCreateOrUpdate(ctx, testRG, lbName, armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			FrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{{
				Name: to.Ptr("fe-1"),
			}},
			BackendAddressPools: []*armnetwork.BackendAddressPool{
				{Name: to.Ptr("pool-a")},
				{Name: to.Ptr("pool-b")},
			},
			Probes: []*armnetwork.Probe{{
				Name: to.Ptr("probe-1"),
				Properties: &armnetwork.ProbePropertiesFormat{
					Protocol: to.Ptr(armnetwork.ProbeProtocolTCP),
					Port:     to.Ptr(int32(80)),
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("seed BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("seed poll: %v", err)
	}
}

func respStatus(t *testing.T, err error) int {
	t.Helper()

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("error %v is not an *azcore.ResponseError", err)
	}

	return respErr.StatusCode
}

// BLOCKER: a standalone child DELETE must remove only the addressed child,
// not cascade-delete the parent load balancer or any sibling.
func TestSDKLBBackendPoolDeleteIsScopedToChild(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()
	const lbName = "lb-pool-delete"

	seedTwoPoolLB(t, c.lb, lbName)

	poller, err := c.pools.BeginDelete(ctx, testRG, lbName, "pool-a", nil)
	if err != nil {
		t.Fatalf("BeginDelete pool-a: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll delete pool-a: %v", err)
	}

	// The parent load balancer must still exist.
	got, err := c.lb.Get(ctx, testRG, lbName, nil)
	if err != nil {
		t.Fatalf("Get after child delete: %v, want the parent load balancer to survive", err)
	}

	if got.Properties == nil || len(got.Properties.BackendAddressPools) != 1 {
		t.Fatalf("pools after deleting pool-a = %+v, want exactly [pool-b]", got.Properties)
	}

	if *got.Properties.BackendAddressPools[0].Name != "pool-b" {
		t.Fatalf("remaining pool = %v, want pool-b", *got.Properties.BackendAddressPools[0].Name)
	}

	// The sibling probe seeded alongside the pools must also survive.
	if len(got.Properties.Probes) != 1 || *got.Properties.Probes[0].Name != "probe-1" {
		t.Fatalf("probes after deleting pool-a = %+v, want probe-1 untouched", got.Properties.Probes)
	}
}

// BLOCKER: a standalone child PUT must add/update only the addressed child,
// not wipe every other child of the parent load balancer.
func TestSDKLBBackendPoolPutPreservesSiblings(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()
	const lbName = "lb-pool-put"

	seedTwoPoolLB(t, c.lb, lbName)

	poller, err := c.pools.BeginCreateOrUpdate(ctx, testRG, lbName, "pool-c", armnetwork.BackendAddressPool{}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate pool-c: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("poll create pool-c: %v", err)
	}

	if created.Name == nil || *created.Name != "pool-c" {
		t.Fatalf("created pool name = %v, want pool-c (not the parent LB's name)", created.Name)
	}

	got, err := c.lb.Get(ctx, testRG, lbName, nil)
	if err != nil {
		t.Fatalf("Get after child put: %v", err)
	}

	var names []string
	for _, p := range got.Properties.BackendAddressPools {
		names = append(names, *p.Name)
	}

	if len(names) != 3 {
		t.Fatalf("pools after standalone put = %v, want 3 (pool-a, pool-b, pool-c all present)", names)
	}

	if len(got.Properties.Probes) != 1 || *got.Properties.Probes[0].Name != "probe-1" {
		t.Fatalf("probes after standalone pool put = %+v, want probe-1 untouched", got.Properties.Probes)
	}

	if len(got.Properties.FrontendIPConfigurations) != 1 {
		t.Fatalf("frontends after standalone pool put = %+v, want fe-1 untouched", got.Properties.FrontendIPConfigurations)
	}
}

// HIGH: standalone GET on the Get/List-only sub-resource kinds (probes,
// loadBalancingRules) must also return the addressed child, not the whole
// parent LoadBalancer object under a wrong .Name — same routing root cause,
// exercised on the two kinds with no standalone PUT/DELETE.
func TestSDKLBProbeAndRuleGetReturnChildNotParent(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()
	const lbName = "lb-getchild"

	poller, err := c.lb.BeginCreateOrUpdate(ctx, testRG, lbName, armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			BackendAddressPools: []*armnetwork.BackendAddressPool{{Name: to.Ptr("pool-a")}},
			Probes: []*armnetwork.Probe{{
				Name: to.Ptr("probe-x"),
				Properties: &armnetwork.ProbePropertiesFormat{
					Protocol: to.Ptr(armnetwork.ProbeProtocolTCP),
					Port:     to.Ptr(int32(80)),
				},
			}},
			LoadBalancingRules: []*armnetwork.LoadBalancingRule{{
				Name: to.Ptr("rule-x"),
				Properties: &armnetwork.LoadBalancingRulePropertiesFormat{
					Protocol:           to.Ptr(armnetwork.TransportProtocolTCP),
					FrontendPort:       to.Ptr(int32(80)),
					BackendPort:        to.Ptr(int32(80)),
					BackendAddressPool: &armnetwork.SubResource{ID: to.Ptr(lbChildID2(lbName, "backendAddressPools", "pool-a"))},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll: %v", err)
	}

	gotProbe, err := c.probe.Get(ctx, testRG, lbName, "probe-x", nil)
	if err != nil {
		t.Fatalf("Get probe-x: %v", err)
	}

	if gotProbe.Name == nil || *gotProbe.Name != "probe-x" {
		t.Fatalf("standalone probe Get .Name = %v, want probe-x (not the parent load balancer's name %q)", gotProbe.Name, lbName)
	}

	gotRule, err := c.rules.Get(ctx, testRG, lbName, "rule-x", nil)
	if err != nil {
		t.Fatalf("Get rule-x: %v", err)
	}

	if gotRule.Name == nil || *gotRule.Name != "rule-x" {
		t.Fatalf("standalone rule Get .Name = %v, want rule-x (not the parent load balancer's name %q)", gotRule.Name, lbName)
	}
}

// HIGH: a standalone child GET must return the addressed child, not the whole
// parent LoadBalancer object under a wrong .Name.
func TestSDKLBBackendPoolGetReturnsChildNotParent(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()
	const lbName = "lb-pool-get"

	seedTwoPoolLB(t, c.lb, lbName)

	got, err := c.pools.Get(ctx, testRG, lbName, "pool-a", nil)
	if err != nil {
		t.Fatalf("Get pool-a: %v", err)
	}

	if got.Name == nil || *got.Name != "pool-a" {
		t.Fatalf("standalone pool Get .Name = %v, want pool-a (not the parent load balancer's name %q)", got.Name, lbName)
	}
}

// Same routing fix, exercised on inboundNatRules (also full standalone CRUD
// in real ARM).
func TestSDKLBNatRuleCRUDIsScopedToChild(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()
	const lbName = "lb-nat"

	seedTwoPoolLB(t, c.lb, lbName)

	putPoller, err := c.nat.BeginCreateOrUpdate(ctx, testRG, lbName, "nat-1", armnetwork.InboundNatRule{
		Properties: &armnetwork.InboundNatRulePropertiesFormat{
			Protocol:     to.Ptr(armnetwork.TransportProtocolTCP),
			FrontendPort: to.Ptr(int32(3389)),
			BackendPort:  to.Ptr(int32(3389)),
			FrontendIPConfiguration: &armnetwork.SubResource{
				ID: to.Ptr(lbChildID2(lbName, "frontendIPConfigurations", "fe-1")),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate nat-1: %v", err)
	}

	created, err := putPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("poll create nat-1: %v", err)
	}

	if created.Name == nil || *created.Name != "nat-1" {
		t.Fatalf("created NAT rule name = %v, want nat-1", created.Name)
	}

	if created.Properties == nil || created.Properties.FrontendPort == nil || *created.Properties.FrontendPort != 3389 {
		t.Fatalf("created NAT rule properties = %+v, want frontendPort 3389", created.Properties)
	}

	// The whole-LB body must reflect the standalone-created NAT rule, and
	// every child seeded before it must still be present.
	got, err := c.lb.Get(ctx, testRG, lbName, nil)
	if err != nil {
		t.Fatalf("Get after NAT rule put: %v", err)
	}

	if len(got.Properties.InboundNatRules) != 1 || *got.Properties.InboundNatRules[0].Name != "nat-1" {
		t.Fatalf("NAT rules on parent = %+v, want [nat-1]", got.Properties.InboundNatRules)
	}

	if len(got.Properties.BackendAddressPools) != 2 {
		t.Fatalf("backend pools after NAT rule put = %+v, want pool-a and pool-b untouched", got.Properties.BackendAddressPools)
	}

	// Standalone GET returns the child, not the parent.
	gotRule, err := c.nat.Get(ctx, testRG, lbName, "nat-1", nil)
	if err != nil {
		t.Fatalf("Get nat-1: %v", err)
	}

	if gotRule.Name == nil || *gotRule.Name != "nat-1" {
		t.Fatalf("standalone NAT rule Get .Name = %v, want nat-1", gotRule.Name)
	}

	// Standalone DELETE removes only the NAT rule.
	delPoller, err := c.nat.BeginDelete(ctx, testRG, lbName, "nat-1", nil)
	if err != nil {
		t.Fatalf("BeginDelete nat-1: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll delete nat-1: %v", err)
	}

	afterDelete, err := c.lb.Get(ctx, testRG, lbName, nil)
	if err != nil {
		t.Fatalf("Get after NAT rule delete: %v, want the parent load balancer to survive", err)
	}

	if len(afterDelete.Properties.InboundNatRules) != 0 {
		t.Fatalf("NAT rules after delete = %+v, want none", afterDelete.Properties.InboundNatRules)
	}

	if len(afterDelete.Properties.BackendAddressPools) != 2 {
		t.Fatalf("backend pools after NAT rule delete = %+v, want pool-a and pool-b untouched", afterDelete.Properties.BackendAddressPools)
	}
}

// MEDIUM: a NAT rule referencing a nonexistent frontend IP configuration is
// rejected with 400, not silently accepted.
func TestSDKLBNatRuleDanglingFrontendRefRejected(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()
	const lbName = "lb-nat-badref"

	seedTwoPoolLB(t, c.lb, lbName)

	_, err := c.nat.BeginCreateOrUpdate(ctx, testRG, lbName, "nat-bad", armnetwork.InboundNatRule{
		Properties: &armnetwork.InboundNatRulePropertiesFormat{
			Protocol:     to.Ptr(armnetwork.TransportProtocolTCP),
			FrontendPort: to.Ptr(int32(3389)),
			BackendPort:  to.Ptr(int32(3389)),
			FrontendIPConfiguration: &armnetwork.SubResource{
				ID: to.Ptr(lbChildID2(lbName, "frontendIPConfigurations", "does-not-exist")),
			},
		},
	}, nil)
	if err == nil {
		t.Fatal("BeginCreateOrUpdate with dangling frontend ref: want error, got nil")
	}

	if status := respStatus(t, err); status != 400 {
		t.Fatalf("dangling frontend ref status = %d, want 400", status)
	}
}

// probes and loadBalancingRules have no standalone create/delete in real ARM
// (Get/List only) — a raw PUT/DELETE against those sub-resource paths must
// not fall through to the whole-LB handlers (which would wipe/delete the
// parent); it must be rejected outright, and the parent and every sibling
// must survive the attempt.
func TestLBProbeStandalonePutDeleteRejected(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()
	const lbName = "lb-probe-405"

	seedTwoPoolLB(t, c.lb, lbName)

	if status := c.rawRequest(t, http.MethodPut, lbName, "probes", "probe-1"); status != http.StatusMethodNotAllowed {
		t.Fatalf("standalone PUT probes/probe-1 status = %d, want 405", status)
	}

	if status := c.rawRequest(t, http.MethodDelete, lbName, "probes", "probe-1"); status != http.StatusMethodNotAllowed {
		t.Fatalf("standalone DELETE probes/probe-1 status = %d, want 405", status)
	}

	if status := c.rawRequest(t, http.MethodDelete, lbName, "loadBalancingRules", "no-such-rule"); status != http.StatusMethodNotAllowed {
		t.Fatalf("standalone DELETE loadBalancingRules/no-such-rule status = %d, want 405", status)
	}

	// The parent load balancer and every sibling seeded alongside probe-1
	// must have survived both rejected requests untouched.
	got, err := c.lb.Get(ctx, testRG, lbName, nil)
	if err != nil {
		t.Fatalf("Get parent after rejected standalone probe requests: %v, want the parent to still exist", err)
	}

	if len(got.Properties.Probes) != 1 || *got.Properties.Probes[0].Name != "probe-1" {
		t.Fatalf("probes after rejected standalone requests = %+v, want probe-1 untouched", got.Properties.Probes)
	}

	if len(got.Properties.BackendAddressPools) != 2 {
		t.Fatalf("backend pools after rejected standalone probe requests = %+v, want pool-a and pool-b untouched",
			got.Properties.BackendAddressPools)
	}
}

// HIGH: loadBalancingRules.disableOutboundSnat must round-trip, not be
// dropped on Get.
func TestSDKLBRuleDisableOutboundSnatRoundTrips(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()
	const lbName = "lb-snat"

	poller, err := c.lb.BeginCreateOrUpdate(ctx, testRG, lbName, armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			BackendAddressPools: []*armnetwork.BackendAddressPool{{Name: to.Ptr("pool-a")}},
			LoadBalancingRules: []*armnetwork.LoadBalancingRule{{
				Name: to.Ptr("rule-1"),
				Properties: &armnetwork.LoadBalancingRulePropertiesFormat{
					Protocol:            to.Ptr(armnetwork.TransportProtocolTCP),
					FrontendPort:        to.Ptr(int32(80)),
					BackendPort:         to.Ptr(int32(80)),
					DisableOutboundSnat: to.Ptr(true),
					BackendAddressPool:  &armnetwork.SubResource{ID: to.Ptr(lbChildID2(lbName, "backendAddressPools", "pool-a"))},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll: %v", err)
	}

	got, err := c.lb.Get(ctx, testRG, lbName, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(got.Properties.LoadBalancingRules) != 1 {
		t.Fatalf("rules = %d, want 1", len(got.Properties.LoadBalancingRules))
	}

	rule := got.Properties.LoadBalancingRules[0]
	if rule.Properties == nil || rule.Properties.DisableOutboundSnat == nil || !*rule.Properties.DisableOutboundSnat {
		t.Fatalf("disableOutboundSnat = %v, want true (dropped on Get)", rule.Properties)
	}
}

// MEDIUM: a loadBalancingRule referencing a nonexistent backendAddressPool in
// the same whole-LB PUT is rejected with 400.
func TestSDKLBRuleDanglingPoolRefRejected(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()
	const lbName = "lb-rule-badref"

	_, err := c.lb.BeginCreateOrUpdate(ctx, testRG, lbName, armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			LoadBalancingRules: []*armnetwork.LoadBalancingRule{{
				Name: to.Ptr("rule-1"),
				Properties: &armnetwork.LoadBalancingRulePropertiesFormat{
					Protocol:           to.Ptr(armnetwork.TransportProtocolTCP),
					FrontendPort:       to.Ptr(int32(80)),
					BackendPort:        to.Ptr(int32(80)),
					BackendAddressPool: &armnetwork.SubResource{ID: to.Ptr(lbChildID2(lbName, "backendAddressPools", "no-such-pool"))},
				},
			}},
		},
	}, nil)
	if err == nil {
		t.Fatal("BeginCreateOrUpdate with dangling pool ref: want error, got nil")
	}

	if status := respStatus(t, err); status != 400 {
		t.Fatalf("dangling pool ref status = %d, want 400", status)
	}
}

// MEDIUM: a loadBalancingRule referencing a nonexistent probe in the same
// whole-LB PUT is rejected with 400.
func TestSDKLBRuleDanglingProbeRefRejected(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()
	const lbName = "lb-rule-badprobe"

	_, err := c.lb.BeginCreateOrUpdate(ctx, testRG, lbName, armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			BackendAddressPools: []*armnetwork.BackendAddressPool{{Name: to.Ptr("pool-a")}},
			LoadBalancingRules: []*armnetwork.LoadBalancingRule{{
				Name: to.Ptr("rule-1"),
				Properties: &armnetwork.LoadBalancingRulePropertiesFormat{
					Protocol:           to.Ptr(armnetwork.TransportProtocolTCP),
					FrontendPort:       to.Ptr(int32(80)),
					BackendPort:        to.Ptr(int32(80)),
					BackendAddressPool: &armnetwork.SubResource{ID: to.Ptr(lbChildID2(lbName, "backendAddressPools", "pool-a"))},
					Probe:              &armnetwork.SubResource{ID: to.Ptr(lbChildID2(lbName, "probes", "no-such-probe"))},
				},
			}},
		},
	}, nil)
	if err == nil {
		t.Fatal("BeginCreateOrUpdate with dangling probe ref: want error, got nil")
	}

	if status := respStatus(t, err); status != 400 {
		t.Fatalf("dangling probe ref status = %d, want 400", status)
	}
}

// LOW: duplicate child names within one whole-LB PUT body are rejected, not
// silently accepted.
func TestSDKLBDuplicatePoolNameRejected(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()
	const lbName = "lb-dup-pool"

	_, err := c.lb.BeginCreateOrUpdate(ctx, testRG, lbName, armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			BackendAddressPools: []*armnetwork.BackendAddressPool{
				{Name: to.Ptr("dup-pool")},
				{Name: to.Ptr("dup-pool")},
			},
		},
	}, nil)
	if err == nil {
		t.Fatal("BeginCreateOrUpdate with duplicate pool names: want error, got nil")
	}

	if status := respStatus(t, err); status != 400 {
		t.Fatalf("duplicate pool name status = %d, want 400", status)
	}
}

// lbChildID2 builds a child resource id under lbName (distinct helper name
// from lb_e2e_test.go's lbChildID, which hardcodes "lb-full").
func lbChildID2(lbName, child, name string) string {
	return "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Network/loadBalancers/" + lbName + "/" + child + "/" + name
}
