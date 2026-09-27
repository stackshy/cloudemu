package virtualmachines

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// TestConcurrentAttachAndUpdateVolumeKeepsAttachment runs AttachVolume,
// UpdateVolume (a disk re-PUT) and DescribeVolumes on the same disks in
// parallel. The update must merge under the store lock with copy-on-write:
// mutating the stored *VolumeInfo in place is a data race with the reader, and
// writing back a pointer read before a concurrent attach silently drops the
// attachment (the disk reads "available" while the VM still owns it).
func TestConcurrentAttachAndUpdateVolumeKeepsAttachment(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_D2s_v3"}, 1)
	require.NoError(t, err)

	vmID := insts[0].ID

	const rounds = 300

	ids := make([]string, rounds)

	for i := range ids {
		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 32, VolumeType: "Premium_LRS"})
		require.NoError(t, err)

		ids[i] = vol.ID
	}

	var wg sync.WaitGroup

	for _, id := range ids {
		wg.Add(3)

		go func() {
			defer wg.Done()

			assert.NoError(t, m.AttachVolume(ctx, id, vmID, "0"))
		}()

		go func() {
			defer wg.Done()

			_, err := m.UpdateVolume(ctx, id, driver.VolumeConfig{
				Size: 64, VolumeType: "Premium_LRS", Tags: map[string]string{"env": "prod"},
			})
			assert.NoError(t, err)
		}()

		go func() {
			defer wg.Done()

			_, _ = m.DescribeVolumes(ctx, nil)
		}()
	}

	wg.Wait()

	vols, err := m.DescribeVolumes(ctx, ids)
	require.NoError(t, err)
	require.Len(t, vols, rounds)

	for i := range vols {
		assert.Equal(t, stateInUse, vols[i].State, "disk %s lost its attachment", vols[i].ID)
		assert.Equal(t, vmID, vols[i].AttachedTo, "disk %s lost its attachment", vols[i].ID)
		assert.Equal(t, 64, vols[i].Size, "disk %s lost its resize", vols[i].ID)
	}
}

// TestConcurrentAttachAndPatchVolumeKeepsAttachment is the PATCH variant of
// the race above: PatchVolume must not drop a concurrent attachment either.
func TestConcurrentAttachAndPatchVolumeKeepsAttachment(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_D2s_v3"}, 1)
	require.NoError(t, err)

	const rounds = 200

	ids := make([]string, rounds)

	for i := range ids {
		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 32, VolumeType: "Premium_LRS"})
		require.NoError(t, err)

		ids[i] = vol.ID
	}

	size := 64

	var wg sync.WaitGroup

	for _, id := range ids {
		wg.Add(3)

		go func() {
			defer wg.Done()

			assert.NoError(t, m.AttachVolume(ctx, id, insts[0].ID, "0"))
		}()

		go func() {
			defer wg.Done()

			_, err := m.PatchVolume(ctx, id, driver.AzureDiskPatch{Size: &size})
			assert.NoError(t, err)
		}()

		go func() {
			defer wg.Done()

			_, _ = m.DescribeVolumes(ctx, nil)
		}()
	}

	wg.Wait()

	vols, err := m.DescribeVolumes(ctx, ids)
	require.NoError(t, err)

	for i := range vols {
		assert.Equal(t, insts[0].ID, vols[i].AttachedTo, "disk %s lost its attachment", vols[i].ID)
		assert.Equal(t, 64, vols[i].Size, "disk %s lost its resize", vols[i].ID)
	}
}

func ptrTo[T any](v T) *T { return &v }

// TestPatchVolumeRules exercises the disk update rules through the Go library,
// the same code the ARM wire handler and a re-PUT go through.
func TestPatchVolumeRules(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	newVol := func(size int, sku string) string {
		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{
			Size: size, VolumeType: sku, Tags: map[string]string{"cloudemu:azureDiskName": "d", "env": "dev"},
		})
		require.NoError(t, err)

		return vol.ID
	}

	premium := newVol(64, skuPremiumLRS)
	standard := newVol(64, skuStandardLRS)

	for _, tc := range []struct {
		name  string
		id    string
		patch driver.AzureDiskPatch
	}{
		{"shrink", premium, driver.AzureDiskPatch{Size: ptrTo(32)}},
		{"unknown sku", premium, driver.AzureDiskPatch{VolumeType: ptrTo("Gold_LRS")}},
		{"above the 32767 GiB maximum", standard, driver.AzureDiskPatch{Size: ptrTo(40000)}},
		{"iops on Premium_LRS", premium, driver.AzureDiskPatch{IOPS: ptrTo(5000)}},
		{"tier on Standard_LRS", standard, driver.AzureDiskPatch{Tier: ptrTo("P10")}},
		{"unknown tier", premium, driver.AzureDiskPatch{Tier: ptrTo("P99")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.PatchVolume(ctx, tc.id, tc.patch)
			assert.True(t, cerrors.IsInvalidArgument(err), "err = %v, want InvalidArgument", err)
		})
	}

	// A re-PUT (UpdateVolume) is held to the grow-only rule too.
	_, err := m.UpdateVolume(ctx, premium, driver.VolumeConfig{Size: 16, VolumeType: skuPremiumLRS})
	assert.True(t, cerrors.IsInvalidArgument(err), "re-PUT shrink: err = %v, want InvalidArgument", err)

	// Growing a Premium SSD past its tier raises the tier to the new baseline,
	// and a tags replacement keeps the named bookkeeping keys.
	got, err := m.PatchVolume(ctx, premium, driver.AzureDiskPatch{
		Size: ptrTo(600), Tags: map[string]string{"team": "x"}, KeepTags: []string{"cloudemu:azureDiskName"},
	})
	require.NoError(t, err)
	assert.Equal(t, "P30", got.Tier)
	assert.Equal(t, map[string]string{"team": "x", "cloudemu:azureDiskName": "d"}, got.Tags)

	// A Standard disk may grow past 4 TiB while detached, and a PremiumV2 disk
	// up to 64 TiB.
	_, err = m.PatchVolume(ctx, standard, driver.AzureDiskPatch{Size: ptrTo(8192)})
	require.NoError(t, err)

	v2 := newVol(64, skuPremiumV2LRS)
	_, err = m.PatchVolume(ctx, v2, driver.AzureDiskPatch{Size: ptrTo(40000), IOPS: ptrTo(8000)})
	require.NoError(t, err)

	_, err = m.PatchVolume(ctx, "no-such-disk", driver.AzureDiskPatch{Size: ptrTo(1)})
	assert.True(t, cerrors.IsNotFound(err), "err = %v, want NotFound", err)
}

// TestPatchVolumeOSDiskRules: an OS disk cannot become a Premium SSD v2, and
// is only resized while its VM is deallocated.
func TestPatchVolumeOSDiskRules(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_D2s_v3"}, 1)
	require.NoError(t, err)

	vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 64, VolumeType: skuPremiumLRS})
	require.NoError(t, err)
	require.NoError(t, m.AttachVolume(ctx, vol.ID, insts[0].ID, osDiskDevice))

	_, err = m.PatchVolume(ctx, vol.ID, driver.AzureDiskPatch{VolumeType: ptrTo(skuPremiumV2LRS)})
	assert.True(t, cerrors.IsInvalidArgument(err), "OS disk to PremiumV2: err = %v, want InvalidArgument", err)

	_, err = m.PatchVolume(ctx, vol.ID, driver.AzureDiskPatch{Size: ptrTo(128)})
	assertDiskErr(t, err, codeOperationNotAllowed)

	// Powered off from the guest is still allocated.
	require.NoError(t, m.PowerOff(ctx, insts[0].ID))

	_, err = m.PatchVolume(ctx, vol.ID, driver.AzureDiskPatch{Size: ptrTo(128)})
	assertDiskErr(t, err, codeOperationNotAllowed)

	require.NoError(t, m.Deallocate(ctx, insts[0].ID))

	got, err := m.PatchVolume(ctx, vol.ID, driver.AzureDiskPatch{Size: ptrTo(128)})
	require.NoError(t, err)
	assert.Equal(t, 128, got.Size)
}

// TestPatchVolumeSASExpires: a granted SAS blocks a resize only until it is
// revoked or reaches its expiry.
func TestPatchVolumeSASExpires(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(clk)))

	vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 64, VolumeType: skuStandardLRS})
	require.NoError(t, err)

	_, err = m.GrantDiskAccess(ctx, vol.ID, "Read", 60)
	require.NoError(t, err)

	_, err = m.PatchVolume(ctx, vol.ID, driver.AzureDiskPatch{Size: ptrTo(128)})
	assertDiskErr(t, err, codeResizeWhileActiveSAS)
	assert.True(t, cerrors.IsFailedPrecondition(err), "an active-SAS refusal is FailedPrecondition (409)")

	clk.Advance(61 * time.Second)

	_, err = m.PatchVolume(ctx, vol.ID, driver.AzureDiskPatch{Size: ptrTo(128)})
	require.NoError(t, err)
}

func assertDiskErr(t *testing.T, err error, code string) {
	t.Helper()

	var de *driver.AzureDiskError
	if assert.True(t, errors.As(err, &de), "err = %v, want *driver.AzureDiskError", err) {
		assert.Equal(t, code, de.Code)
	}
}
