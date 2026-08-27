package vcn_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// TestSnapshotRestoreRoundTrip seeds a VCN, subnet, NSG, gateway and public IP,
// snapshots, restores into a fresh mock and asserts each resource comes back
// under its original OCID with cross-references intact — the subnet still points
// at its VCN, and the recorded scope/creation time survive.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := t.Context()
	src := newMock(t)

	v := newVCN(t, src, vcnCIDR)

	subnet, err := src.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: subnetCIDR})
	require.NoError(t, err)

	sg, err := src.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{
		Name: "app", Description: "app tier", VPCID: v.ID,
	})
	require.NoError(t, err)

	igw, err := src.CreateInternetGateway(ctx, driver.InternetGatewayConfig{})
	require.NoError(t, err)

	eip, err := src.AllocateAddress(ctx, driver.ElasticIPConfig{})
	require.NoError(t, err)

	// Move the subnet into a non-default compartment so the scopes store is
	// exercised, not just the create-time default.
	src.SetScope(subnet.ID, scope.Scope{Compartment: otherCompartment})

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	// VCN restored under its OCID with its default resources.
	vcns, err := dst.DescribeVPCs(ctx, []string{v.ID})
	require.NoError(t, err)
	require.Len(t, vcns, 1)
	assert.Equal(t, vcnCIDR, vcns[0].CIDRBlock)

	// Subnet restored and its VCNID cross-reference still resolves.
	subnets, err := dst.DescribeSubnets(ctx, []string{subnet.ID})
	require.NoError(t, err)
	require.Len(t, subnets, 1)
	assert.Equal(t, v.ID, subnets[0].VPCID)

	// Security group and gateway and public IP restored under their OCIDs.
	sgs, err := dst.DescribeSecurityGroups(ctx, []string{sg.ID})
	require.NoError(t, err)
	require.Len(t, sgs, 1)

	igws, err := dst.DescribeInternetGateways(ctx, []string{igw.ID})
	require.NoError(t, err)
	require.Len(t, igws, 1)

	eips, err := dst.DescribeAddresses(ctx, []string{eip.AllocationID})
	require.NoError(t, err)
	require.Len(t, eips, 1)

	// The side-stores (scope, creation time) survived.
	assert.Equal(t, otherCompartment, dst.Scope(subnet.ID).Compartment)
	assert.NotEmpty(t, dst.Created(v.ID), "creation time restored")
}

// TestSnapshotRestoreEmptyNilSafe confirms an empty mock round-trips cleanly.
func TestSnapshotRestoreEmptyNilSafe(t *testing.T) {
	ctx := t.Context()
	src := newMock(t)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	_, err = dst.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: vcnCIDR})
	require.NoError(t, err)
}
