package firewall

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/azurefirewall/driver"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("sub"), config.WithRegion("eastus"))

	return New(opts)
}

func sampleFirewall() driver.AzureFirewall {
	return driver.AzureFirewall{
		Location:         "eastus",
		Zones:            []string{"1", "2"},
		Tags:             map[string]string{"env": "test"},
		SKUName:          "AZFW_VNet",
		SKUTier:          "Standard",
		ThreatIntelMode:  "Alert",
		FirewallPolicyID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/firewallPolicies/pol",
		IPConfigurations: []driver.AzureFirewallIPConfig{{
			Name: "ipconfig1", SubnetID: "subnet-id", PublicIPAddressID: "pip-id", PrivateIPAddress: "10.0.1.4",
		}},
		OtherProps: map[string]any{"hubIPAddresses": map[string]any{"privateIPAddress": "x"}},
	}
}

func TestFirewallCreateThenUpdate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	stored, created, err := m.CreateOrUpdateAzureFirewall(ctx, "rg", "fw", sampleFirewall())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !created {
		t.Fatal("first create reported created=false, want true")
	}

	if stored.Name != "fw" || stored.ResourceGroup != "rg" {
		t.Fatalf("stored identity = %q/%q, want rg/fw", stored.ResourceGroup, stored.Name)
	}

	if _, created2, _ := m.CreateOrUpdateAzureFirewall(ctx, "rg", "fw", sampleFirewall()); created2 {
		t.Fatal("second create reported created=true, want false (update)")
	}
}

func TestFirewallCreateRequiresName(t *testing.T) {
	if _, _, err := newTestMock().CreateOrUpdateAzureFirewall(context.Background(), "rg", "", sampleFirewall()); err == nil {
		t.Fatal("empty name: want error, got nil")
	}
}

func TestFirewallGetNotFound(t *testing.T) {
	_, err := newTestMock().GetAzureFirewall(context.Background(), "rg", "missing")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestFirewallDeleteNotFound(t *testing.T) {
	if err := newTestMock().DeleteAzureFirewall(context.Background(), "rg", "missing"); !cerrors.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestFirewallCloneIsolation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	fw := sampleFirewall()
	if _, _, err := m.CreateOrUpdateAzureFirewall(ctx, "rg", "fw", fw); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Mutating the caller's maps/slices must not affect stored state.
	fw.Tags["env"] = "mutated"
	fw.Zones[0] = "9"
	fw.IPConfigurations[0].Name = "mutated"

	got, err := m.GetAzureFirewall(ctx, "rg", "fw")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Tags["env"] != "test" || got.Zones[0] != "1" || got.IPConfigurations[0].Name != "ipconfig1" {
		t.Fatal("stored firewall aliased caller state")
	}
}

func TestFirewallListScopedAndSorted(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	for _, name := range []string{"fw-b", "fw-a"} {
		if _, _, err := m.CreateOrUpdateAzureFirewall(ctx, "rg1", name, sampleFirewall()); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	if _, _, err := m.CreateOrUpdateAzureFirewall(ctx, "rg2", "fw-other", sampleFirewall()); err != nil {
		t.Fatalf("create rg2: %v", err)
	}

	list, err := m.ListAzureFirewalls(ctx, "rg1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(list) != 2 || list[0].Name != "fw-a" || list[1].Name != "fw-b" {
		t.Fatalf("list = %v, want [fw-a fw-b] scoped to rg1", list)
	}
}

func TestFirewallPolicyLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	pol := driver.FirewallPolicy{
		Location: "eastus", SKUTier: "Premium", ThreatIntelMode: "Deny",
		Tags: map[string]string{"k": "v"}, OtherProps: map[string]any{"dnsSettings": map[string]any{"enableProxy": true}},
	}

	stored, created, err := m.CreateOrUpdateFirewallPolicy(ctx, "rg", "pol", pol)
	if err != nil || !created {
		t.Fatalf("create policy: created=%v err=%v", created, err)
	}

	if stored.SKUTier != "Premium" {
		t.Fatalf("sku.tier = %q, want Premium", stored.SKUTier)
	}

	got, err := m.GetFirewallPolicy(ctx, "rg", "pol")
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}

	if got.ThreatIntelMode != "Deny" {
		t.Fatalf("threatIntelMode = %q, want Deny", got.ThreatIntelMode)
	}

	if err := m.DeleteFirewallPolicy(ctx, "rg", "pol"); err != nil {
		t.Fatalf("delete policy: %v", err)
	}

	if _, err := m.GetFirewallPolicy(ctx, "rg", "pol"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete: %v, want NotFound", err)
	}
}
