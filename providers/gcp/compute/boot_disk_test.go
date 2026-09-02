package compute

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// TestRemoveInstanceAutoDeleteCascade proves RemoveInstance (GCP instances.delete)
// deletes a disk attached with DeleteOnTermination=true (autoDelete) and detaches
// — but keeps — one attached with DeleteOnTermination=false.
func TestRemoveInstanceAutoDeleteCascade(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"}, 1)
	require.NoError(t, err)
	instID := insts[0].ID

	boot, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10, AvailabilityZone: "us-central1-a"})
	require.NoError(t, err)

	data, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 20, AvailabilityZone: "us-central1-a"})
	require.NoError(t, err)

	require.NoError(t, m.AttachDiskGCP(instID, boot.ID, "boot", true))
	require.NoError(t, m.AttachDiskGCP(instID, data.ID, "data", false))

	require.NoError(t, m.RemoveInstance(ctx, instID))

	// autoDelete=true disk is gone.
	gone, err := m.DescribeVolumes(ctx, []string{boot.ID})
	require.NoError(t, err)
	assert.Empty(t, gone, "autoDelete=true disk should be deleted with the instance")

	// autoDelete=false disk survives, detached and available.
	kept, err := m.DescribeVolumes(ctx, []string{data.ID})
	require.NoError(t, err)
	require.Len(t, kept, 1)
	assert.Equal(t, stateAvailable, kept[0].State)
	assert.Empty(t, kept[0].AttachedTo)
	assert.False(t, kept[0].DeleteOnTermination)
}

// TestAttachDiskGCPSetsDeleteOnTermination proves the attach flips the driver
// volume to in-use and records the autoDelete flag, and that a plain AttachVolume
// leaves it false.
func TestAttachDiskGCPSetsDeleteOnTermination(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"}, 1)
	require.NoError(t, err)
	instID := insts[0].ID

	vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10, AvailabilityZone: "us-central1-a"})
	require.NoError(t, err)

	require.NoError(t, m.AttachDiskGCP(instID, vol.ID, "data", true))

	got, err := m.DescribeVolumes(ctx, []string{vol.ID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, stateInUse, got[0].State)
	assert.Equal(t, instID, got[0].AttachedTo)
	assert.True(t, got[0].DeleteOnTermination)

	// Detach clears the attachment and the auto-delete flag.
	require.NoError(t, m.DetachVolume(ctx, vol.ID, "", ""))

	got, err = m.DescribeVolumes(ctx, []string{vol.ID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, stateAvailable, got[0].State)
	assert.False(t, got[0].DeleteOnTermination)
}
