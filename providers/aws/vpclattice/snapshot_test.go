package vpclattice

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

// TestSnapshotRoundTripVPCLattice proves a snapshot/restore round-trip preserves
// the resource stores, the target-group-keyed targets store, and the ARN-keyed
// tag store under their original identities.
func TestSnapshotRoundTripVPCLattice(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	sn, err := src.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn"})
	require.NoError(t, err)

	svc, err := src.CreateService(ctx, &driver.CreateServiceInput{
		Name: "svc", Tags: map[string]string{"env": "prod"},
	})
	require.NoError(t, err)

	tg, err := src.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "tg", Type: "IP"})
	require.NoError(t, err)

	_, _, err = src.RegisterTargets(ctx, tg.ID, []driver.RegisteredTarget{{ID: "10.0.0.1", Port: 443}})
	require.NoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))

	gotSN, err := dst.GetServiceNetwork(ctx, sn.ID)
	require.NoError(t, err)
	assert.Equal(t, sn.ARN, gotSN.ARN)
	assert.Equal(t, "sn", gotSN.Name)

	gotSvc, err := dst.GetService(ctx, svc.ID)
	require.NoError(t, err)
	assert.Equal(t, "svc", gotSvc.Name)

	gotTG, err := dst.GetTargetGroup(ctx, tg.ID)
	require.NoError(t, err)
	assert.Equal(t, "tg", gotTG.Name)
	assert.Equal(t, "IP", gotTG.Type)

	// The target-group-keyed targets store survived the round-trip.
	gotTargets, err := dst.ListTargets(ctx, tg.ID)
	require.NoError(t, err)
	require.Len(t, gotTargets, 1)
	assert.Equal(t, "10.0.0.1", gotTargets[0].ID)

	// Tags (ARN-keyed tag store) survived the round-trip.
	tags, err := dst.ListTagsForResource(ctx, svc.ARN)
	require.NoError(t, err)
	assert.Equal(t, "prod", tags["env"])
}
