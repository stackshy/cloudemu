package vpc

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// seedNetworking populates a mock across many of its stores (VPC, subnet,
// security group, route table, IGW, NAT gateway, EIP, ENI, peering) so a
// round-trip has to carry every one of them. It returns the ids a caller asserts
// on after a restore.
func seedNetworking(t *testing.T, m *Mock) (vpcID, subnetID, sgID, eniID string) {
	t.Helper()
	ctx := context.Background()

	vpc := createTestVPC(m)
	vpcID = vpc.ID

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpcID, CIDRBlock: "10.0.1.0/24"})
	requireNoError(t, err)
	subnetID = subnet.ID

	sg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{
		Name: "web", Description: "web tier", VPCID: vpcID,
	})
	requireNoError(t, err)
	sgID = sg.ID

	_, err = m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpcID})
	requireNoError(t, err)

	attachTestIGW(t, m, vpcID)

	eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{})
	requireNoError(t, err)

	_, err = m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: subnetID, AllocationID: eip.AllocationID})
	requireNoError(t, err)

	eni, err := m.CreateNetworkInterface(ctx, subnetID, "app eni", []string{sgID}, nil)
	requireNoError(t, err)
	eniID = eni.ID

	return vpcID, subnetID, sgID, eniID
}

// TestSnapshotRestoreRoundTrip proves the VPC mock serializes its entire state
// and restores it into a fresh mock identity-preservingly: re-snapshotting the
// restored mock yields byte-identical JSON (so every store round-tripped), and
// the seeded resources come back under their original ids.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()
	vpcID, subnetID, sgID, eniID := seedNetworking(t, src)

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	// Re-snapshotting the restored mock must reproduce the exact bytes: any store
	// the snapshot missed (or restored under a different key) would diverge here.
	data2, err := dst.Snapshot(ctx, true)
	requireNoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("snapshot not stable across restore:\n first=%s\nsecond=%s", data, data2)
	}

	// Spot-check that the seeded resources resolve under their original ids.
	vpcs, err := dst.DescribeVPCs(ctx, []string{vpcID})
	requireNoError(t, err)
	if len(vpcs) != 1 || vpcs[0].ID != vpcID {
		t.Fatalf("restored VPCs = %v, want id %q", vpcs, vpcID)
	}

	subnets, err := dst.DescribeSubnets(ctx, []string{subnetID})
	requireNoError(t, err)
	if len(subnets) != 1 || subnets[0].VPCID != vpcID {
		t.Fatalf("restored subnet = %v, want VPCID %q", subnets, vpcID)
	}

	sgs, err := dst.DescribeSecurityGroups(ctx, []string{sgID})
	requireNoError(t, err)
	if len(sgs) != 1 || sgs[0].ID != sgID {
		t.Fatalf("restored SGs = %v, want id %q", sgs, sgID)
	}

	// The ENI keeps its id AND its subnet/VPC cross-references (the security-group
	// binding on the stored eniData is covered by the byte-equality round-trip
	// above; DescribeNetworkInterfaces does not surface it).
	enis, err := dst.DescribeNetworkInterfaces(ctx, []string{eniID})
	requireNoError(t, err)
	if len(enis) != 1 {
		t.Fatalf("restored %d ENIs, want 1", len(enis))
	}
	if enis[0].SubnetID != subnetID {
		t.Fatalf("restored ENI subnet = %q, want %q", enis[0].SubnetID, subnetID)
	}
	if enis[0].VPCID != vpcID {
		t.Fatalf("restored ENI VPCID = %q, want %q", enis[0].VPCID, vpcID)
	}
}

// TestSnapshotEmpty confirms a fresh mock snapshots and restores without error
// (all stores empty) — the degenerate round-trip persist relies on.
func TestSnapshotEmpty(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, false)
	requireNoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("empty snapshot not stable: %s vs %s", data, data2)
	}
}
