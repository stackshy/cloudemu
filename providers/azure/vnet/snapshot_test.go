package vnet

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRestoreRoundTrip proves the VNet mock serializes its entire state
// and restores it into a fresh mock identity-preservingly: re-snapshotting the
// restored mock yields byte-identical JSON (so every store round-tripped,
// including the ARM metadata and NIC stores), and the seeded resources come back
// under their original ids.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()
	vpcID := createTestVPC(t, src)

	subnet, err := src.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpcID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	sg, err := src.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "nsg1", VPCID: vpcID})
	require.NoError(t, err)

	nic, err := src.CreateOrUpdateNetworkInterface(ctx, "rg1", "nic1", driver.AzureNICConfig{
		Location: "eastus",
		IPConfigs: []driver.AzureIPConfig{
			{Name: "ipcfg1", SubnetID: subnet.ID, AllocationMethod: "Dynamic"},
		},
	})
	require.NoError(t, err)

	src.PutAzureApplicationSecurityGroup(ctx, driver.AzureApplicationSecurityGroup{
		Name: "asg1", ResourceGroup: "rg1", Location: "eastus", Tags: map[string]string{"env": "prod"},
	})

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(data2), "snapshot must be stable across restore")

	subnets, err := dst.DescribeSubnets(ctx, []string{subnet.ID})
	require.NoError(t, err)
	require.Len(t, subnets, 1)
	assert.Equal(t, vpcID, subnets[0].VPCID, "subnet keeps its VNet cross-reference")

	sgs, err := dst.DescribeSecurityGroups(ctx, []string{sg.ID})
	require.NoError(t, err)
	require.Len(t, sgs, 1)
	assert.Equal(t, sg.ID, sgs[0].ID)

	// The NIC survives under its ARM key with its subnet cross-reference intact.
	got, err := dst.GetNetworkInterface(ctx, "rg1", "nic1")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.IPConfigs, 1)
	assert.Equal(t, subnet.ID, got.IPConfigs[0].SubnetID)
	assert.Equal(t, nic.MACAddress, got.MACAddress)

	// The application security group survives under its ARM addressing pair.
	asg, ok := dst.GetAzureApplicationSecurityGroup(ctx, "rg1", "asg1")
	require.True(t, ok, "ASG must survive snapshot/restore")
	assert.Equal(t, "prod", asg.Tags["env"])
}

// TestSnapshotEmpty confirms a fresh mock snapshots and restores without error.
func TestSnapshotEmpty(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()
	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(data2))
}
