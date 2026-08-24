package vcn_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// The stores hand back pointers, so an update mutates the very record a
// concurrent Describe is projecting. These tests fail under -race if the
// Mock's mutex is dropped from the read or write paths, and the 1:1 assignment
// test fails outright: its check and its write are only atomic together.

const raceGoroutines = 16

func TestDescribeConcurrentWithMutation(t *testing.T) {
	t.Parallel()

	m := newMock(t)
	ctx := t.Context()
	parent := newVCN(t, m, vcnCIDR)

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: parent.ID, CIDRBlock: subnetCIDR})
	require.NoError(t, err)

	nsg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{VPCID: parent.ID, Name: "web"})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(6)

		go func() {
			defer wg.Done()

			dns := i%2 == 0
			if err := m.ModifyVPCAttribute(ctx, parent.ID, driver.VPCAttributeUpdate{
				EnableDNSSupport: &dns, EnableDNSHostnames: &dns,
			}); err != nil {
				t.Errorf("ModifyVPCAttribute: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.DescribeVPCs(ctx, nil); err != nil {
				t.Errorf("DescribeVPCs: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if err := m.UpdateSubnetTags(ctx, subnet.ID, map[string]string{"env": "test"}); err != nil {
				t.Errorf("UpdateSubnetTags: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.DescribeSubnets(ctx, []string{subnet.ID}); err != nil {
				t.Errorf("DescribeSubnets: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			rule := driver.SecurityRule{Protocol: "tcp", FromPort: 8000 + i, ToPort: 8000 + i, CIDR: "0.0.0.0/0"}
			if err := m.AddIngressRule(ctx, nsg.ID, rule); err != nil {
				t.Errorf("AddIngressRule: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.DescribeSecurityGroups(ctx, nil); err != nil {
				t.Errorf("DescribeSecurityGroups: %v", err)
			}
		}()
	}

	wg.Wait()
}

// TestConcurrentCIDRAndGatewayMutation walks the CIDR list and the local
// peering gateways while both are being written. Each exported method takes
// the Mock's lock once, so a re-entrant call would hang here rather than fail.
func TestConcurrentCIDRAndGatewayMutation(t *testing.T) {
	t.Parallel()

	m := newMock(t)
	ctx := t.Context()
	parent := newVCN(t, m, vcnCIDR)

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(5)

		go func() {
			defer wg.Done()

			block := fmt.Sprintf("172.%d.0.0/16", i+16)
			if err := m.AddVCNCIDR(ctx, parent.ID, block); err != nil {
				t.Errorf("AddVCNCIDR: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			m.VCNCIDRs(parent.ID)
		}()

		go func() {
			defer wg.Done()

			if _, err := m.DescribeVPCs(ctx, nil); err != nil {
				t.Errorf("DescribeVPCs: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			g, err := m.CreateLocalPeeringGateway(ctx, parent.ID, map[string]string{"env": "test"})
			if err != nil {
				t.Errorf("CreateLocalPeeringGateway: %v", err)
				return
			}

			if err := m.DeleteLocalPeeringGateway(ctx, g.ID); err != nil {
				t.Errorf("DeleteLocalPeeringGateway: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			gateways, err := m.DescribeLocalPeeringGateways(ctx, nil)
			if err != nil {
				t.Errorf("DescribeLocalPeeringGateways: %v", err)
				return
			}

			for i := range gateways {
				_ = gateways[i].PeerAdvertisedCIDRs
			}
		}()
	}

	wg.Wait()

	assert.Len(t, m.VCNCIDRs(parent.ID), raceGoroutines+1, "every block lands exactly once")
}

// TestConcurrentConnectPeersOnce drives the connect check-then-act window: each
// goroutine reads both gateways as NEW before any of them writes.
func TestConcurrentConnectPeersOnce(t *testing.T) {
	t.Parallel()

	m := newMock(t)
	ctx := t.Context()
	local := newVCN(t, m, vcnCIDR)
	remote := newVCN(t, m, "192.168.0.0/16")

	here, err := m.CreateLocalPeeringGateway(ctx, local.ID, nil)
	require.NoError(t, err)

	there, err := m.CreateLocalPeeringGateway(ctx, remote.ID, nil)
	require.NoError(t, err)

	var (
		wg     sync.WaitGroup
		peered atomic.Int64
	)

	start := make(chan struct{})

	for range raceGoroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			if err := m.ConnectLocalPeeringGateways(ctx, here.ID, there.ID); err == nil {
				peered.Add(1)
			} else if code := cerrors.GetCode(err); code != cerrors.FailedPrecondition {
				t.Errorf("ConnectLocalPeeringGateways: unexpected code %v: %v", code, err)
			}
		}()
	}

	close(start)
	wg.Wait()

	assert.Equal(t, int64(1), peered.Load(), "a pair of gateways peers once")

	peerings, err := m.DescribePeeringConnections(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, peerings, 1, "exactly one connection stands behind the pair")
}

// TestConcurrentAssociateAddressKeepsOneToOne drives the check-then-act
// window: every goroutine reads the private IP as free, then writes its own
// public IP record. Only one may win.
func TestConcurrentAssociateAddressKeepsOneToOne(t *testing.T) {
	t.Parallel()

	m := newMock(t)
	ctx := t.Context()
	parent := newVCN(t, m, vcnCIDR)

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: parent.ID, CIDRBlock: subnetCIDR})
	require.NoError(t, err)

	vnic, err := m.CreateNetworkInterface(ctx, subnet.ID, "primary", nil, nil)
	require.NoError(t, err)

	privateIPs, err := m.DescribePrivateIPs(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, privateIPs)
	require.Equal(t, vnic.ID, privateIPs[0].VNICID)

	target := privateIPs[0].ID
	allocations := make([]string, 0, raceGoroutines)

	for range raceGoroutines {
		ip, allocErr := m.AllocateAddress(ctx, driver.ElasticIPConfig{})
		require.NoError(t, allocErr)

		allocations = append(allocations, ip.AllocationID)
	}

	var (
		wg       sync.WaitGroup
		assigned atomic.Int64
	)

	start := make(chan struct{})

	for _, allocationID := range allocations {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			if _, err := m.AssociateAddress(ctx, allocationID, driver.AssociateAddressInput{InstanceID: target}); err == nil {
				assigned.Add(1)
			} else if code := cerrors.GetCode(err); code != cerrors.FailedPrecondition {
				t.Errorf("AssociateAddress: unexpected code %v: %v", code, err)
			}
		}()
	}

	close(start)
	wg.Wait()

	assert.Equal(t, int64(1), assigned.Load(), "a private IP holds exactly one public IP")

	addresses, err := m.DescribeAddresses(ctx, nil)
	require.NoError(t, err)

	holders := 0

	for i := range addresses {
		if addresses[i].AssociationID == target {
			holders++
		}
	}

	assert.Equal(t, 1, holders, "exactly one stored address names the private IP")
}
