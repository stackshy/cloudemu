package loadbalancer

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedLB populates a mock across all of its serialized state so a round-trip has
// to carry every store and map.
func seedLB(t *testing.T, m *Mock) (lbARN, tgARN string) {
	t.Helper()
	ctx := context.Background()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "web-lb", Type: "application"})
	require.NoError(t, err)
	lbARN = lb.ARN

	tg, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{
		Name: "web-tg", Protocol: "HTTP", Port: 80, TargetType: "instance",
	})
	require.NoError(t, err)
	tgARN = tg.ARN

	_, err = m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lbARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tgARN,
	})
	require.NoError(t, err)

	require.NoError(t, m.RegisterTargets(ctx, tgARN, []driver.Target{
		{ID: "vm-aaaa", Port: 80}, {ID: "vm-bbbb", Port: 8080},
	}))

	require.NoError(t, m.PutLBAttributes(ctx, lbARN, driver.LBAttributes{DeletionProtection: true}))

	return lbARN, tgARN
}

// TestSnapshotRestoreRoundTrip proves the Azure LB mock serializes its entire
// state and restores it into a fresh mock identity-preservingly.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()
	lbARN, tgARN := seedLB(t, src)

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("snapshot not stable across restore:\n first=%s\nsecond=%s", data, data2)
	}

	lbs, err := dst.DescribeLoadBalancers(ctx, []string{lbARN})
	require.NoError(t, err)
	require.Len(t, lbs, 1)
	assert.Equal(t, lbARN, lbs[0].ARN)

	health, err := dst.DescribeTargetHealth(ctx, tgARN)
	require.NoError(t, err)
	assert.Len(t, health, 2)

	attrs, err := dst.GetLBAttributes(ctx, lbARN)
	require.NoError(t, err)
	require.NotNil(t, attrs)
	assert.True(t, attrs.DeletionProtection)
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

	assert.True(t, bytes.Equal(data, data2))
}
