package loadbalancer_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKAzureLBCaseInsensitiveAddressing proves the native store keys a load
// balancer case-insensitively: a load balancer created with lower-case rg/name
// resolves through differently-cased GET/DELETE/sub-resource requests, and a
// re-PUT with different casing updates in place instead of creating a duplicate.
func TestSDKAzureLBCaseInsensitiveAddressing(t *testing.T) {
	c := newLBSubClients(t)
	ctx := context.Background()

	const (
		lowerRG = "myrg"
		upperRG = "MyRG"
		lowerLB = "mylb"
		upperLB = "MyLb"
	)

	mustCreate := func(rg, name string) {
		poller, err := c.lb.BeginCreateOrUpdate(ctx, rg, name, armnetwork.LoadBalancer{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.LoadBalancerPropertiesFormat{
				BackendAddressPools: []*armnetwork.BackendAddressPool{{Name: to.Ptr("pool-a")}},
			},
		}, nil)
		if err != nil {
			t.Fatalf("BeginCreateOrUpdate(%s/%s): %v", rg, name, err)
		}

		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			t.Fatalf("CreateOrUpdate PollUntilDone(%s/%s): %v", rg, name, err)
		}
	}

	mustCreate(lowerRG, lowerLB)

	// GET with differently-cased rg and name resolves the same load balancer.
	got, err := c.lb.Get(ctx, upperRG, upperLB, nil)
	if err != nil {
		t.Fatalf("Get(%s/%s): %v", upperRG, upperLB, err)
	}

	if got.Properties == nil || len(got.Properties.BackendAddressPools) != 1 {
		t.Fatalf("cased Get pools = %+v, want 1", got.Properties)
	}

	// Re-PUT with different casing must update in place, not duplicate. List by
	// rg (List matches rg case-insensitively) must still show exactly one.
	mustCreate(upperRG, upperLB)

	var count int

	pager := c.lb.NewListPager(lowerRG, nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("List: %v", perr)
		}

		count += len(page.Value)
	}

	if count != 1 {
		t.Fatalf("list after cased re-PUT = %d load balancers, want 1 (no duplicate)", count)
	}

	// Sub-resource CRUD addressed by cased rg/lb name resolves the same LB.
	poolPoller, err := c.pools.BeginCreateOrUpdate(ctx, upperRG, upperLB, "pool-b",
		armnetwork.BackendAddressPool{}, nil)
	if err != nil {
		t.Fatalf("pool BeginCreateOrUpdate (cased): %v", err)
	}

	if _, err := poolPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool PollUntilDone (cased): %v", err)
	}

	poolGot, err := c.pools.Get(ctx, upperRG, upperLB, "pool-b", nil)
	if err != nil {
		t.Fatalf("pool Get (cased): %v", err)
	}

	if poolGot.Name == nil || *poolGot.Name != "pool-b" {
		t.Fatalf("pool name = %v, want pool-b", poolGot.Name)
	}

	delPoolPoller, err := c.pools.BeginDelete(ctx, upperRG, upperLB, "pool-b", nil)
	if err != nil {
		t.Fatalf("pool BeginDelete (cased): %v", err)
	}

	if _, err := delPoolPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool delete PollUntilDone (cased): %v", err)
	}

	// DELETE the whole load balancer by a differently-cased name.
	delPoller, err := c.lb.BeginDelete(ctx, upperRG, upperLB, nil)
	if err != nil {
		t.Fatalf("BeginDelete (cased): %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone (cased): %v", err)
	}
}

// TestSDKAzureLBUpdateTags proves PATCH LoadBalancers.UpdateTags merges the
// supplied tags into the stored load balancer (keeping existing tags) and
// returns 200 with the updated resource.
func TestSDKAzureLBUpdateTags(t *testing.T) {
	client := newLBClient(t)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, testRG, "lb-tags", armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("test"), "team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("CreateOrUpdate PollUntilDone: %v", err)
	}

	updated, err := client.UpdateTags(ctx, testRG, "lb-tags", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("platform"), "cost": to.Ptr("42")},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	tags := updated.Tags
	if tags["env"] == nil || *tags["env"] != "test" {
		t.Fatalf("merged tags lost env: %v", tags)
	}

	if tags["team"] == nil || *tags["team"] != "platform" {
		t.Fatalf("merged tags team = %v, want platform (overwritten)", tags["team"])
	}

	if tags["cost"] == nil || *tags["cost"] != "42" {
		t.Fatalf("merged tags missing cost: %v", tags)
	}
}

// TestSDKAzureLBRuleProbeDefaults proves omitted rule/probe fields read back
// with the defaults real Azure synthesizes: loadBalancingRule
// idleTimeoutInMinutes=4 / loadDistribution=Default, probe intervalInSeconds=15
// / numberOfProbes=2.
func TestSDKAzureLBRuleProbeDefaults(t *testing.T) {
	client := newLBClient(t)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, testRG, "lb-def", armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			Probes: []*armnetwork.Probe{
				{
					Name: to.Ptr("probe-a"),
					Properties: &armnetwork.ProbePropertiesFormat{
						Protocol: to.Ptr(armnetwork.ProbeProtocolTCP),
						Port:     to.Ptr(int32(80)),
					},
				},
			},
			LoadBalancingRules: []*armnetwork.LoadBalancingRule{
				{
					Name: to.Ptr("rule-a"),
					Properties: &armnetwork.LoadBalancingRulePropertiesFormat{
						Protocol:     to.Ptr(armnetwork.TransportProtocolTCP),
						FrontendPort: to.Ptr(int32(80)),
						BackendPort:  to.Ptr(int32(80)),
					},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate PollUntilDone: %v", err)
	}

	if len(created.Properties.LoadBalancingRules) != 1 {
		t.Fatalf("rules = %d, want 1", len(created.Properties.LoadBalancingRules))
	}

	rp := created.Properties.LoadBalancingRules[0].Properties
	if rp.IdleTimeoutInMinutes == nil || *rp.IdleTimeoutInMinutes != 4 {
		t.Fatalf("rule idleTimeoutInMinutes = %v, want 4", rp.IdleTimeoutInMinutes)
	}

	if rp.LoadDistribution == nil || *rp.LoadDistribution != armnetwork.LoadDistributionDefault {
		t.Fatalf("rule loadDistribution = %v, want Default", rp.LoadDistribution)
	}

	if len(created.Properties.Probes) != 1 {
		t.Fatalf("probes = %d, want 1", len(created.Properties.Probes))
	}

	pp := created.Properties.Probes[0].Properties
	if pp.IntervalInSeconds == nil || *pp.IntervalInSeconds != 15 {
		t.Fatalf("probe intervalInSeconds = %v, want 15", pp.IntervalInSeconds)
	}

	if pp.NumberOfProbes == nil || *pp.NumberOfProbes != 2 {
		t.Fatalf("probe numberOfProbes = %v, want 2", pp.NumberOfProbes)
	}
}
