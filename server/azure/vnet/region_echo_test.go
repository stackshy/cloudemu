package vnet_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKPublicIPRegionAndAddress guards AZNET-10: a public IP created in
// westus2 must read back as westus2 (GET and LIST), build its FQDN from that
// region, carry a non-RFC1918 address that stays the same across reads and a
// re-PUT, and echo sku, allocation method and IP version.
func TestSDKPublicIPRegionAndAddress(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	put := func(tags map[string]*string) {
		t.Helper()

		p, perr := pips.BeginCreateOrUpdate(ctx, "rg-1", "pip-west", armnetwork.PublicIPAddress{
			Location: to.Ptr("westus2"),
			Tags:     tags,
			SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
			Properties: &armnetwork.PublicIPAddressPropertiesFormat{
				PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
				DNSSettings:              &armnetwork.PublicIPAddressDNSSettings{DomainNameLabel: to.Ptr("web1")},
			},
		}, nil)
		if perr != nil {
			t.Fatalf("create: %v", perr)
		}

		pollDone(t, p)
	}

	put(nil)

	got, err := pips.Get(ctx, "rg-1", "pip-west", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Location == nil || *got.Location != "westus2" {
		t.Errorf("location=%v want westus2", got.Location)
	}

	props := got.Properties
	if props == nil || props.DNSSettings == nil || props.DNSSettings.Fqdn == nil ||
		*props.DNSSettings.Fqdn != "web1.westus2.cloudapp.azure.com" {
		t.Errorf("fqdn=%v want web1.westus2.cloudapp.azure.com", props.DNSSettings)
	}

	if got.SKU == nil || got.SKU.Name == nil || *got.SKU.Name != armnetwork.PublicIPAddressSKUNameStandard {
		t.Errorf("sku=%v want Standard", got.SKU)
	}

	if props.PublicIPAllocationMethod == nil || *props.PublicIPAllocationMethod != armnetwork.IPAllocationMethodStatic {
		t.Errorf("allocation method=%v want Static", props.PublicIPAllocationMethod)
	}

	if props.PublicIPAddressVersion == nil || *props.PublicIPAddressVersion != armnetwork.IPVersionIPv4 {
		t.Errorf("ip version=%v want IPv4", props.PublicIPAddressVersion)
	}

	if props.IPAddress == nil {
		t.Fatal("ipAddress missing")
	}

	addr, err := netip.ParseAddr(*props.IPAddress)
	if err != nil || addr.IsPrivate() || addr.IsLoopback() || !addr.Is4() {
		t.Errorf("ipAddress=%q want a public IPv4 address", *props.IPAddress)
	}

	put(map[string]*string{"env": to.Ptr("b")})

	again, err := pips.Get(ctx, "rg-1", "pip-west", nil)
	if err != nil {
		t.Fatalf("get after re-PUT: %v", err)
	}

	if again.Properties.IPAddress == nil || *again.Properties.IPAddress != *props.IPAddress {
		t.Errorf("ipAddress changed across re-PUT: %v -> %v", *props.IPAddress, again.Properties.IPAddress)
	}

	pager := pips.NewListPager("rg-1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(page.Value) != 1 || page.Value[0].Location == nil || *page.Value[0].Location != "westus2" {
		t.Errorf("list location=%v want westus2", page.Value)
	}
}

// TestSDKNATGatewayRegionSKUIdleTimeout guards AZNET-11: a NAT gateway GET and
// LIST must report the location, sku, zones and idleTimeoutInMinutes it was
// created with, defaulting sku to Standard and the idle timeout to 4 minutes
// when the request omits them, and a re-PUT must apply new values.
func TestSDKNATGatewayRegionSKUIdleTimeout(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	nats, err := armnetwork.NewNatGatewaysClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		body     armnetwork.NatGateway
		wantIdle int32
		wantZone string
	}{
		{
			name:     "defaults",
			body:     armnetwork.NatGateway{Location: to.Ptr("westus2")},
			wantIdle: 4,
		},
		{
			name: "explicit",
			body: armnetwork.NatGateway{
				Location:   to.Ptr("westus2"),
				SKU:        &armnetwork.NatGatewaySKU{Name: to.Ptr(armnetwork.NatGatewaySKUNameStandard)},
				Zones:      []*string{to.Ptr("1")},
				Properties: &armnetwork.NatGatewayPropertiesFormat{IdleTimeoutInMinutes: to.Ptr(int32(10))},
			},
			wantIdle: 10,
			wantZone: "1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, perr := nats.BeginCreateOrUpdate(ctx, "rg-1", "nat-west", tc.body, nil)
			if perr != nil {
				t.Fatalf("create: %v", perr)
			}

			pollDone(t, p)

			got, gerr := nats.Get(ctx, "rg-1", "nat-west", nil)
			if gerr != nil {
				t.Fatalf("get: %v", gerr)
			}

			if got.Location == nil || *got.Location != "westus2" {
				t.Errorf("location=%v want westus2", got.Location)
			}

			if got.SKU == nil || got.SKU.Name == nil || *got.SKU.Name != armnetwork.NatGatewaySKUNameStandard {
				t.Errorf("sku=%v want Standard", got.SKU)
			}

			if got.Properties == nil || got.Properties.IdleTimeoutInMinutes == nil ||
				*got.Properties.IdleTimeoutInMinutes != tc.wantIdle {
				t.Errorf("idleTimeoutInMinutes=%v want %d", got.Properties, tc.wantIdle)
			}

			var zones []string
			for _, z := range got.Zones {
				zones = append(zones, *z)
			}

			if strings.Join(zones, ",") != tc.wantZone {
				t.Errorf("zones=%v want %q", zones, tc.wantZone)
			}

			page, lerr := nats.NewListPager("rg-1", nil).NextPage(ctx)
			if lerr != nil {
				t.Fatalf("list: %v", lerr)
			}

			if len(page.Value) != 1 || page.Value[0].Location == nil || *page.Value[0].Location != "westus2" {
				t.Errorf("list location=%v want westus2", page.Value)
			}
		})
	}
}
