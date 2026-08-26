package vpc

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// The records in these stores are held by pointer, so a mutation through one
// handle is visible to — and races — every concurrent reader of the same
// record. These tests exist to fail under `-race` if the guarding is ever
// dropped again; the ENI guard was added on its own once, and the identical
// pattern in the VPC-attribute, route-table and elastic-IP paths went
// unnoticed because nothing exercised them concurrently.

const raceGoroutines = 16

func newRaceMock(t *testing.T) (*Mock, string) {
	t.Helper()

	m := New(config.NewOptions())

	vpc, err := m.CreateVPC(context.Background(), driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	return m, vpc.ID
}

func TestModifyVPCAttribute_ConcurrentWithDescribe(t *testing.T) {
	t.Parallel()

	m, vpcID := newRaceMock(t)
	ctx := context.Background()

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(2)

		go func() {
			defer wg.Done()

			on := i%2 == 0
			if err := m.ModifyVPCAttribute(ctx, vpcID, driver.VPCAttributeUpdate{
				EnableDNSHostnames: &on,
			}); err != nil {
				t.Errorf("ModifyVPCAttribute: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.DescribeVPCs(ctx, []string{vpcID}); err != nil {
				t.Errorf("DescribeVPCs: %v", err)
			}
		}()
	}

	wg.Wait()
}

// TestCreateRoute_ConcurrentCreatesAllLand pins the lost-update half of the
// route-table race: an unlocked append means two concurrent creates can read
// the same backing array and one route vanishes.
func TestCreateRoute_ConcurrentCreatesAllLand(t *testing.T) {
	t.Parallel()

	m, vpcID := newRaceMock(t)
	ctx := context.Background()

	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpcID})
	if err != nil {
		t.Fatalf("CreateRouteTable: %v", err)
	}

	// CreateRoute validates its target exists, so attach a real gateway to point
	// the concurrent routes at.
	igw, err := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{})
	if err != nil {
		t.Fatalf("CreateInternetGateway: %v", err)
	}

	if err := m.AttachInternetGateway(ctx, igw.ID, vpcID); err != nil {
		t.Fatalf("AttachInternetGateway: %v", err)
	}

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(2)

		go func() {
			defer wg.Done()

			cidr := "10." + strconv.Itoa(i+1) + ".0.0/16"
			if err := m.CreateRoute(ctx, rt.ID, cidr, igw.ID, "gateway"); err != nil {
				t.Errorf("CreateRoute: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.DescribeRouteTables(ctx, []string{rt.ID}); err != nil {
				t.Errorf("DescribeRouteTables: %v", err)
			}
		}()
	}

	wg.Wait()

	tables, err := m.DescribeRouteTables(ctx, []string{rt.ID})
	if err != nil {
		t.Fatalf("DescribeRouteTables: %v", err)
	}

	if len(tables) != 1 {
		t.Fatalf("got %d route tables, want 1", len(tables))
	}

	// The local route plus every concurrently created one.
	if want := raceGoroutines + 1; len(tables[0].Routes) != want {
		t.Errorf("got %d routes, want %d — a concurrent create was lost",
			len(tables[0].Routes), want)
	}
}

func TestAssociateAddress_ConcurrentWithDescribe(t *testing.T) {
	t.Parallel()

	m := New(config.NewOptions())
	ctx := context.Background()

	eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		// Exactly one association may succeed; the rest are already-associated.
		// Either outcome is fine — the point is that neither races the reads.
		_, _ = m.AssociateAddress(ctx, eip.AllocationID, driver.AssociateAddressInput{InstanceID: "i-test"})
	}()

	for range raceGoroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if _, err := m.DescribeAddresses(ctx, nil); err != nil {
				t.Errorf("DescribeAddresses: %v", err)
			}
		}()
	}

	wg.Wait()
}
