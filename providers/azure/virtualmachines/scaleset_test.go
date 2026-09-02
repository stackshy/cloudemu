package virtualmachines

import (
	"context"
	"strconv"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateScaleSetDefaults(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	ss, err := m.CreateScaleSet(ctx, ScaleSet{Name: "vmss-defaults"})
	require.NoError(t, err)

	assert.Equal(t, "vmss-defaults", ss.Name)
	assert.NotEmpty(t, ss.ID)
	assert.NotEmpty(t, ss.Location)
	assert.Equal(t, "Standard_D2s_v3", ss.SKUName)
	assert.Equal(t, "Standard", ss.SKUTier)
	assert.Equal(t, 1, ss.Capacity)
	assert.Equal(t, "Regular", ss.Priority)
	assert.Equal(t, "Linux", ss.OSType)
}

func TestCreateScaleSetExplicitZeroCapacityHonored(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	ss, err := m.CreateScaleSet(ctx, ScaleSet{Name: "vmss-zero", Capacity: 0, CapacityZero: true})
	require.NoError(t, err)
	assert.Equal(t, 0, ss.Capacity)

	list, err := m.ListScaleSets(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, 0, list[0].Capacity)
}

func TestCreateScaleSetRequiresName(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateScaleSet(ctx, ScaleSet{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestCreateScaleSetExplicitRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	in := ScaleSet{
		Name:        "vmss-spot",
		Location:    "westus2",
		SKUName:     "Standard_F4s_v2",
		SKUTier:     "Standard",
		Capacity:    7,
		Priority:    "Spot",
		LicenseType: "Windows_Server",
		OSType:      "Windows",
		Tags:        map[string]string{"env": "prod"},
	}

	created, err := m.CreateScaleSet(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, in.Priority, created.Priority)
	assert.Equal(t, in.LicenseType, created.LicenseType)

	list, err := m.ListScaleSets(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	got := list[0]
	assert.Equal(t, "vmss-spot", got.Name)
	assert.Equal(t, "westus2", got.Location)
	assert.Equal(t, "Standard_F4s_v2", got.SKUName)
	assert.Equal(t, "Standard", got.SKUTier)
	assert.Equal(t, 7, got.Capacity)
	assert.Equal(t, "Spot", got.Priority)
	assert.Equal(t, "Windows_Server", got.LicenseType)
	assert.Equal(t, "Windows", got.OSType)
	assert.Equal(t, "prod", got.Tags["env"])
}

func TestScaleSetMaterializesInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateScaleSet(ctx, ScaleSet{Name: "vmss-mat", Capacity: 3})
	require.NoError(t, err)

	vms, err := m.ListScaleSetVMs(ctx, "vmss-mat")
	require.NoError(t, err)
	require.Len(t, vms, 3)

	// Fresh create assigns ordinals 0..N-1, all running.
	for i, vm := range vms {
		assert.Equal(t, strconv.Itoa(i), vm.InstanceID)
		assert.Equal(t, scaleSetVMRunning, vm.PowerState)
		assert.Equal(t, "Succeeded", vm.ProvisioningState)
	}
}

func TestScaleSetPowerAndDeleteInstance(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateScaleSet(ctx, ScaleSet{Name: "vmss-ops", Capacity: 3})
	require.NoError(t, err)

	require.NoError(t, m.PowerScaleSetVM(ctx, "vmss-ops", "0", "poweroff"))
	require.NoError(t, m.PowerScaleSetVM(ctx, "vmss-ops", "1", "deallocate"))

	vm0, err := m.GetScaleSetVM(ctx, "vmss-ops", "0")
	require.NoError(t, err)
	assert.Equal(t, scaleSetVMStopped, vm0.PowerState)

	vm1, err := m.GetScaleSetVM(ctx, "vmss-ops", "1")
	require.NoError(t, err)
	assert.Equal(t, scaleSetVMDeallocated, vm1.PowerState)

	// Deleting an instance drops it and decrements the effective count.
	require.NoError(t, m.DeleteScaleSetVM(ctx, "vmss-ops", "1"))

	vms, err := m.ListScaleSetVMs(ctx, "vmss-ops")
	require.NoError(t, err)
	require.Len(t, vms, 2)

	_, err = m.GetScaleSetVM(ctx, "vmss-ops", "1")
	require.Error(t, err)

	// An unsupported power verb is rejected.
	require.Error(t, m.PowerScaleSetVM(ctx, "vmss-ops", "0", "bogus"))
}

func TestPowerScaleSetWholeSet(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateScaleSet(ctx, ScaleSet{Name: "vmss-ws", Capacity: 3})
	require.NoError(t, err)

	// Whole-set poweroff transitions every instance to stopped.
	require.NoError(t, m.PowerScaleSet(ctx, "vmss-ws", "poweroff", nil))

	vms, err := m.ListScaleSetVMs(ctx, "vmss-ws")
	require.NoError(t, err)
	require.Len(t, vms, 3)

	for _, vm := range vms {
		assert.Equal(t, scaleSetVMStopped, vm.PowerState)
	}

	// Whole-set start brings them all back to running.
	require.NoError(t, m.PowerScaleSet(ctx, "vmss-ws", "start", nil))

	vms, err = m.ListScaleSetVMs(ctx, "vmss-ws")
	require.NoError(t, err)

	for _, vm := range vms {
		assert.Equal(t, scaleSetVMRunning, vm.PowerState)
	}

	// A subset request touches only the named instance.
	require.NoError(t, m.PowerScaleSet(ctx, "vmss-ws", "deallocate", []string{"1"}))

	vm1, err := m.GetScaleSetVM(ctx, "vmss-ws", "1")
	require.NoError(t, err)
	assert.Equal(t, scaleSetVMDeallocated, vm1.PowerState)

	vm0, err := m.GetScaleSetVM(ctx, "vmss-ws", "0")
	require.NoError(t, err)
	assert.Equal(t, scaleSetVMRunning, vm0.PowerState)
}

func TestPowerScaleSetErrors(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateScaleSet(ctx, ScaleSet{Name: "vmss-err", Capacity: 2})
	require.NoError(t, err)

	// Missing scale set → NotFound.
	require.Error(t, m.PowerScaleSet(ctx, "no-such", "start", nil))

	// Unknown action → InvalidArgument.
	require.Error(t, m.PowerScaleSet(ctx, "vmss-err", "bogus", nil))

	// Subset naming a non-existent instance → NotFound.
	require.Error(t, m.PowerScaleSet(ctx, "vmss-err", "start", []string{"9"}))
}

func TestUpdateScaleSetMergesTagsAndCapacity(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateScaleSet(ctx, ScaleSet{
		Name:     "vmss-patch",
		Capacity: 2,
		Tags:     map[string]string{"env": "dev"},
	})
	require.NoError(t, err)

	cap4 := int64(4)

	updated, err := m.UpdateScaleSet(ctx, "vmss-patch", ScaleSetPatch{
		Tags:     map[string]string{"team": "platform"},
		Capacity: &cap4,
	})
	require.NoError(t, err)

	// Tags merge (pre-existing key survives), capacity rescales.
	assert.Equal(t, "dev", updated.Tags["env"])
	assert.Equal(t, "platform", updated.Tags["team"])
	assert.Equal(t, 4, updated.Capacity)

	vms, err := m.ListScaleSetVMs(ctx, "vmss-patch")
	require.NoError(t, err)
	require.Len(t, vms, 4)

	// Missing scale set → NotFound.
	_, err = m.UpdateScaleSet(ctx, "no-such", ScaleSetPatch{})
	require.Error(t, err)
}

func TestUpdateScaleSetExplicitZeroCapacity(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateScaleSet(ctx, ScaleSet{Name: "vmss-scalein", Capacity: 3})
	require.NoError(t, err)

	cap0 := int64(0)

	updated, err := m.UpdateScaleSet(ctx, "vmss-scalein", ScaleSetPatch{Capacity: &cap0})
	require.NoError(t, err)
	assert.Equal(t, 0, updated.Capacity)

	vms, err := m.ListScaleSetVMs(ctx, "vmss-scalein")
	require.NoError(t, err)
	assert.Empty(t, vms)
}

func TestScaleSetReconcilePreservesStateAcrossCapacityChange(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateScaleSet(ctx, ScaleSet{Name: "vmss-rc", Capacity: 2})
	require.NoError(t, err)
	require.NoError(t, m.PowerScaleSetVM(ctx, "vmss-rc", "0", "poweroff"))

	// Scale out to 4: existing instances keep their power state; new instances
	// get the next monotonic ordinals.
	_, err = m.CreateScaleSet(ctx, ScaleSet{Name: "vmss-rc", Capacity: 4})
	require.NoError(t, err)

	vm0, err := m.GetScaleSetVM(ctx, "vmss-rc", "0")
	require.NoError(t, err)
	assert.Equal(t, scaleSetVMStopped, vm0.PowerState, "existing instance power state must survive scale-out")

	vms, err := m.ListScaleSetVMs(ctx, "vmss-rc")
	require.NoError(t, err)
	require.Len(t, vms, 4)
	assert.Equal(t, "3", vms[3].InstanceID)

	// Scale in to 1: the highest-ordinal instances are dropped.
	_, err = m.CreateScaleSet(ctx, ScaleSet{Name: "vmss-rc", Capacity: 1})
	require.NoError(t, err)

	vms, err = m.ListScaleSetVMs(ctx, "vmss-rc")
	require.NoError(t, err)
	require.Len(t, vms, 1)
	assert.Equal(t, "0", vms[0].InstanceID)
}

func TestRunInstancesCarriesCostFields(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	cfg := driver.InstanceConfig{
		ImageID:      "img-1",
		InstanceType: "Standard_B1s",
		OSType:       "Windows",
		Priority:     "Spot",
		LicenseType:  "Windows_Server",
		Zones:        []string{"1", "2"},
	}

	instances, err := m.RunInstances(ctx, cfg, 1)
	require.NoError(t, err)
	require.Len(t, instances, 1)

	inst := instances[0]
	assert.Equal(t, "Windows", inst.OSType)
	assert.Equal(t, "Spot", inst.Priority)
	assert.Equal(t, "Windows_Server", inst.LicenseType)
	assert.Equal(t, []string{"1", "2"}, inst.Zones)
}

func TestCreateVolumeCarriesPerformanceFields(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	cfg := driver.VolumeConfig{
		Size:       256,
		VolumeType: "Premium_LRS",
		IOPS:       5000,
		Throughput: 200,
		Tier:       "P15",
	}

	vol, err := m.CreateVolume(ctx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 5000, vol.IOPS)
	assert.Equal(t, 200, vol.Throughput)
	assert.Equal(t, "P15", vol.Tier)
}
