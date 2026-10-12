package compute_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	ocivcn "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	driver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (f *fixture) attach(t *testing.T, volumeID, instanceID string, shareable bool) *ocicompute.VolumeAttachment {
	t.Helper()

	att, err := f.compute.AttachVolumeToInstance(f.ctx, ocicompute.VolumeAttachment{
		InstanceID: instanceID, VolumeID: volumeID, IsShareable: shareable,
	})
	require.NoError(t, err)

	return att
}

func TestSharedVolumeStaysAttachedUntilItsLastAttachment(t *testing.T) {
	f := newFixture(t)
	a, b := f.launch(t), f.launch(t)
	vol := f.volume(t, 50)

	first := f.attach(t, vol, a.ID, true)
	f.attach(t, vol, b.ID, true)

	require.NoError(t, f.compute.DetachVolumeAttachment(f.ctx, first.ID))

	got, err := f.compute.DescribeVolumes(f.ctx, []string{vol})
	require.NoError(t, err)
	assert.Equal(t, "in-use", got[0].State, "b still holds the volume")
	assert.Equal(t, b.ID, got[0].AttachedTo)

	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(f.compute.DeleteVolume(f.ctx, vol)),
		"a volume with a live attachment cannot be deleted")

	require.NoError(t, f.compute.DetachVolume(f.ctx, vol, b.ID, ""))
	require.NoError(t, f.compute.DeleteVolume(f.ctx, vol))
}

func TestSharedVolumeAttachRules(t *testing.T) {
	f := newFixture(t)
	a, b := f.launch(t), f.launch(t)

	exclusive := f.volume(t, 50)
	f.attach(t, exclusive, a.ID, false)

	shared := f.volume(t, 50)
	f.attach(t, shared, a.ID, true)

	for name, tc := range map[string]ocicompute.VolumeAttachment{
		"shareable onto an exclusive attachment": {InstanceID: b.ID, VolumeID: exclusive, IsShareable: true},
		"exclusive onto a shared volume":         {InstanceID: b.ID, VolumeID: shared},
		"same instance twice":                    {InstanceID: a.ID, VolumeID: shared, IsShareable: true},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.compute.AttachVolumeToInstance(f.ctx, tc)
			assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))
		})
	}
}

func TestPortableDetachNarrowsToOneAttachment(t *testing.T) {
	f := newFixture(t)
	a, b := f.launch(t), f.launch(t)
	vol := f.volume(t, 50)

	f.attach(t, vol, a.ID, true)
	f.attach(t, vol, b.ID, true)

	assert.Equal(t, cerrors.NotFound,
		cerrors.GetCode(f.compute.DetachVolume(f.ctx, vol, "ocid1.instance.oc1.iad.other", "")))

	require.NoError(t, f.compute.DetachVolume(f.ctx, vol, a.ID, ""))

	atts, err := f.compute.ListVolumeAttachments(f.ctx, f.compartment, "", vol)
	require.NoError(t, err)
	require.Len(t, atts, 1)
	assert.Equal(t, b.ID, atts[0].InstanceID)

	require.NoError(t, f.compute.TerminateInstances(f.ctx, []string{b.ID}))

	got, err := f.compute.DescribeVolumes(f.ctx, []string{vol})
	require.NoError(t, err)
	assert.Equal(t, "available", got[0].State, "terminating the last holder frees the volume")
}

func TestAttachAcrossAvailabilityDomainsIsRefused(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	vol, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{Size: 50, AvailabilityZone: "cloudemu:US-ASHBURN-1-AD-2"})
	require.NoError(t, err)

	_, err = f.compute.AttachVolumeToInstance(f.ctx, ocicompute.VolumeAttachment{InstanceID: inst.ID, VolumeID: vol.ID})
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

func TestISCSIAttachmentCarriesItsTarget(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	att, err := f.compute.AttachVolumeToInstance(f.ctx, ocicompute.VolumeAttachment{
		InstanceID: inst.ID, VolumeID: f.volume(t, 50), AttachmentType: ocicompute.AttachmentISCSI,
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(att.IPv4, "169.254.2."), "got %q", att.IPv4)
	assert.True(t, strings.HasPrefix(att.IQN, "iqn.2015-12.com.oracleiaas:"), "got %q", att.IQN)
	assert.Equal(t, 3260, att.Port)

	again, err := f.compute.GetVolumeAttachment(f.ctx, att.ID)
	require.NoError(t, err)
	assert.Equal(t, att.IQN, again.IQN, "the target is stable across reads")

	pv := f.attach(t, f.volume(t, 50), inst.ID, false)
	assert.Empty(t, pv.IQN, "a paravirtualized attachment has no iSCSI target")
}

func TestUpdateVolumeBounds(t *testing.T) {
	f := newFixture(t)
	vol := f.volume(t, 100)

	for name, tc := range map[string]struct{ size, vpus int }{
		"above the cap":   {40000, 0},
		"shrink":          {60, 0},
		"odd vpus":        {0, 7},
		"vpus above 120":  {0, 130},
		"negative vpus":   {0, -10},
		"vpus off a step": {0, 35},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.compute.UpdateBlockVolume(f.ctx, vol, ocicompute.Update{}, tc.size, tc.vpus)
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
		})
	}

	got, err := f.compute.UpdateBlockVolume(f.ctx, vol, ocicompute.Update{}, 32768, 120)
	require.NoError(t, err)
	assert.Equal(t, 32768, got.Size)
	assert.Equal(t, 120, got.IOPS)
}

func TestUpdatesReplaceFreeformTags(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)
	upd := ocicompute.Update{Tags: map[string]string{"b": "2"}}
	want := map[string]string{"b": "2"}

	vol, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{
		Size: 50, Tags: map[string]string{"a": "1", ocicompute.TagDisplayName: "data"},
	})
	require.NoError(t, err)

	updated, err := f.compute.UpdateBlockVolume(f.ctx, vol.ID, upd, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"b": "2", ocicompute.TagDisplayName: "data"}, updated.Tags,
		"the internal display name survives a tag replace")

	img, err := f.compute.CreateImage(f.ctx, driver.ImageConfig{
		InstanceID: inst.ID, Name: "golden", Tags: map[string]string{"a": "1"},
	})
	require.NoError(t, err)

	gotImage, err := f.compute.UpdateImage(f.ctx, img.ID, upd)
	require.NoError(t, err)
	assert.Equal(t, want, gotImage.Tags)

	group, err := f.compute.CreateVolumeGroup(f.ctx, ocicompute.VolumeGroup{
		VolumeIDs: []string{vol.ID}, Tags: map[string]string{"a": "1"},
	})
	require.NoError(t, err)

	gotGroup, err := f.compute.UpdateVolumeGroup(f.ctx, group.ID, upd, nil)
	require.NoError(t, err)
	assert.Equal(t, want, gotGroup.Tags)

	cfg, err := f.compute.CreateInstanceConfiguration(f.ctx, "tpl", ocicompute.LaunchSpec{
		Shape: shape, ImageID: f.image,
	}, map[string]string{"a": "1"})
	require.NoError(t, err)

	gotCfg, err := f.compute.UpdateInstanceConfiguration(f.ctx, cfg.ID, upd)
	require.NoError(t, err)
	assert.Equal(t, want, gotCfg.Tags)

	kept, err := f.compute.UpdateInstanceConfiguration(f.ctx, cfg.ID, ocicompute.Update{DisplayName: ptr("tpl2")})
	require.NoError(t, err)
	assert.Equal(t, want, kept.Tags, "an update naming no tags leaves them alone")
}

func TestLaunchSizesTheBootVolume(t *testing.T) {
	f := newFixture(t)

	out, err := f.compute.RunInstances(f.ctx, driver.InstanceConfig{
		ImageID: f.image, InstanceType: shape,
		Tags: map[string]string{ocicompute.TagBootVolumeSizeInGBs: "100"},
	}, 1)
	require.NoError(t, err)
	assert.NotContains(t, out[0].Tags, ocicompute.TagBootVolumeSizeInGBs, "the size is not kept on the instance")

	bv, err := f.compute.GetBootVolume(f.ctx, f.bootVolumeOf(t, out[0].ID))
	require.NoError(t, err)
	assert.Equal(t, 100, bv.SizeInGBs)

	for _, size := range []string{"10", "40000", "big"} {
		_, err := f.compute.RunInstances(f.ctx, driver.InstanceConfig{
			ImageID: f.image, InstanceType: shape,
			Tags: map[string]string{ocicompute.TagBootVolumeSizeInGBs: size},
		}, 1)
		assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err), "size %s", size)
	}
}

// racingNetworking terminates the instance while its VNIC is being created,
// as a TerminateInstance landing mid-AttachVNIC would.
type racingNetworking struct {
	*ocivcn.Mock
	compute    *ocicompute.Mock
	instanceID string
}

func (r *racingNetworking) CreateNetworkInterface(
	ctx context.Context, subnetID, description string, groups []string, tags map[string]string,
) (*netdriver.NetworkInterface, error) {
	nic, err := r.Mock.CreateNetworkInterface(ctx, subnetID, description, groups, tags)
	if err != nil {
		return nil, err
	}

	if err := r.compute.TerminateInstance(ctx, r.instanceID, false); err != nil {
		return nil, err
	}

	return nic, nil
}

func TestAttachVNICRacingTerminateLeaksNoVNIC(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	f.compute.SetNetworking(&racingNetworking{Mock: f.vcn, compute: f.compute, instanceID: inst.ID})

	_, err := f.compute.AttachVNIC(f.ctx, inst.ID, f.subnet, "", "", nil)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	vnics, err := f.vcn.DescribeVNICs(f.ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, vnics, "neither the primary nor the half-attached VNIC is left in the VCN")
}
