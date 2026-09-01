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
