package compute_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	ocivcn "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	driver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	shape            = "VM.Standard.E4.Flex"
	otherCompartment = "ocid1.compartment.oc1..other"
)

type fixture struct {
	compute     *ocicompute.Mock
	vcn         *ocivcn.Mock
	subnet      string
	vcnID       string
	image       string
	compartment string
	ctx         context.Context //nolint:containedctx // the test's context, carried for brevity
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-ashburn-1"))

	vcnMock := ocivcn.New(opts)
	computeMock := ocicompute.New(opts)
	computeMock.SetNetworking(vcnMock)

	ctx := t.Context()

	vcn, err := vcnMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := vcnMock.CreateSubnet(ctx, netdriver.SubnetConfig{VPCID: vcn.ID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	images, err := computeMock.ListImages(ctx, opts.CompartmentID, "", "")
	require.NoError(t, err)
	require.NotEmpty(t, images)

	return &fixture{
		compute:     computeMock,
		vcn:         vcnMock,
		subnet:      subnet.ID,
		vcnID:       vcn.ID,
		image:       images[0].ID,
		compartment: opts.CompartmentID,
		ctx:         ctx,
	}
}

func (f *fixture) launch(t *testing.T) driver.Instance {
	t.Helper()

	out, err := f.compute.RunInstances(f.ctx, driver.InstanceConfig{
		ImageID:      f.image,
		InstanceType: shape,
		SubnetID:     f.subnet,
		Tags:         map[string]string{"Name": "web-1"},
	}, 1)
	require.NoError(t, err)
	require.Len(t, out, 1)

	return out[0]
}

func TestLaunchPlacesTheInstanceInItsSubnet(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	assert.True(t, strings.HasPrefix(inst.ID, "ocid1.instance.oc1.iad."), "got %q", inst.ID)
	assert.Equal(t, "running", inst.State)
	assert.Equal(t, f.subnet, inst.SubnetID)
	assert.Equal(t, f.vcnID, inst.VPCID, "the VCN comes from the subnet")
	assert.Equal(t, "10.0.1.2", inst.PrivateIP, "the address comes from the VNIC the VCN service created")
	assert.Contains(t, inst.SecurityGroups, f.vcn.Defaults(f.vcnID).SecurityListID)

	details, ok := f.compute.InstanceDetails(inst.ID)
	require.True(t, ok)
	assert.NotEmpty(t, details.VNICID, "a VNIC was created for the instance")
	assert.NotEmpty(t, details.BootVolumeID, "a boot volume was created with the instance")

	vnics, err := f.vcn.DescribeVNICs(f.ctx, []string{details.VNICID})
	require.NoError(t, err)
	require.Len(t, vnics, 1)
	assert.Equal(t, f.subnet, vnics[0].SubnetID)
}

func TestLaunchRejectsUnsupportedInput(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name  string
		cfg   driver.InstanceConfig
		count int
		code  cerrors.Code
	}{
		{
			name:  "zero count",
			cfg:   driver.InstanceConfig{InstanceType: shape},
			count: 0,
			code:  cerrors.InvalidArgument,
		},
		{
			name:  "key pair",
			cfg:   driver.InstanceConfig{InstanceType: shape, KeyName: "my-key"},
			count: 1,
			code:  cerrors.InvalidArgument,
		},
		{
			name:  "unknown priority",
			cfg:   driver.InstanceConfig{InstanceType: shape, Priority: "Reserved"},
			count: 1,
			code:  cerrors.InvalidArgument,
		},
		{
			name:  "unknown subnet",
			cfg:   driver.InstanceConfig{InstanceType: shape, SubnetID: "ocid1.subnet.oc1.iad.missing"},
			count: 1,
			code:  cerrors.NotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.compute.RunInstances(f.ctx, tc.cfg, tc.count)
			assert.Equal(t, tc.code, cerrors.GetCode(err))
		})
	}
}

func TestInstanceLifecycle(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	require.NoError(t, f.compute.StopInstances(f.ctx, []string{inst.ID}))
	assert.Equal(t, "stopped", f.state(t, inst.ID))

	// A second stop is the documented no-op, not an error.
	require.NoError(t, f.compute.StopInstances(f.ctx, []string{inst.ID}))

	require.NoError(t, f.compute.StartInstances(f.ctx, []string{inst.ID}))
	assert.Equal(t, "running", f.state(t, inst.ID))

	require.NoError(t, f.compute.RebootInstances(f.ctx, []string{inst.ID}))
	assert.Equal(t, "running", f.state(t, inst.ID))

	require.NoError(t, f.compute.TerminateInstances(f.ctx, []string{inst.ID}))

	got, err := f.compute.DescribeInstances(f.ctx, []string{inst.ID}, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func (f *fixture) state(t *testing.T, id string) string {
	t.Helper()

	got, err := f.compute.DescribeInstances(f.ctx, []string{id}, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)

	return got[0].State
}

func TestInstanceErrors(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	tests := []struct {
		name string
		call func() error
		code cerrors.Code
	}{
		{
			name: "start unknown",
			call: func() error { return f.compute.StartInstances(f.ctx, []string{"ocid1.instance.oc1.iad.missing"}) },
			code: cerrors.NotFound,
		},
		{
			name: "terminate unknown",
			call: func() error { return f.compute.TerminateInstances(f.ctx, []string{"ocid1.instance.oc1.iad.x"}) },
			code: cerrors.NotFound,
		},
		{
			name: "start a running instance",
			call: func() error { return f.compute.RebootInstances(f.ctx, []string{"ocid1.instance.oc1.iad.x"}) },
			code: cerrors.NotFound,
		},
		{
			name: "reshape a running instance",
			call: func() error {
				return f.compute.ModifyInstance(f.ctx, inst.ID, driver.ModifyInstanceInput{
					InstanceType: "VM.Standard2.1",
				})
			},
			code: cerrors.FailedPrecondition,
		},
		{
			name: "unsupported filter",
			call: func() error {
				_, err := f.compute.DescribeInstances(f.ctx, nil, []driver.DescribeFilter{{Name: "vcn-id"}})

				return err
			},
			code: cerrors.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.code, cerrors.GetCode(tc.call()))
		})
	}
}

func TestDescribeInstancesFilters(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	tests := []struct {
		name      string
		filter    driver.DescribeFilter
		expectLen int
	}{
		{name: "shape", filter: driver.DescribeFilter{Name: "shape", Values: []string{shape}}, expectLen: 1},
		{name: "other shape", filter: driver.DescribeFilter{Name: "shape", Values: []string{"BM.Standard.E4.128"}}},
		{name: "state", filter: driver.DescribeFilter{Name: "lifecycle-state", Values: []string{"running"}}, expectLen: 1},
		{name: "subnet", filter: driver.DescribeFilter{Name: "subnet-id", Values: []string{f.subnet}}, expectLen: 1},
		{name: "tag", filter: driver.DescribeFilter{Name: "tag:Name", Values: []string{"web-1"}}, expectLen: 1},
		{name: "other tag", filter: driver.DescribeFilter{Name: "tag:Name", Values: []string{"db"}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.compute.DescribeInstances(f.ctx, nil, []driver.DescribeFilter{tc.filter})
			require.NoError(t, err)
			assert.Len(t, got, tc.expectLen)
		})
	}

	assert.NotEmpty(t, inst.ID)
}

func TestCompartmentFiltering(t *testing.T) {
	f := newFixture(t)
	mine := f.launch(t)
	theirs := f.launch(t)

	f.compute.SetScope(theirs.ID, scope.Scope{Compartment: otherCompartment})

	assert.Equal(t, []string{mine.ID}, f.compute.InstancesInCompartment(f.compartment))
	assert.Equal(t, []string{theirs.ID}, f.compute.InstancesInCompartment(otherCompartment))
}

func TestOCIDShapes(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	details, ok := f.compute.InstanceDetails(inst.ID)
	require.True(t, ok)

	vol, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{Size: 50})
	require.NoError(t, err)

	att, err := f.compute.AttachVolumeToInstance(f.ctx, ocicompute.VolumeAttachment{
		InstanceID: inst.ID, VolumeID: vol.ID,
	})
	require.NoError(t, err)

	backup, err := f.compute.CreateSnapshot(f.ctx, driver.SnapshotConfig{VolumeID: vol.ID})
	require.NoError(t, err)

	group, err := f.compute.CreateVolumeGroup(f.ctx, ocicompute.VolumeGroup{VolumeIDs: []string{vol.ID}})
	require.NoError(t, err)

	cfg, err := f.compute.CreateInstanceConfiguration(f.ctx, "tpl", ocicompute.LaunchSpec{
		Shape: shape, ImageID: f.image, SubnetID: f.subnet,
	}, nil)
	require.NoError(t, err)

	pool, err := f.compute.CreateInstancePool(f.ctx, "pool", cfg.ID, 1, nil, nil)
	require.NoError(t, err)

	tests := []struct {
		name   string
		id     string
		prefix string
	}{
		{name: "instance", id: inst.ID, prefix: "ocid1.instance.oc1.iad."},
		{name: "image", id: f.image, prefix: "ocid1.image.oc1.iad."},
		{name: "boot volume", id: details.BootVolumeID, prefix: "ocid1.bootvolume.oc1.iad."},
		{name: "volume", id: vol.ID, prefix: "ocid1.volume.oc1.iad."},
		{name: "volume attachment", id: att.ID, prefix: "ocid1.volumeattachment.oc1.iad."},
		{name: "volume backup", id: backup.ID, prefix: "ocid1.volumebackup.oc1.iad."},
		{name: "volume group", id: group.ID, prefix: "ocid1.volumegroup.oc1.iad."},
		{name: "instance configuration", id: cfg.ID, prefix: "ocid1.instanceconfiguration.oc1.iad."},
		{name: "instance pool", id: pool.ID, prefix: "ocid1.instancepool.oc1.iad."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, strings.HasPrefix(tc.id, tc.prefix), "got %q, want prefix %q", tc.id, tc.prefix)
		})
	}
}

func TestVolumeAttachAndDetach(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	vol, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{Size: 100, AvailabilityZone: "AD-1"})
	require.NoError(t, err)
	assert.Equal(t, "available", vol.State)

	require.NoError(t, f.compute.AttachVolume(f.ctx, vol.ID, inst.ID, "/dev/oracleoci/oraclevdb"))

	got, err := f.compute.DescribeVolumes(f.ctx, []string{vol.ID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "in-use", got[0].State)
	assert.Equal(t, inst.ID, got[0].AttachedTo)

	atts, err := f.compute.ListVolumeAttachments(f.ctx, f.compartment, inst.ID, "")
	require.NoError(t, err)
	require.Len(t, atts, 1)
	assert.Equal(t, "paravirtualized", atts[0].AttachmentType)

	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(f.compute.DeleteVolume(f.ctx, vol.ID)))

	require.NoError(t, f.compute.DetachVolumeAttachment(f.ctx, atts[0].ID))
	require.NoError(t, f.compute.DeleteVolume(f.ctx, vol.ID))
}

func TestVolumeErrors(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	vol, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{Size: 50})
	require.NoError(t, err)

	require.NoError(t, f.compute.AttachVolume(f.ctx, vol.ID, inst.ID, ""))

	tests := []struct {
		name string
		call func() error
		code cerrors.Code
	}{
		{
			name: "zero size",
			call: func() error {
				_, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{})

				return err
			},
			code: cerrors.InvalidArgument,
		},
		{
			name: "delete unknown",
			call: func() error { return f.compute.DeleteVolume(f.ctx, "ocid1.volume.oc1.iad.missing") },
			code: cerrors.NotFound,
		},
		{
			name: "attach twice",
			call: func() error { return f.compute.AttachVolume(f.ctx, vol.ID, inst.ID, "") },
			code: cerrors.AlreadyExists,
		},
		{
			name: "shrink",
			call: func() error {
				_, err := f.compute.UpdateVolume(f.ctx, vol.ID, ocicompute.Update{}, 10, 0)

				return err
			},
			code: cerrors.InvalidArgument,
		},
		{
			name: "unknown source",
			call: func() error {
				_, err := f.compute.CreateVolumeFrom(f.ctx, driver.VolumeConfig{Size: 50},
					ocicompute.SourceDetails{SourceType: "volume", ID: "ocid1.volume.oc1.iad.missing"})

				return err
			},
			code: cerrors.NotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.code, cerrors.GetCode(tc.call()))
		})
	}
}

func TestBootVolumeSurvivesAPreservingTerminate(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	details, ok := f.compute.InstanceDetails(inst.ID)
	require.True(t, ok)

	require.NoError(t, f.compute.TerminateInstance(f.ctx, inst.ID, true))

	bv, err := f.compute.GetBootVolume(f.ctx, details.BootVolumeID)
	require.NoError(t, err)
	assert.Equal(t, "AVAILABLE", bv.LifecycleState)

	// Its attachment went with the instance, so the volume can now be deleted.
	require.NoError(t, f.compute.DeleteBootVolume(f.ctx, details.BootVolumeID))
}

func TestBootVolumeGoesWithTheInstanceByDefault(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	details, _ := f.compute.InstanceDetails(inst.ID)

	require.NoError(t, f.compute.TerminateInstance(f.ctx, inst.ID, false))

	_, err := f.compute.GetBootVolume(f.ctx, details.BootVolumeID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestVolumeGroupTracksItsMembers(t *testing.T) {
	f := newFixture(t)

	first, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{Size: 50})
	require.NoError(t, err)

	second, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{Size: 100})
	require.NoError(t, err)

	group, err := f.compute.CreateVolumeGroup(f.ctx, ocicompute.VolumeGroup{
		DisplayName: "consistency",
		VolumeIDs:   []string{first.ID, second.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, 150, group.SizeInGBs)

	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(f.compute.DeleteVolume(f.ctx, first.ID)))

	listed, err := f.compute.ListVolumeGroups(f.ctx, f.compartment)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	require.NoError(t, f.compute.DeleteVolumeGroup(f.ctx, group.ID))
	require.NoError(t, f.compute.DeleteVolume(f.ctx, first.ID))
}

func TestVolumeGroupErrors(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name string
		spec ocicompute.VolumeGroup
		code cerrors.Code
	}{
		{name: "empty", spec: ocicompute.VolumeGroup{}, code: cerrors.InvalidArgument},
		{
			name: "unknown member",
			spec: ocicompute.VolumeGroup{VolumeIDs: []string{"ocid1.volume.oc1.iad.missing"}},
			code: cerrors.NotFound,
		},
		{
			name: "unsupported source",
			spec: ocicompute.VolumeGroup{SourceType: "volumeGroupBackup", SourceID: "x"},
			code: cerrors.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.compute.CreateVolumeGroup(f.ctx, tc.spec)
			assert.Equal(t, tc.code, cerrors.GetCode(err))
		})
	}
}

func TestBackupsRecordTheirType(t *testing.T) {
	f := newFixture(t)

	vol, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{Size: 50})
	require.NoError(t, err)

	backup, err := f.compute.CreateVolumeBackup(f.ctx, vol.ID, "nightly", ocicompute.BackupFull, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 50, backup.Size)

	backupType, boot, ok := f.compute.VolumeBackupDetails(backup.ID)
	require.True(t, ok)
	assert.Equal(t, ocicompute.BackupFull, backupType)
	assert.False(t, boot)

	restored, err := f.compute.CreateVolumeFrom(f.ctx, driver.VolumeConfig{Size: 50},
		ocicompute.SourceDetails{SourceType: "volumeBackup", ID: backup.ID})
	require.NoError(t, err)
	assert.NotEqual(t, vol.ID, restored.ID)

	require.NoError(t, f.compute.DeleteSnapshot(f.ctx, backup.ID))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.DeleteSnapshot(f.ctx, backup.ID)))
}

func TestBackupErrors(t *testing.T) {
	f := newFixture(t)

	_, err := f.compute.CreateSnapshot(f.ctx, driver.SnapshotConfig{VolumeID: "ocid1.volume.oc1.iad.missing"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	vol, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{Size: 50})
	require.NoError(t, err)

	_, err = f.compute.CreateVolumeBackup(f.ctx, vol.ID, "x", "ARCHIVE", false, nil)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

func TestImagesIncludeThePlatformCatalogue(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	platform, err := f.compute.ListImages(f.ctx, otherCompartment, "", "")
	require.NoError(t, err)
	assert.NotEmpty(t, platform, "platform images are visible from every compartment")

	oracle, err := f.compute.ListImages(f.ctx, f.compartment, "Oracle Linux", "")
	require.NoError(t, err)
	require.NotEmpty(t, oracle)

	for i := range oracle {
		assert.Equal(t, "Oracle Linux", oracle[i].OperatingSystem)
	}

	custom, err := f.compute.CreateImage(f.ctx, driver.ImageConfig{InstanceID: inst.ID, Name: "golden"})
	require.NoError(t, err)

	got, err := f.compute.GetImage(f.ctx, custom.ID)
	require.NoError(t, err)
	assert.Equal(t, "golden", got.DisplayName)
	assert.Equal(t, f.image, got.BaseImageID)
	assert.False(t, got.IsPlatform)

	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(f.compute.DeregisterImage(f.ctx, f.image)),
		"a platform image cannot be deleted")
	require.NoError(t, f.compute.DeregisterImage(f.ctx, custom.ID))
}

func TestShapesAreListed(t *testing.T) {
	f := newFixture(t)

	shapes, err := f.compute.ListShapes(f.ctx, "")
	require.NoError(t, err)
	require.NotEmpty(t, shapes)

	_, err = f.compute.ListShapes(f.ctx, "ocid1.image.oc1.iad.missing")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	flex, ok := f.compute.Shape(shape)
	require.True(t, ok)
	assert.True(t, flex.IsFlexible)
}

func TestInstancePoolScalesItsMembership(t *testing.T) {
	f := newFixture(t)

	cfg, err := f.compute.CreateInstanceConfiguration(f.ctx, "web-tpl", ocicompute.LaunchSpec{
		Shape: shape, ImageID: f.image, SubnetID: f.subnet,
	}, nil)
	require.NoError(t, err)

	pool, err := f.compute.CreateInstancePool(f.ctx, "web", cfg.ID, 2, nil, nil)
	require.NoError(t, err)
	assert.Len(t, pool.InstanceIDs, 2)

	members, err := f.compute.ListInstancePoolInstances(f.ctx, pool.ID)
	require.NoError(t, err)
	assert.Len(t, members, 2)

	grown, err := f.compute.UpdateInstancePool(f.ctx, pool.ID, ocicompute.Update{}, 3)
	require.NoError(t, err)
	assert.Len(t, grown.InstanceIDs, 3)

	shrunk, err := f.compute.UpdateInstancePool(f.ctx, pool.ID, ocicompute.Update{}, 1)
	require.NoError(t, err)
	assert.Len(t, shrunk.InstanceIDs, 1)

	stopped, err := f.compute.InstancePoolAction(f.ctx, pool.ID, ocicompute.PoolActionStop)
	require.NoError(t, err)
	assert.Equal(t, "STOPPED", stopped.LifecycleState)

	_, err = f.compute.InstancePoolAction(f.ctx, pool.ID, "MIGRATE")
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(
		f.compute.DeleteInstanceConfiguration(f.ctx, cfg.ID)), "the pool still uses it")

	require.NoError(t, f.compute.TerminateInstancePool(f.ctx, pool.ID))
	require.NoError(t, f.compute.DeleteInstanceConfiguration(f.ctx, cfg.ID))
}

func TestInstanceConfigurationLaunch(t *testing.T) {
	f := newFixture(t)

	cfg, err := f.compute.CreateInstanceConfiguration(f.ctx, "tpl", ocicompute.LaunchSpec{
		Shape: shape, ImageID: f.image, SubnetID: f.subnet, DisplayName: "from-config",
	}, nil)
	require.NoError(t, err)

	inst, err := f.compute.LaunchFromInstanceConfiguration(f.ctx, cfg.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, shape, inst.InstanceType)

	details, ok := f.compute.InstanceDetails(inst.ID)
	require.True(t, ok)
	assert.Equal(t, cfg.ID, details.InstanceConfigurationID)

	_, err = f.compute.CreateInstanceConfiguration(f.ctx, "tpl", ocicompute.LaunchSpec{Shape: shape}, nil)
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	_, err = f.compute.LaunchFromInstanceConfiguration(f.ctx, "ocid1.instanceconfiguration.oc1.iad.x", nil)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestPreemptibleInstancesAreSpot(t *testing.T) {
	f := newFixture(t)

	reqs, err := f.compute.RequestSpotInstances(f.ctx, driver.SpotRequestConfig{
		InstanceConfig: driver.InstanceConfig{ImageID: f.image, InstanceType: shape, SubnetID: f.subnet},
		Count:          1,
	})
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	assert.Equal(t, "active", reqs[0].Status)

	got, err := f.compute.DescribeInstances(f.ctx, []string{reqs[0].InstanceID}, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Spot", got[0].Priority)

	require.NoError(t, f.compute.CancelSpotRequests(f.ctx, []string{reqs[0].ID}))

	after, err := f.compute.DescribeSpotRequests(f.ctx, []string{reqs[0].ID})
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.Equal(t, "canceled", after[0].Status)
}

func TestKeyPairsAreNotAnOCIResource(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "create",
			call: func() error {
				_, err := f.compute.CreateKeyPair(f.ctx, driver.KeyPairConfig{Name: "k"})

				return err
			},
		},
		{name: "delete", call: func() error { return f.compute.DeleteKeyPair(f.ctx, "k") }},
		{
			name: "describe",
			call: func() error {
				_, err := f.compute.DescribeKeyPairs(f.ctx, nil)

				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(err))
			assert.Contains(t, err.Error(), "ssh_authorized_keys")
		})
	}
}

func TestScopeAndCreatedAreRecorded(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	assert.Equal(t, scope.Scope{Compartment: f.compartment}, f.compute.Scope(inst.ID))
	assert.Equal(t, "2026-03-01T00:00:00Z", f.compute.Created(inst.ID))

	require.NoError(t, f.compute.SetTags(inst.ID, map[string]string{"env": "prod"}))

	got, err := f.compute.DescribeInstances(f.ctx, []string{inst.ID}, nil)
	require.NoError(t, err)
	assert.Equal(t, "prod", got[0].Tags["env"])

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.SetTags("ocid1.instance.oc1.iad.x", nil)))

	f.compute.SetScope(inst.ID, scope.Scope{})
	assert.True(t, f.compute.Scope(inst.ID).IsZero())
}

func TestSecondaryVNICAttachment(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	other, err := f.vcn.CreateSubnet(f.ctx, netdriver.SubnetConfig{VPCID: f.vcnID, CIDRBlock: "10.0.2.0/24"})
	require.NoError(t, err)

	att, err := f.compute.AttachVNIC(f.ctx, inst.ID, other.ID, "secondary", "", nil)
	require.NoError(t, err)
	assert.Equal(t, other.ID, att.SubnetID)

	atts, err := f.compute.ListVNICAttachments(f.ctx, f.compartment, inst.ID, "")
	require.NoError(t, err)
	assert.Len(t, atts, 2, "the primary VNIC is attached too")

	require.NoError(t, f.compute.DetachVNIC(f.ctx, att.ID))

	primary, err := f.compute.ListVNICAttachments(f.ctx, f.compartment, inst.ID, "")
	require.NoError(t, err)
	require.Len(t, primary, 1)

	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(f.compute.DetachVNIC(f.ctx, primary[0].ID)),
		"the primary VNIC is detached with the instance")
}
