package vnet

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func newNICMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions())
}

func dynamicConfig(subnetID, cidr string) driver.AzureNICConfig {
	return driver.AzureNICConfig{
		Location: "eastus",
		IPConfigs: []driver.AzureIPConfig{{
			Name:       "ipconfig1",
			SubnetID:   subnetID,
			SubnetCIDR: cidr,
			Primary:    true,
		}},
	}
}

func TestNICCreateAllocatesPrivateIP(t *testing.T) {
	m := newNICMock(t)
	ctx := context.Background()

	nic, err := m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-a", dynamicConfig("subnet-1", "10.0.1.0/24"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got := nic.IPConfigs[0].PrivateIP; got != "10.0.1.4" {
		t.Errorf("privateIP=%q want 10.0.1.4", got)
	}

	if nic.IPConfigs[0].AllocationMethod != "Dynamic" {
		t.Errorf("allocationMethod=%q want Dynamic (default)", nic.IPConfigs[0].AllocationMethod)
	}

	if nic.MACAddress == "" || nic.ResourceGUID == "" || nic.ETag == "" {
		t.Errorf("server-assigned fields empty: mac=%q guid=%q etag=%q", nic.MACAddress, nic.ResourceGUID, nic.ETag)
	}
}

func TestNICAllocationIsSequentialAndCollisionFree(t *testing.T) {
	m := newNICMock(t)
	ctx := context.Background()

	a, _ := m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-a", dynamicConfig("subnet-1", "10.0.1.0/24"))
	b, _ := m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-b", dynamicConfig("subnet-1", "10.0.1.0/24"))

	if a.IPConfigs[0].PrivateIP != "10.0.1.4" || b.IPConfigs[0].PrivateIP != "10.0.1.5" {
		t.Errorf("IPs = %q, %q; want 10.0.1.4, 10.0.1.5", a.IPConfigs[0].PrivateIP, b.IPConfigs[0].PrivateIP)
	}

	// A NIC in a different subnet starts its own range.
	c, _ := m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-c", dynamicConfig("subnet-2", "10.0.2.0/24"))
	if c.IPConfigs[0].PrivateIP != "10.0.2.4" {
		t.Errorf("subnet-2 IP=%q want 10.0.2.4", c.IPConfigs[0].PrivateIP)
	}
}

func TestNICIdempotentUpdateKeepsIdentityAndIP(t *testing.T) {
	m := newNICMock(t)
	ctx := context.Background()

	first, _ := m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-a", dynamicConfig("subnet-1", "10.0.1.0/24"))
	second, err := m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-a", dynamicConfig("subnet-1", "10.0.1.0/24"))
	if err != nil {
		t.Fatalf("re-put: %v", err)
	}

	list, _ := m.ListNetworkInterfaces(ctx, "rg-1")
	if len(list) != 1 {
		t.Fatalf("list=%d want 1 (idempotent PUT must not duplicate)", len(list))
	}

	if second.ResourceGUID != first.ResourceGUID {
		t.Errorf("resourceGuid changed on update: %q -> %q", first.ResourceGUID, second.ResourceGUID)
	}

	if second.IPConfigs[0].PrivateIP != "10.0.1.4" {
		t.Errorf("re-PUT reallocated a different IP %q, want stable 10.0.1.4", second.IPConfigs[0].PrivateIP)
	}
}

func TestNICStaticAllocation(t *testing.T) {
	m := newNICMock(t)
	ctx := context.Background()

	cfg := driver.AzureNICConfig{
		Location: "eastus",
		IPConfigs: []driver.AzureIPConfig{{
			Name:             "ipconfig1",
			SubnetID:         "subnet-1",
			SubnetCIDR:       "10.0.1.0/24",
			AllocationMethod: "Static",
			PrivateIP:        "10.0.1.50",
		}},
	}

	nic, err := m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-static", cfg)
	if err != nil {
		t.Fatalf("create static: %v", err)
	}

	if nic.IPConfigs[0].PrivateIP != "10.0.1.50" {
		t.Errorf("static IP=%q want 10.0.1.50 (submitted value must be kept)", nic.IPConfigs[0].PrivateIP)
	}
}

func TestNICStaticRequiresAddress(t *testing.T) {
	m := newNICMock(t)

	cfg := driver.AzureNICConfig{
		IPConfigs: []driver.AzureIPConfig{{Name: "ipconfig1", AllocationMethod: "Static"}},
	}

	_, err := m.CreateOrUpdateNetworkInterface(context.Background(), "rg-1", "nic-bad", cfg)
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("err=%v want InvalidArgument for Static without privateIPAddress", err)
	}
}

func TestNICResourceGroupScoping(t *testing.T) {
	m := newNICMock(t)
	ctx := context.Background()

	_, _ = m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-a", dynamicConfig("subnet-1", "10.0.1.0/24"))
	_, _ = m.CreateOrUpdateNetworkInterface(ctx, "rg-2", "nic-b", dynamicConfig("subnet-9", "10.9.1.0/24"))

	rg1, _ := m.ListNetworkInterfaces(ctx, "rg-1")
	if len(rg1) != 1 || rg1[0].Name != "nic-a" {
		t.Errorf("ListNetworkInterfaces(rg-1)=%v want just nic-a", rg1)
	}

	// A NIC in rg-1 must not be resolvable from rg-2.
	if _, err := m.GetNetworkInterface(ctx, "rg-2", "nic-a"); !cerrors.IsNotFound(err) {
		t.Errorf("cross-RG Get err=%v want NotFound", err)
	}

	all, _ := m.ListNetworkInterfaces(ctx, "")
	if len(all) != 2 {
		t.Errorf("subscription-wide list=%d want 2", len(all))
	}
}

func TestNICDistinctMACAndGUID(t *testing.T) {
	m := newNICMock(t)
	ctx := context.Background()

	a, _ := m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-a", dynamicConfig("subnet-1", "10.0.1.0/24"))
	b, _ := m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-b", dynamicConfig("subnet-1", "10.0.1.0/24"))

	if a.MACAddress == b.MACAddress {
		t.Errorf("both NICs got the same MAC %q; each NIC must get a distinct address", a.MACAddress)
	}

	if a.ResourceGUID == b.ResourceGUID {
		t.Errorf("both NICs got the same resourceGuid %q; must be distinct", a.ResourceGUID)
	}
}

func TestNICSubnetExhaustedIsResourceExhausted(t *testing.T) {
	m := newNICMock(t)
	ctx := context.Background()

	// A /30 has hosts .0-.3, all reserved by Azure, so the first assignable
	// host (.4) is already outside the block — allocation must report exhaustion
	// (which the wire layer maps to HTTP 400, not 500).
	_, err := m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-tight", dynamicConfig("subnet-tiny", "10.0.0.0/30"))
	if cerrors.GetCode(err) != cerrors.ResourceExhausted {
		t.Errorf("err=%v want ResourceExhausted for a /30 with no assignable host", err)
	}
}

func TestNICDeleteAttachedIsRejected(t *testing.T) {
	m := newNICMock(t)
	ctx := context.Background()

	_, _ = m.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-a", dynamicConfig("subnet-1", "10.0.1.0/24"))

	// Simulate attachment to a VM.
	nic, _ := m.nics.Get(nicKey("rg-1", "nic-a"))
	nic.VirtualMachineID = "/subscriptions/s/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm-a"

	if err := m.DeleteNetworkInterface(ctx, "rg-1", "nic-a"); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("delete attached err=%v want FailedPrecondition", err)
	}
}
