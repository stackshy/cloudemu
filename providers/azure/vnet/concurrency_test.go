package vnet

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stretchr/testify/require"
)

// TestConcurrentSubnetMutationAndRead hammers the copy-on-write mutators
// (UpdateSubnetTags/RemoveSubnetTags/UpdateSubnetCIDR and the NSG rule/tag
// mutators) against concurrent DescribeSubnets/DescribeSecurityGroups readers
// on the same resources from many goroutines. It exists to be run with -race:
// before the fix the mutators reassigned fields on the shared stored pointer
// while readers walked its maps/slices, a genuine data race. The readers copy
// every map/slice they touch, so a clean run also proves no reader observes a
// half-updated snapshot or a dangling reference.
func TestConcurrentSubnetMutationAndRead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	sn, err := m.CreateSubnet(ctx, driver.SubnetConfig{
		VPCID:     vpcID,
		CIDRBlock: "10.0.1.0/24",
		Tags:      map[string]string{"seed": "1"},
	})
	require.NoError(t, err)
	subnetID := sn.ID

	sg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{
		Name:  "nsg-race",
		VPCID: vpcID,
	})
	require.NoError(t, err)
	nsgID := sg.ID

	const (
		workers = 24
		iters   = 200
	)

	var wg sync.WaitGroup

	// Writers: mutate subnet tags/CIDR and NSG rules/tags.
	for w := range workers {
		wg.Add(1)

		go func(w int) {
			defer wg.Done()

			for i := range iters {
				key := fmt.Sprintf("k%d", w)
				_ = m.UpdateSubnetTags(ctx, subnetID, map[string]string{key: fmt.Sprintf("%d", i)})
				_ = m.UpdateSubnetCIDR(ctx, subnetID, fmt.Sprintf("10.0.%d.0/24", (i%250)+1))
				_ = m.RemoveSubnetTags(ctx, subnetID, []string{key})

				rule := driver.SecurityRule{Protocol: "tcp", FromPort: w, ToPort: w, CIDR: "0.0.0.0/0"}
				_ = m.AddIngressRule(ctx, nsgID, rule)
				_ = m.UpdateSecurityGroupTags(ctx, nsgID, map[string]string{key: "v"})
				_ = m.RemoveIngressRule(ctx, nsgID, rule)
			}
		}(w)
	}

	// Readers: describe by id and describe-all, deep-reading every field so the
	// race detector sees concurrent reads of the same backing maps/slices.
	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range iters {
				subs, derr := m.DescribeSubnets(ctx, []string{subnetID})
				require.NoError(t, derr)

				for i := range subs {
					_ = subs[i].CIDRBlock
					for k, v := range subs[i].Tags {
						_, _ = k, v
					}
				}

				all, derr := m.DescribeSubnets(ctx, nil)
				require.NoError(t, derr)
				_ = len(all)

				sgs, derr := m.DescribeSecurityGroups(ctx, []string{nsgID})
				require.NoError(t, derr)

				for i := range sgs {
					_ = len(sgs[i].IngressRules)
					for k := range sgs[i].Tags {
						_ = k
					}
				}
			}
		}()
	}

	wg.Wait()

	// Final state must be readable and self-consistent.
	subs, err := m.DescribeSubnets(ctx, []string{subnetID})
	require.NoError(t, err)
	require.Len(t, subs, 1)
	require.Equal(t, vpcID, subs[0].VPCID)
}

// TestConcurrentAssociateVsRelease drives the atomic in-use guards: concurrent
// AssociateAddress / DisassociateAddress / ReleaseAddress on the same public IP
// plus DescribeAddresses readers. The guard invariant is that an associated IP
// is never released out from under its association — ReleaseAddress must return
// FailedPrecondition while the IP is bound — and that the check-and-act never
// tears under -race. A clean run with a consistent final read proves it.
func TestConcurrentAssociateVsRelease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := newTestMock()

	const (
		workers = 16
		iters   = 300
	)

	// Each worker owns its own public IP to exercise the guard independently,
	// then a shared loser/winner race releases while others associate.
	allocIDs := make([]string, workers)

	for w := range workers {
		eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{})
		require.NoError(t, err)
		allocIDs[w] = eip.AllocationID
	}

	var wg sync.WaitGroup

	for w := range workers {
		wg.Add(1)

		go func(w int) {
			defer wg.Done()

			alloc := allocIDs[w]

			for range iters {
				assocID, err := m.AssociateAddress(ctx, alloc, driver.AssociateAddressInput{InstanceID: "vm-1"})
				if err == nil {
					// While associated, a release MUST be refused, never delete it.
					relErr := m.ReleaseAddress(ctx, alloc)
					require.Error(t, relErr)

					_ = m.DisassociateAddress(ctx, assocID)
				}
			}
		}(w)
	}

	// Concurrent readers over the whole set.
	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range iters {
				addrs, err := m.DescribeAddresses(ctx, nil)
				require.NoError(t, err)

				for i := range addrs {
					_ = addrs[i].AssociationID
					_ = addrs[i].InstanceID
				}
			}
		}()
	}

	wg.Wait()

	// Every IP must still exist and be readable — none released while bound.
	addrs, err := m.DescribeAddresses(ctx, nil)
	require.NoError(t, err)
	require.Len(t, addrs, workers)
}

// TestConcurrentNATBindVsDelete exercises the atomic public-IP binding guard
// through the NAT gateway path: many goroutines race to bind the same single
// public IP to a new NAT gateway, then delete it (freeing the IP). At most one
// binding can hold the IP at any instant; the guard must never let two NAT
// gateways claim it, and the store must stay consistent under -race.
func TestConcurrentNATBindVsDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := newTestMock()

	eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{})
	require.NoError(t, err)
	alloc := eip.AllocationID

	const (
		workers = 16
		iters   = 200
	)

	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range iters {
				nat, cerr := m.CreateNATGateway(ctx, driver.NATGatewayConfig{AllocationID: alloc})
				if cerr != nil {
					// Contended: the IP was already bound. Expected.
					continue
				}

				// We won the binding; releasing the IP now must fail (still bound),
				// proving the association actually took hold.
				require.Error(t, m.ReleaseAddress(ctx, alloc))

				require.NoError(t, m.DeleteNATGateway(ctx, nat.ID))
			}
		}()
	}

	wg.Wait()

	// The IP is free again and releasable, and no NAT gateway leaked a binding.
	addrs, err := m.DescribeAddresses(ctx, []string{alloc})
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	require.Empty(t, addrs[0].AssociationID)
}

// TestConcurrentReplaceTagsVsDescribe covers the ARM UpdateTags PATCH mutators
// (Replace*Tags), which previously reassigned .Tags on the shared stored pointer
// under Update() instead of copy-on-write. It fires ReplaceVPCTags /
// ReplaceSecurityGroupTags against concurrent DescribeVPCs / DescribeSecurityGroups
// readers that walk the returned tag maps. Run with -race, a write at the tag
// reassignment used to race the reader's map iteration in toVPCInfo/toSGInfo.
func TestConcurrentReplaceTagsVsDescribe(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	sg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "nsg-tags", VPCID: vpcID})
	require.NoError(t, err)
	nsgID := sg.ID

	const (
		workers = 24
		iters   = 250
	)

	var wg sync.WaitGroup

	for w := range workers {
		wg.Add(1)

		go func(w int) {
			defer wg.Done()

			for i := range iters {
				tags := map[string]string{
					fmt.Sprintf("k%d", w): fmt.Sprintf("%d", i),
					"shared":              fmt.Sprintf("%d", w),
				}
				require.NoError(t, m.ReplaceVPCTags(ctx, vpcID, tags))
				require.NoError(t, m.ReplaceSecurityGroupTags(ctx, nsgID, tags))
			}
		}(w)
	}

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range iters {
				vpcs, derr := m.DescribeVPCs(ctx, []string{vpcID})
				require.NoError(t, derr)

				for i := range vpcs {
					for k, v := range vpcs[i].Tags {
						_, _ = k, v
					}
				}

				sgs, derr := m.DescribeSecurityGroups(ctx, []string{nsgID})
				require.NoError(t, derr)

				for i := range sgs {
					for k := range sgs[i].Tags {
						_ = k
					}
				}
			}
		}()
	}

	wg.Wait()
}

// createTestNIC creates a single-ipConfiguration NIC keyed by (rg, name).
func createTestNIC(t *testing.T, m *Mock, rg, name string) {
	t.Helper()

	_, err := m.CreateOrUpdateNetworkInterface(context.Background(), rg, name, driver.AzureNICConfig{
		Location: "eastus",
		IPConfigs: []driver.AzureIPConfig{
			{Name: "ipconfig1", AllocationMethod: "Static", PrivateIP: "10.0.0.10", Primary: true},
		},
	})
	require.NoError(t, err)
}

// TestConcurrentNICMutationVsSnapshot drives NIC AttachNetworkInterface /
// DetachNetworkInterface / ReplaceNetworkInterfaceTags against concurrent
// Snapshot() calls. Snapshot dumps m.nics via store.All()+json.Marshal without
// nicMu; before the fix Attach/Detach mutated *nicData in place, so the Marshal
// read raced the field write. Copy-on-write through the store lock makes the
// captured pointer immutable, so this must be -race clean.
func TestConcurrentNICMutationVsSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := newTestMock()

	const (
		nics    = 8
		workers = 16
		iters   = 200
	)

	for i := range nics {
		createTestNIC(t, m, "rg-1", fmt.Sprintf("nic-%d", i))
	}

	var wg sync.WaitGroup

	// Mutators: attach, detach and replace-tags across the NIC set.
	for w := range workers {
		wg.Add(1)

		go func(w int) {
			defer wg.Done()

			for i := range iters {
				name := fmt.Sprintf("nic-%d", (w+i)%nics)
				vmID := fmt.Sprintf("/subscriptions/s/vm-%d", w)

				_ = m.AttachNetworkInterface(ctx, "rg-1", name, vmID)
				_ = m.ReplaceNetworkInterfaceTags(ctx, "rg-1", name, map[string]string{"w": fmt.Sprintf("%d", w)})
				_ = m.DetachNetworkInterface(ctx, "rg-1", name, vmID)
			}
		}(w)
	}

	// Snapshotters: dump the whole mock concurrently.
	for range workers / 2 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range iters {
				data, err := m.Snapshot(ctx, false)
				require.NoError(t, err)
				require.NotEmpty(t, data)
			}
		}()
	}

	wg.Wait()

	// Final snapshot must round-trip cleanly.
	data, err := m.Snapshot(ctx, false)
	require.NoError(t, err)
	require.NoError(t, m.Restore(ctx, data))
}
