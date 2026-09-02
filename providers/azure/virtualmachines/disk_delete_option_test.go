package virtualmachines

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// attachDiskWithDeleteOption creates a managed disk, attaches it to instanceID
// at device, and records its deleteOption (deleteOnTermination) — the sequence
// the Azure VM wire handler runs when materializing an OS/data disk.
func attachDiskWithDeleteOption(
	ctx context.Context, t *testing.T, m *Mock, instanceID, device string, deleteOnTermination bool,
) string {
	t.Helper()

	vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 32, VolumeType: "Premium_LRS"})
	require.NoError(t, err)
	require.NoError(t, m.AttachVolume(ctx, vol.ID, instanceID, device))
	require.NoError(t, m.SetDiskDeleteOnTermination(ctx, vol.ID, deleteOnTermination))

	return vol.ID
}

// TestTerminateDeleteOptionCascade verifies the VM-delete disk cascade honors
// each attachment's deleteOption: a DeleteOnTermination=true disk is deleted
// with the VM while a =false disk is detached (returned to available with its
// attachment cleared).
func TestTerminateDeleteOptionCascade(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_D2s_v3"}, 1)
	require.NoError(t, err)
	vmID := insts[0].ID

	deleteVol := attachDiskWithDeleteOption(ctx, t, m, vmID, "osdisk", true)
	keepVol := attachDiskWithDeleteOption(ctx, t, m, vmID, "0", false)

	require.NoError(t, m.TerminateInstances(ctx, []string{vmID}))

	vols, err := m.DescribeVolumes(ctx, nil)
	require.NoError(t, err)

	byID := make(map[string]driver.VolumeInfo, len(vols))
	for _, v := range vols {
		byID[v.ID] = v
	}

	// The DeleteOnTermination=true disk is gone.
	_, stillThere := byID[deleteVol]
	assert.False(t, stillThere, "deleteOption=Delete disk should be deleted on VM terminate")

	// The DeleteOnTermination=false disk survives, detached.
	survivor, ok := byID[keepVol]
	require.True(t, ok, "deleteOption=Detach disk should survive VM terminate")
	assert.Equal(t, stateAvailable, survivor.State)
	assert.Empty(t, survivor.AttachedTo)
	assert.False(t, survivor.DeleteOnTermination, "detach clears the delete-on-termination flag")
}

// TestConcurrentTerminateAndDetachNoRace exercises the terminate-vs-detach cascade
// under -race: for many VMs each holding a DeleteOnTermination=true disk, one
// goroutine terminates the VM while another concurrently detaches its disk. Every
// mutation goes through the store lock, so there is no data race, and the outcome
// is always consistent — a disk that survives (detach won the race) is Unattached,
// never left half-mutated.
func TestConcurrentTerminateAndDetachNoRace(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	const n = 40

	type pair struct {
		vmID  string
		volID string
	}

	pairs := make([]pair, 0, n)

	for i := 0; i < n; i++ {
		insts, err := m.RunInstances(ctx, driver.InstanceConfig{
			ImageID:      "img-1",
			InstanceType: "Standard_D2s_v3",
			Tags:         map[string]string{"n": fmt.Sprintf("%d", i)},
		}, 1)
		require.NoError(t, err)

		volID := attachDiskWithDeleteOption(ctx, t, m, insts[0].ID, "osdisk", true)
		pairs = append(pairs, pair{vmID: insts[0].ID, volID: volID})
	}

	var wg sync.WaitGroup

	for _, p := range pairs {
		wg.Add(2)

		go func(vmID string) {
			defer wg.Done()

			_ = m.TerminateInstances(ctx, []string{vmID})
		}(p.vmID)

		go func(volID string) {
			defer wg.Done()

			_ = m.DetachVolume(ctx, volID, "", "")
		}(p.volID)
	}

	wg.Wait()

	// The store must be self-consistent: any disk that survived the race is
	// detached (available, no attachment), never corrupted.
	vols, err := m.DescribeVolumes(ctx, nil)
	require.NoError(t, err)

	for _, v := range vols {
		assert.Equal(t, stateAvailable, v.State, "surviving disk %s must be available", v.ID)
		assert.Empty(t, v.AttachedTo, "surviving disk %s must have no attachment", v.ID)
	}
}
