package vnet

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestPublicIPAndNATGatewayRegionSurviveRestore proves the Azure-only public IP
// and NAT gateway fields (location, address, fqdn, sku, idle timeout) are held
// by the provider and come back unchanged after a snapshot restore.
func TestPublicIPAndNATGatewayRegionSurviveRestore(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	eip, err := src.AllocateAddress(ctx, driver.ElasticIPConfig{Location: "westus2", DNSDomainNameLabel: "web1"})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}

	nat, err := src.CreateNATGateway(ctx, driver.NATGatewayConfig{Location: "westus2"})
	if err != nil {
		t.Fatalf("CreateNATGateway: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	eips, _ := dst.DescribeAddresses(ctx, []string{eip.AllocationID})
	if len(eips) != 1 {
		t.Fatalf("restored public IPs = %d, want 1", len(eips))
	}

	got := eips[0]
	if got.Location != "westus2" || got.PublicIP != eip.PublicIP || got.DNSFQDN != "web1.westus2.cloudapp.azure.com" {
		t.Errorf("restored public IP = %+v, want westus2 / %s / web1.westus2.cloudapp.azure.com", got, eip.PublicIP)
	}

	nats, _ := dst.DescribeNATGateways(ctx, []string{nat.ID})
	if len(nats) != 1 {
		t.Fatalf("restored NAT gateways = %d, want 1", len(nats))
	}

	if n := nats[0]; n.Location != "westus2" || n.SKU != "Standard" || n.IdleTimeoutMinutes != 4 {
		t.Errorf("restored NAT gateway = %+v, want westus2 / Standard / 4", n)
	}
}

// TestConcurrentPublicIPAllocationsGetUniqueAddresses proves choosing an
// address and storing the allocation is atomic: many concurrent creates never
// share an address.
func TestConcurrentPublicIPAllocationsGetUniqueAddresses(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	const n = 64

	addrs := make([]string, n)

	var wg sync.WaitGroup

	for i := range n {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{Location: "westus2"})
			if err != nil {
				t.Errorf("AllocateAddress: %v", err)
				return
			}

			addrs[i] = eip.PublicIP
		}(i)
	}

	wg.Wait()

	seen := make(map[string]bool, n)
	for _, a := range addrs {
		if seen[a] {
			t.Fatalf("address %s handed out twice", a)
		}

		seen[a] = true
	}
}
