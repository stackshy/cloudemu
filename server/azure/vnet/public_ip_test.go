package vnet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKPublicIPDelete guards the fix for publicIPAddresses having no DELETE
// handler (every teardown 405'd). A public IP with no association deletes
// cleanly and a subsequent Get reports NotFound.
func TestSDKPublicIPDelete(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	p, err := pips.BeginCreateOrUpdate(ctx, "rg-1", "pip-del", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pollDone(t, p)

	dp, err := pips.BeginDelete(ctx, "rg-1", "pip-del", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	pollDone(t, dp)

	if _, err = pips.Get(ctx, "rg-1", "pip-del", nil); err == nil {
		t.Error("Get after delete succeeded, want NotFound")
	}
}

// TestSDKPublicIPDeleteBlockedWhileAttached guards ReleaseAddress's existing
// in-use precondition now being reachable through the DELETE handler: a public
// IP still attached to a NIC's ipConfiguration cannot be deleted until the NIC
// releases it.
func TestSDKPublicIPDeleteBlockedWhileAttached(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	p, err := pips.BeginCreateOrUpdate(ctx, "rg-1", "pip-attached", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
		SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
		},
	}, nil)
	if err != nil {
		t.Fatalf("create pip: %v", err)
	}

	pollDone(t, p)

	pipID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/publicIPAddresses/pip-attached"

	nics, err := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	np, err := nics.BeginCreateOrUpdate(ctx, "rg-1", "nic-attached", armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					PublicIPAddress: &armnetwork.PublicIPAddress{ID: to.Ptr(pipID)},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create nic: %v", err)
	}

	pollDone(t, np)

	_, err = pips.BeginDelete(ctx, "rg-1", "pip-attached", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 400 {
		t.Fatalf("delete attached public IP: got %v, want 400", err)
	}
}

// TestSDKPublicIPResourceGroupIsolation guards the RG-scoping fix: two public
// IPs sharing a name but living in different resource groups must not
// collide, and a resource-group-scoped List must only return its own.
func TestSDKPublicIPResourceGroupIsolation(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	for _, rg := range []string{"rg-a", "rg-b"} {
		p, cErr := pips.BeginCreateOrUpdate(ctx, rg, "pip-shared", armnetwork.PublicIPAddress{
			Location: to.Ptr("eastus"),
		}, nil)
		if cErr != nil {
			t.Fatalf("create in %s: %v", rg, cErr)
		}

		pollDone(t, p)
	}

	// Each resource group's own Get succeeds and returns a distinct address.
	gotA, err := pips.Get(ctx, "rg-a", "pip-shared", nil)
	if err != nil {
		t.Fatalf("get rg-a: %v", err)
	}

	gotB, err := pips.Get(ctx, "rg-b", "pip-shared", nil)
	if err != nil {
		t.Fatalf("get rg-b: %v", err)
	}

	if gotA.Properties == nil || gotB.Properties == nil ||
		gotA.Properties.IPAddress == nil || gotB.Properties.IPAddress == nil {
		t.Fatal("missing ipAddress on one of the two public IPs")
	}

	if *gotA.Properties.IPAddress != *gotB.Properties.IPAddress {
		// Not required to differ, but if they don't the ids below still prove isolation.
		t.Logf("rg-a=%s rg-b=%s (distinct addresses, expected)", *gotA.Properties.IPAddress, *gotB.Properties.IPAddress)
	}

	// A resource-group-scoped list of rg-a must not see rg-b's public IP.
	count := 0

	pager := pips.NewListPager("rg-a", nil)
	for pager.More() {
		page, pErr := pager.NextPage(ctx)
		if pErr != nil {
			t.Fatalf("list rg-a: %v", pErr)
		}

		count += len(page.Value)
	}

	if count != 1 {
		t.Errorf("List(rg-a) returned %d public IPs, want 1 (rg-b's must not leak in)", count)
	}
}

// TestSDKPublicIPFieldsRoundTrip guards zones/dnsSettings/idleTimeoutInMinutes
// no longer being silently dropped.
func TestSDKPublicIPFieldsRoundTrip(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	p, err := pips.BeginCreateOrUpdate(ctx, "rg-1", "pip-fields", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
		Zones:    []*string{to.Ptr("1"), to.Ptr("2")},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			IdleTimeoutInMinutes: to.Ptr(int32(15)),
			DNSSettings: &armnetwork.PublicIPAddressDNSSettings{
				DomainNameLabel: to.Ptr("cloudemu-test"),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pollDone(t, p)

	got, err := pips.Get(ctx, "rg-1", "pip-fields", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if len(got.Zones) != 2 || *got.Zones[0] != "1" || *got.Zones[1] != "2" {
		t.Errorf("zones=%v want [1 2]", got.Zones)
	}

	if got.Properties == nil || got.Properties.IdleTimeoutInMinutes == nil || *got.Properties.IdleTimeoutInMinutes != 15 {
		t.Errorf("idleTimeoutInMinutes=%v want 15", got.Properties)
	}

	if got.Properties == nil || got.Properties.DNSSettings == nil ||
		got.Properties.DNSSettings.DomainNameLabel == nil || *got.Properties.DNSSettings.DomainNameLabel != "cloudemu-test" {
		t.Errorf("dnsSettings.domainNameLabel missing, want cloudemu-test")
	}

	if got.Properties == nil || got.Properties.DNSSettings == nil ||
		got.Properties.DNSSettings.Fqdn == nil || *got.Properties.DNSSettings.Fqdn == "" {
		t.Error("dnsSettings.fqdn empty, want derived FQDN")
	}
}

// TestSDKPublicIPIPConfigurationBackref guards the missing ipConfiguration
// back-reference: once a NIC attaches a public IP, a Get on that address must
// report the NIC's ipConfiguration id.
func TestSDKPublicIPIPConfigurationBackref(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	p, err := pips.BeginCreateOrUpdate(ctx, "rg-1", "pip-backref", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create pip: %v", err)
	}

	pollDone(t, p)

	// Before attachment: no back-reference.
	got, err := pips.Get(ctx, "rg-1", "pip-backref", nil)
	if err != nil {
		t.Fatalf("get before attach: %v", err)
	}

	if got.Properties != nil && got.Properties.IPConfiguration != nil {
		t.Error("ipConfiguration set before any NIC attached it")
	}

	pipID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/publicIPAddresses/pip-backref"

	nics, err := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	np, err := nics.BeginCreateOrUpdate(ctx, "rg-1", "nic-backref", armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					PublicIPAddress: &armnetwork.PublicIPAddress{ID: to.Ptr(pipID)},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create nic: %v", err)
	}

	pollDone(t, np)

	got, err = pips.Get(ctx, "rg-1", "pip-backref", nil)
	if err != nil {
		t.Fatalf("get after attach: %v", err)
	}

	wantIPConfig := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"networkInterfaces/nic-backref/ipConfigurations/ipconfig1"

	if got.Properties == nil || got.Properties.IPConfiguration == nil || got.Properties.IPConfiguration.ID == nil {
		t.Fatal("ipConfiguration backref missing after NIC attach")
	}

	if *got.Properties.IPConfiguration.ID != wantIPConfig {
		t.Errorf("ipConfiguration.id=%q want %q", *got.Properties.IPConfiguration.ID, wantIPConfig)
	}
}

// TestSDKPublicIPRePUTUpdatesInPlace guards the re-PUT idempotency fix: a
// second CreateOrUpdate to the same publicIPAddresses/{name} must mutate the
// existing allocation in place, not mint a duplicate. It asserts the critical
// invariant — LIST returns exactly ONE entry for the name — plus the updated
// idleTimeout and tags round-tripping on GET, and the address staying stable.
func TestSDKPublicIPRePUTUpdatesInPlace(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	first, err := pips.BeginCreateOrUpdate(ctx, "rg-reput", "pip-reput", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("dev")},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
			IdleTimeoutInMinutes:     to.Ptr(int32(4)),
		},
	}, nil)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	created := pollDone(t, first)

	// Second PUT to the same name with changed idleTimeout and tags.
	second, err := pips.BeginCreateOrUpdate(ctx, "rg-reput", "pip-reput", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("net")},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
			IdleTimeoutInMinutes:     to.Ptr(int32(20)),
		},
	}, nil)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}

	pollDone(t, second)

	// The critical invariant: LIST returns exactly one entry for the name.
	count := 0

	pager := pips.NewListPager("rg-reput", nil)
	for pager.More() {
		page, pErr := pager.NextPage(ctx)
		if pErr != nil {
			t.Fatalf("list: %v", pErr)
		}

		for _, v := range page.Value {
			if v.Name != nil && *v.Name == "pip-reput" {
				count++
			}
		}
	}

	if count != 1 {
		t.Fatalf("List returned %d entries for pip-reput, want 1 (re-PUT must not duplicate)", count)
	}

	got, err := pips.Get(ctx, "rg-reput", "pip-reput", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Identity (address) preserved across the re-PUT.
	if created.Properties == nil || created.Properties.IPAddress == nil ||
		got.Properties == nil || got.Properties.IPAddress == nil ||
		*got.Properties.IPAddress != *created.Properties.IPAddress {
		t.Errorf("ipAddress changed across re-PUT: created=%v got=%v", created.Properties, got.Properties)
	}

	if got.Properties.IdleTimeoutInMinutes == nil || *got.Properties.IdleTimeoutInMinutes != 20 {
		t.Errorf("idleTimeoutInMinutes=%v want 20 (updated by re-PUT)", got.Properties.IdleTimeoutInMinutes)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "prod" {
		t.Errorf("tags[env]=%v want prod (updated by re-PUT)", got.Tags["env"])
	}

	if got.Tags["team"] == nil || *got.Tags["team"] != "net" {
		t.Errorf("tags[team]=%v want net (added by re-PUT)", got.Tags["team"])
	}
}
