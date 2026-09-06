package firewall_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	testRG   = "rg-1"
	testSub  = "sub-1"
	fwName   = "fw-1"
	polName  = "pol-1"
	provider = "Microsoft.Network"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// newServer stands up the full Azure wire server backed by a fresh in-memory
// provider. The VNet, LB and AppGateway handlers are wired too so we prove the
// azureFirewalls / firewallPolicies resource types are not shadowed by another
// Microsoft.Network handler.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		Firewall:   cloudP.Firewall,
		Network:    cloudP.VNet,
		LB:         cloudP.LB,
		AppGateway: cloudP.AppGateway,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func clientOpts(ts *httptest.Server) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}
}

func firewallClient(t *testing.T, ts *httptest.Server) *armnetwork.AzureFirewallsClient {
	t.Helper()

	c, err := armnetwork.NewAzureFirewallsClient(testSub, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewAzureFirewallsClient: %v", err)
	}

	return c
}

func policyClient(t *testing.T, ts *httptest.Server) *armnetwork.FirewallPoliciesClient {
	t.Helper()

	c, err := armnetwork.NewFirewallPoliciesClient(testSub, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewFirewallPoliciesClient: %v", err)
	}

	return c
}

func fwID() string {
	return "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/" + provider + "/azureFirewalls/" + fwName
}

func policyID(name string) string {
	return "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/" + provider + "/firewallPolicies/" + name
}

// sampleFirewall builds a Standard AZFW_VNet firewall with one ipConfiguration
// (subnet + public IP), zones, threatIntelMode and a firewall-policy association.
func sampleFirewall(policyRef string) armnetwork.AzureFirewall {
	return armnetwork.AzureFirewall{
		Location: to.Ptr("eastus"),
		Zones:    []*string{to.Ptr("1"), to.Ptr("2"), to.Ptr("3")},
		Properties: &armnetwork.AzureFirewallPropertiesFormat{
			SKU: &armnetwork.AzureFirewallSKU{
				Name: to.Ptr(armnetwork.AzureFirewallSKUNameAZFWVnet),
				Tier: to.Ptr(armnetwork.AzureFirewallSKUTierStandard),
			},
			ThreatIntelMode: to.Ptr(armnetwork.AzureFirewallThreatIntelModeDeny),
			FirewallPolicy:  &armnetwork.SubResource{ID: to.Ptr(policyRef)},
			IPConfigurations: []*armnetwork.AzureFirewallIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.AzureFirewallIPConfigurationPropertiesFormat{
					Subnet: &armnetwork.SubResource{ID: to.Ptr("/subscriptions/" + testSub +
						"/resourceGroups/" + testRG + "/providers/" + provider +
						"/virtualNetworks/vnet/subnets/AzureFirewallSubnet")},
					PublicIPAddress: &armnetwork.SubResource{ID: to.Ptr("/subscriptions/" + testSub +
						"/resourceGroups/" + testRG + "/providers/" + provider + "/publicIPAddresses/pip")},
				},
			}},
		},
	}
}

func createFirewall(
	t *testing.T, c *armnetwork.AzureFirewallsClient, body armnetwork.AzureFirewall,
) armnetwork.AzureFirewall {
	t.Helper()

	ctx := context.Background()

	poller, err := c.BeginCreateOrUpdate(ctx, testRG, fwName, body, nil)
	if err != nil {
		t.Fatalf("firewall BeginCreateOrUpdate: %v", err)
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("firewall PollUntilDone: %v", err)
	}

	return resp.AzureFirewall
}

// TestFirewallRoundTrip proves the modeled surface (sku name/tier, zones,
// ipConfigurations with computed privateIPAddress + stamped ids, threatIntelMode,
// firewallPolicy association, provisioningState) survives create → get.
func TestFirewallRoundTrip(t *testing.T) {
	ts := newServer(t)
	c := firewallClient(t, ts)
	polRef := policyID(polName)

	created := createFirewall(t, c, sampleFirewall(polRef))
	assertFirewall(t, &created, polRef)

	got, err := c.Get(context.Background(), testRG, fwName, nil)
	if err != nil {
		t.Fatalf("firewall Get: %v", err)
	}

	assertFirewall(t, &got.AzureFirewall, polRef)
}

//nolint:gocyclo,cyclop // a round-trip assertion legitimately checks many fields.
func assertFirewall(t *testing.T, fw *armnetwork.AzureFirewall, polRef string) {
	t.Helper()

	if got := deref(fw.ID); got != fwID() {
		t.Errorf("id = %q, want %q", got, fwID())
	}

	if got := deref(fw.Name); got != fwName {
		t.Errorf("name = %q, want %q", got, fwName)
	}

	if fw.Properties == nil {
		t.Fatal("properties nil")
	}

	if fw.Properties.SKU == nil || fw.Properties.SKU.Name == nil || fw.Properties.SKU.Tier == nil {
		t.Fatal("sku not round-tripped")
	}

	if string(*fw.Properties.SKU.Name) != string(armnetwork.AzureFirewallSKUNameAZFWVnet) {
		t.Errorf("sku.name = %q, want AZFW_VNet", *fw.Properties.SKU.Name)
	}

	if string(*fw.Properties.SKU.Tier) != string(armnetwork.AzureFirewallSKUTierStandard) {
		t.Errorf("sku.tier = %q, want Standard", *fw.Properties.SKU.Tier)
	}

	if len(fw.Zones) != 3 {
		t.Errorf("zones len = %d, want 3", len(fw.Zones))
	}

	if fw.Properties.ThreatIntelMode == nil || *fw.Properties.ThreatIntelMode != armnetwork.AzureFirewallThreatIntelModeDeny {
		t.Errorf("threatIntelMode = %v, want Deny", fw.Properties.ThreatIntelMode)
	}

	if fw.Properties.FirewallPolicy == nil || deref(fw.Properties.FirewallPolicy.ID) != polRef {
		t.Errorf("firewallPolicy.id = %v, want %q", fw.Properties.FirewallPolicy, polRef)
	}

	if fw.Properties.ProvisioningState == nil || *fw.Properties.ProvisioningState != armnetwork.ProvisioningStateSucceeded {
		t.Errorf("provisioningState = %v, want Succeeded", fw.Properties.ProvisioningState)
	}

	assertIPConfig(t, fw)
}

func assertIPConfig(t *testing.T, fw *armnetwork.AzureFirewall) {
	t.Helper()

	if len(fw.Properties.IPConfigurations) != 1 {
		t.Fatalf("ipConfigurations len = %d, want 1", len(fw.Properties.IPConfigurations))
	}

	cfg := fw.Properties.IPConfigurations[0]
	if deref(cfg.Name) != "ipconfig1" {
		t.Errorf("ipconfig name = %q, want ipconfig1", deref(cfg.Name))
	}

	if deref(cfg.ID) != fwID()+"/ipConfigurations/ipconfig1" {
		t.Errorf("ipconfig id = %q, unexpected", deref(cfg.ID))
	}

	if cfg.Properties == nil || deref(cfg.Properties.PrivateIPAddress) == "" {
		t.Error("ipconfig privateIPAddress not computed")
	}

	if cfg.Properties == nil || cfg.Properties.Subnet == nil || cfg.Properties.PublicIPAddress == nil {
		t.Error("ipconfig subnet/publicIPAddress not round-tripped")
	}
}

// TestFirewallThreatIntelModeDefault proves an omitted threatIntelMode defaults
// to Alert.
func TestFirewallThreatIntelModeDefault(t *testing.T) {
	ts := newServer(t)
	c := firewallClient(t, ts)

	body := armnetwork.AzureFirewall{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.AzureFirewallPropertiesFormat{
			SKU: &armnetwork.AzureFirewallSKU{
				Name: to.Ptr(armnetwork.AzureFirewallSKUNameAZFWVnet),
				Tier: to.Ptr(armnetwork.AzureFirewallSKUTierStandard),
			},
		},
	}

	created := createFirewall(t, c, body)
	if created.Properties.ThreatIntelMode == nil ||
		*created.Properties.ThreatIntelMode != armnetwork.AzureFirewallThreatIntelModeAlert {
		t.Errorf("default threatIntelMode = %v, want Alert", created.Properties.ThreatIntelMode)
	}
}

// TestFirewallUpdateTagsReplaces proves PATCH UpdateTags replaces the tag set
// wholesale while preserving the firewall's other modeled state.
func TestFirewallUpdateTagsReplaces(t *testing.T) {
	ts := newServer(t)
	c := firewallClient(t, ts)
	ctx := context.Background()

	body := sampleFirewall(policyID(polName))
	body.Tags = map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("net")}
	createFirewall(t, c, body)

	poller, err := c.BeginUpdateTags(ctx, testRG, fwName,
		armnetwork.TagsObject{Tags: map[string]*string{"env": to.Ptr("dev")}}, nil)
	if err != nil {
		t.Fatalf("BeginUpdateTags: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("UpdateTags PollUntilDone: %v", err)
	}

	got, err := c.Get(ctx, testRG, fwName, nil)
	if err != nil {
		t.Fatalf("Get after UpdateTags: %v", err)
	}

	if len(got.Tags) != 1 || deref(got.Tags["env"]) != "dev" {
		t.Errorf("tags = %v, want only {env:dev}", got.Tags)
	}

	if got.Properties.SKU == nil || got.Properties.SKU.Name == nil {
		t.Error("UpdateTags dropped sku")
	}
}

// TestFirewallDelete proves delete removes the firewall (subsequent Get 404s).
func TestFirewallDelete(t *testing.T) {
	ts := newServer(t)
	c := firewallClient(t, ts)
	ctx := context.Background()

	createFirewall(t, c, sampleFirewall(policyID(polName)))

	poller, err := c.BeginDelete(ctx, testRG, fwName, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	if _, err = c.Get(ctx, testRG, fwName, nil); err == nil {
		t.Fatal("Get after delete: want error, got nil")
	}
}

// TestFirewallPolicyRoundTrip proves the policy modeled surface (sku.tier,
// threatIntelMode, provisioningState) survives create → get, and dnsSettings is
// echoed through.
func TestFirewallPolicyRoundTrip(t *testing.T) {
	ts := newServer(t)
	c := policyClient(t, ts)
	ctx := context.Background()

	body := armnetwork.FirewallPolicy{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.FirewallPolicyPropertiesFormat{
			SKU:             &armnetwork.FirewallPolicySKU{Tier: to.Ptr(armnetwork.FirewallPolicySKUTierPremium)},
			ThreatIntelMode: to.Ptr(armnetwork.AzureFirewallThreatIntelModeDeny),
			DNSSettings:     &armnetwork.DNSSettings{EnableProxy: to.Ptr(true), Servers: []*string{to.Ptr("10.0.0.5")}},
		},
	}

	poller, err := c.BeginCreateOrUpdate(ctx, testRG, polName, body, nil)
	if err != nil {
		t.Fatalf("policy BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("policy PollUntilDone: %v", err)
	}

	assertPolicy(t, &created.FirewallPolicy)

	got, err := c.Get(ctx, testRG, polName, nil)
	if err != nil {
		t.Fatalf("policy Get: %v", err)
	}

	assertPolicy(t, &got.FirewallPolicy)
}

func assertPolicy(t *testing.T, pol *armnetwork.FirewallPolicy) {
	t.Helper()

	if deref(pol.ID) != policyID(polName) {
		t.Errorf("id = %q, want %q", deref(pol.ID), policyID(polName))
	}

	if pol.Properties == nil || pol.Properties.SKU == nil || pol.Properties.SKU.Tier == nil {
		t.Fatal("sku.tier not round-tripped")
	}

	if *pol.Properties.SKU.Tier != armnetwork.FirewallPolicySKUTierPremium {
		t.Errorf("sku.tier = %q, want Premium", *pol.Properties.SKU.Tier)
	}

	if pol.Properties.ThreatIntelMode == nil || *pol.Properties.ThreatIntelMode != armnetwork.AzureFirewallThreatIntelModeDeny {
		t.Errorf("threatIntelMode = %v, want Deny", pol.Properties.ThreatIntelMode)
	}

	if pol.Properties.ProvisioningState == nil || *pol.Properties.ProvisioningState != armnetwork.ProvisioningStateSucceeded {
		t.Errorf("provisioningState = %v, want Succeeded", pol.Properties.ProvisioningState)
	}

	if pol.Properties.DNSSettings == nil || pol.Properties.DNSSettings.EnableProxy == nil ||
		!*pol.Properties.DNSSettings.EnableProxy {
		t.Error("dnsSettings.enableProxy not echoed through")
	}
}

// TestFirewallListDeterministic proves List returns firewalls in a stable order.
func TestFirewallListDeterministic(t *testing.T) {
	ts := newServer(t)
	c := firewallClient(t, ts)
	ctx := context.Background()

	for _, name := range []string{"fw-c", "fw-a", "fw-b"} {
		poller, err := c.BeginCreateOrUpdate(ctx, testRG, name, armnetwork.AzureFirewall{
			Location: to.Ptr("eastus"),
		}, nil)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}

		if _, err = poller.PollUntilDone(ctx, nil); err != nil {
			t.Fatalf("poll %s: %v", name, err)
		}
	}

	pager := c.NewListPager(testRG, nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, fw := range page.Value {
			names = append(names, deref(fw.Name))
		}
	}

	want := []string{"fw-a", "fw-b", "fw-c"}
	if len(names) != len(want) {
		t.Fatalf("list = %v, want %v", names, want)
	}

	for i := range want {
		if names[i] != want[i] {
			t.Errorf("list[%d] = %q, want %q (list=%v)", i, names[i], want[i], names)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
