package virtualmachines

import (
	"context"
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
