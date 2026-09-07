package compute_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	driver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// TestSnapshotRestoreRoundTrip fills every one of the mock's sixteen stores,
// snapshots, restores into a fresh mock and asserts each resource comes back
// under its original OCID with its cross-references intact. A store left out of
// snapshot.go silently drops its resources, so each one is asserted.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	src := newFixture(t)
	ctx := src.ctx

	// instances, details, bootVolumes, bootAttach, vnicAttach, scopes, created.
	inst := src.launch(t)

	details, ok := src.compute.InstanceDetails(inst.ID)
	require.True(t, ok)
	require.NotEmpty(t, details.BootVolumeID)
	require.NotEmpty(t, details.VNICID)

	// volumes, volAttach.
	vol, err := src.compute.CreateVolume(ctx, driver.VolumeConfig{Size: 100, VolumeType: "balanced"})
	require.NoError(t, err)
	require.NoError(t, src.compute.AttachVolume(ctx, vol.ID, inst.ID, "/dev/oracleoci/oraclevdb"))

	// backups.
	backup, err := src.compute.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: vol.ID, Description: "nightly"})
	require.NoError(t, err)

	// volGroups.
	group, err := src.compute.CreateVolumeGroup(ctx, ocicompute.VolumeGroup{
		DisplayName:        "grp",
		AvailabilityDomain: details.AvailabilityDomain,
		VolumeIDs:          []string{vol.ID},
	})
	require.NoError(t, err)

	// images.
	image, err := src.compute.CreateImage(ctx, driver.ImageConfig{Name: "golden", InstanceID: inst.ID})
	require.NoError(t, err)

	// configs.
	tmpl, err := src.compute.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name:           "web-config",
		InstanceConfig: driver.InstanceConfig{ImageID: src.image, InstanceType: shape, SubnetID: src.subnet},
	})
	require.NoError(t, err)

	// pools, and the scaling policies held in the pool's nested store.
	pool, err := src.compute.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "web-pool", MinSize: 1, MaxSize: 3, DesiredCapacity: 1,
		LaunchTemplateName: tmpl.Name,
		InstanceConfig:     driver.InstanceConfig{ImageID: src.image, InstanceType: shape, SubnetID: src.subnet},
	})
	require.NoError(t, err)
	require.NoError(t, src.compute.PutScalingPolicy(ctx, driver.ScalingPolicy{
		Name: "hold", AutoScalingGroup: pool.Name,
		AdjustmentType: "ExactCapacity", ScalingAdjustment: 1,
	}))

	// spot.
	spot, err := src.compute.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		Count:          1,
		InstanceConfig: driver.InstanceConfig{ImageID: src.image, InstanceType: shape, SubnetID: src.subnet},
	})
	require.NoError(t, err)
	require.Len(t, spot, 1)

	// A non-default compartment, so the scopes store is exercised beyond the
	// create-time default.
	src.compute.SetScope(vol.ID, scope.Scope{Compartment: otherCompartment})

	data, err := src.compute.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newFixture(t)
	require.NoError(t, dst.compute.Restore(ctx, data))

	// instances and details, with the boot volume and VNIC cross-references.
	got, err := dst.compute.DescribeInstances(ctx, []string{inst.ID}, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, inst.PrivateIP, got[0].PrivateIP)
	assert.Equal(t, inst.SubnetID, got[0].SubnetID)

	gotDetails, ok := dst.compute.InstanceDetails(inst.ID)
	require.True(t, ok)
	assert.Equal(t, details.BootVolumeID, gotDetails.BootVolumeID)
	assert.Equal(t, details.VNICID, gotDetails.VNICID)

	// bootVolumes and bootAttach.
	bootVols, err := dst.compute.ListBootVolumes(ctx, dst.compartment)
	require.NoError(t, err)
	assert.Contains(t, idsOfBootVolumes(bootVols), details.BootVolumeID)

	bootAttachments, err := dst.compute.ListBootVolumeAttachments(ctx, dst.compartment, inst.ID, "")
	require.NoError(t, err)
	assert.Len(t, bootAttachments, 1)

	// vnicAttach.
	vnicAttachments, err := dst.compute.ListVNICAttachments(ctx, dst.compartment, inst.ID, "")
	require.NoError(t, err)
	assert.Len(t, vnicAttachments, 1)

	// volumes and volAttach, the volume still attached to its instance.
	vols, err := dst.compute.DescribeVolumes(ctx, []string{vol.ID})
	require.NoError(t, err)
	require.Len(t, vols, 1)
	assert.Equal(t, inst.ID, vols[0].AttachedTo)

	volAttachments, err := dst.compute.ListVolumeAttachments(ctx, dst.compartment, "", vol.ID)
	require.NoError(t, err)
	require.Len(t, volAttachments, 1)
	assert.Equal(t, inst.ID, volAttachments[0].InstanceID)

	// backups.
	backups, err := dst.compute.DescribeSnapshots(ctx, []string{backup.ID})
	require.NoError(t, err)
	require.Len(t, backups, 1)
	assert.Equal(t, vol.ID, backups[0].VolumeID)

	// volGroups.
	groups, err := dst.compute.ListVolumeGroups(ctx, dst.compartment)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, group.ID, groups[0].ID)
	assert.Equal(t, []string{vol.ID}, groups[0].VolumeIDs)

	// images: the created one alongside the fresh mock's seeded catalog.
	images, err := dst.compute.DescribeImages(ctx, []string{image.ID})
	require.NoError(t, err)
	require.Len(t, images, 1)
	assert.Equal(t, "golden", images[0].Name)

	// configs.
	configs, err := dst.compute.ListInstanceConfigurations(ctx, dst.compartment)
	require.NoError(t, err)
	require.Len(t, configs, 1)
	assert.Equal(t, tmpl.Name, configs[0].DisplayName)

	// pools, and the nested policy store rebuilt with its policy.
	pools, err := dst.compute.ListInstancePools(ctx, dst.compartment)
	require.NoError(t, err)
	require.Len(t, pools, 1)
	assert.Equal(t, pool.Name, pools[0].DisplayName)

	// The policy is addressable on the restored pool: a nil policy store would
	// panic here and a dropped policy would report NotFound. It holds the pool
	// at its current size, so nothing is launched into the other mock's VCN.
	require.NoError(t, dst.compute.ExecuteScalingPolicy(ctx, pool.Name, "hold"))
	require.NoError(t, dst.compute.DeleteScalingPolicy(ctx, pool.Name, "hold"))

	// spot.
	spotReqs, err := dst.compute.DescribeSpotRequests(ctx, []string{spot[0].ID})
	require.NoError(t, err)
	require.Len(t, spotReqs, 1)

	// shapes, seeded in both mocks and dumped either way.
	shapes, err := dst.compute.ListShapes(ctx, "")
	require.NoError(t, err)
	assert.NotEmpty(t, shapes)

	// scopes: the volume kept the compartment it was moved to.
	assert.Equal(t, otherCompartment, dst.compute.Scope(vol.ID).Compartment)

	// created: the instance kept its creation time.
	assert.Equal(t, inst.LaunchTime, got[0].LaunchTime)
}

// TestSnapshotCarriesEngineBackedInstances pins the engine flag through a
// snapshot: it is an unexported-by-default detail, and losing it would strand a
// real backing that terminate would then never tear down.
func TestSnapshotCarriesEngineBackedInstances(t *testing.T) {
	eng := &fakeComputeEngine{ip: "172.30.1.9", console: []byte("boot log")}
	src := newEngineFixture(t, eng)

	out, err := src.compute.RunInstances(src.ctx, driver.InstanceConfig{
		ImageID: src.image, InstanceType: shape, SubnetID: src.subnet,
	}, 1)
	require.NoError(t, err)

	data, err := src.compute.Snapshot(src.ctx, false)
	require.NoError(t, err)

	dst := newEngineFixture(t, eng)
	require.NoError(t, dst.compute.Restore(dst.ctx, data))

	// The restored instance is still engine-backed: console output resolves and
	// terminating it deprovisions the real backing.
	console, err := dst.compute.GetConsoleOutput(dst.ctx, out[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "boot log", string(console))

	require.NoError(t, dst.compute.TerminateInstance(dst.ctx, out[0].ID, false))
	assert.Equal(t, []string{out[0].ID}, eng.deprovisioned)
}

func idsOfBootVolumes(vols []ocicompute.BootVolume) []string {
	out := make([]string, 0, len(vols))
	for i := range vols {
		out = append(out, vols[i].ID)
	}

	return out
}
