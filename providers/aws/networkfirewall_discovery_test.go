package aws

import (
	"context"
	"testing"

	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// Every Network Firewall resource family the emulator holds must surface in the
// cross-service inventory with its live tags: firewalls and policies, rule
// groups, TLS inspection configurations and VPC endpoint associations.
func TestResourceDiscoverySurfacesNetworkFirewall(t *testing.T) {
	ctx := context.Background()
	p := New()
	nf := p.NetworkFirewall

	pol, err := nf.CreateFirewallPolicy(ctx, nfdriver.CreateFirewallPolicyConfig{
		Name: "pol", StatelessDefaultActions: []string{"aws:pass"}, StatelessFragmentDefaultActions: []string{"aws:pass"},
		Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateFirewallPolicy: %v", err)
	}

	fw, err := nf.CreateFirewall(ctx, nfdriver.CreateFirewallConfig{Name: "fw", PolicyARN: pol.ARN, VPCID: "vpc-1a"})
	if err != nil {
		t.Fatalf("CreateFirewall: %v", err)
	}

	if _, err := nf.CreateRuleGroup(ctx, nfdriver.CreateRuleGroupConfig{Name: "rg", Type: "STATEFUL", Capacity: 5}); err != nil {
		t.Fatalf("CreateRuleGroup: %v", err)
	}

	if _, err := nf.CreateTLSInspectionConfiguration(ctx, nfdriver.CreateTLSInspectionConfig{
		Name: "tls", Configuration: []byte(`{"ServerCertificateConfigurations":[{}]}`),
	}); err != nil {
		t.Fatalf("CreateTLSInspectionConfiguration: %v", err)
	}

	if _, err := nf.CreateVpcEndpointAssociation(ctx, nfdriver.CreateVPCEndpointAssociationConfig{
		FirewallARN: fw.ARN, VPCID: "vpc-2b", SubnetID: "subnet-1",
	}); err != nil {
		t.Fatalf("CreateVpcEndpointAssociation: %v", err)
	}

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{Services: []string{"network-firewall"}})
	if err != nil {
		t.Fatalf("discovery List: %v", err)
	}

	types := map[string]resourcediscovery.Resource{}
	for _, r := range res {
		types[r.Type] = r
	}

	for _, want := range []string{"firewall", "firewall-policy", "rule-group", "tls-configuration", "vpc-endpoint-association"} {
		if _, ok := types[want]; !ok {
			t.Errorf("%s missing from discovery output (%d resources)", want, len(res))
		}
	}

	if got := types["firewall-policy"].Tags["env"]; got != "prod" {
		t.Errorf("firewall policy tags not surfaced: %+v", types["firewall-policy"].Tags)
	}
}
