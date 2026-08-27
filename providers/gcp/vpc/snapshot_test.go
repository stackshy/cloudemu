package vpc

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRestoreRoundTrip proves the GCP VPC mock serializes its entire
// state and restores it into a fresh mock identity-preservingly: re-snapshotting
// the restored mock yields byte-identical JSON (so every store round-tripped),
// and the seeded resources come back under their original self-link ids.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()

	vpc, err := src.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := src.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	sg, err := src.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "fw1", VPCID: vpc.ID})
	require.NoError(t, err)

	_, err = src.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpc.ID})
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(data2), "snapshot must be stable across restore")

	vpcs, err := dst.DescribeVPCs(ctx, []string{vpc.ID})
	require.NoError(t, err)
	require.Len(t, vpcs, 1)
	assert.Equal(t, vpc.ID, vpcs[0].ID)

	subnets, err := dst.DescribeSubnets(ctx, []string{subnet.ID})
	require.NoError(t, err)
	require.Len(t, subnets, 1)
	assert.Equal(t, vpc.ID, subnets[0].VPCID, "subnet keeps its VPC cross-reference")

	sgs, err := dst.DescribeSecurityGroups(ctx, []string{sg.ID})
	require.NoError(t, err)
	require.Len(t, sgs, 1)
	assert.Equal(t, sg.ID, sgs[0].ID)
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
