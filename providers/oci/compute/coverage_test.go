package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	ocimonitoring "github.com/stackshy/cloudemu/v2/providers/oci/monitoring"
	driver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	missingInstance   = "ocid1.instance.oc1.iad.missing"
	missingBootVolume = "ocid1.bootvolume.oc1.iad.missing"
)

func ptr(s string) *string { return &s }

func (f *fixture) volume(t *testing.T, size int) string {
	t.Helper()

	v, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{Size: size})
	require.NoError(t, err)

	return v.ID
}

func (f *fixture) bootAttachmentOf(t *testing.T, instanceID string) string {
	t.Helper()

	atts, err := f.compute.ListBootVolumeAttachments(f.ctx, f.compartment, instanceID, "")
	require.NoError(t, err)
	require.Len(t, atts, 1)

	return atts[0].ID
}

func (f *fixture) bootVolumeOf(t *testing.T, instanceID string) string {
	t.Helper()

	d, ok := f.compute.InstanceDetails(instanceID)
	require.True(t, ok)

	return d.BootVolumeID
}

func TestBootVolumeCreateAndUpdate(t *testing.T) {
	f := newFixture(t)
	source := f.bootVolumeOf(t, f.launch(t).ID)

	clone, err := f.compute.CreateBootVolume(f.ctx, ocicompute.BootVolume{
		DisplayName:   "clone",
		SourceDetails: ocicompute.SourceDetails{SourceType: "bootVolume", ID: source},
		Tags:          map[string]string{"env": "dev"},
	})
	require.NoError(t, err)
	assert.Equal(t, "cloudemu:US-ASHBURN-1-AD-1", clone.AvailabilityDomain)
	assert.Equal(t, 50, clone.SizeInGBs)
	assert.Equal(t, 10, clone.VpusPerGB)
	assert.Equal(t, "AVAILABLE", clone.LifecycleState)

	backup, err := f.compute.CreateVolumeBackup(f.ctx, source, "boot", "", true, nil)
	require.NoError(t, err)

	tests := []struct {
		name   string
		source ocicompute.SourceDetails
		code   cerrors.Code
	}{
		{name: "no source", source: ocicompute.SourceDetails{}},
		{name: "image", source: ocicompute.SourceDetails{SourceType: "image", ID: f.image}},
		{name: "backup", source: ocicompute.SourceDetails{SourceType: "bootVolumeBackup", ID: backup.ID}},
		{
			name:   "unknown boot volume",
			source: ocicompute.SourceDetails{SourceType: "bootVolume", ID: missingBootVolume},
			code:   cerrors.NotFound,
		},
		{
			name:   "unknown backup",
			source: ocicompute.SourceDetails{SourceType: "volumeBackup", ID: "ocid1.volumebackup.oc1.iad.x"},
			code:   cerrors.NotFound,
		},
		{
			name:   "unknown image",
			source: ocicompute.SourceDetails{SourceType: "image", ID: "ocid1.image.oc1.iad.x"},
			code:   cerrors.NotFound,
		},
		{
			name:   "unsupported type",
			source: ocicompute.SourceDetails{SourceType: "volume", ID: "x"},
			code:   cerrors.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.compute.CreateBootVolume(f.ctx, ocicompute.BootVolume{
				AvailabilityDomain: "cloudemu:US-ASHBURN-1-AD-2", SizeInGBs: 80, VpusPerGB: 20,
				SourceDetails: tc.source,
			})
			assert.Equal(t, tc.code, cerrors.GetCode(err))
		})
	}

	updated, err := f.compute.UpdateBootVolume(f.ctx, clone.ID, ocicompute.Update{
		DisplayName: ptr("renamed"), Tags: map[string]string{"team": "x"},
	}, 100, 30)
	require.NoError(t, err)
	assert.Equal(t, "renamed", updated.DisplayName)
	assert.Equal(t, 100, updated.SizeInGBs)
	assert.Equal(t, 30, updated.VpusPerGB)
	assert.Equal(t, map[string]string{"team": "x"}, updated.Tags, "an update replaces the tags")

	kept, err := f.compute.UpdateBootVolume(f.ctx, clone.ID, ocicompute.Update{}, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 100, kept.SizeInGBs, "a zero size leaves it alone")

	_, err = f.compute.UpdateBootVolume(f.ctx, clone.ID, ocicompute.Update{}, 60, 0)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = f.compute.UpdateBootVolume(f.ctx, clone.ID, ocicompute.Update{}, 40000, 0)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err), "above OCI's 32768 GB cap")

	for _, size := range []int{10, 40000} {
		_, err = f.compute.CreateBootVolume(f.ctx, ocicompute.BootVolume{
			SizeInGBs: size, SourceDetails: ocicompute.SourceDetails{SourceType: "bootVolume", ID: source},
		})
		assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err), "create at %d GB", size)
	}

	_, err = f.compute.UpdateBootVolume(f.ctx, missingBootVolume, ocicompute.Update{}, 0, 0)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.DeleteBootVolume(f.ctx, missingBootVolume)))
}

func TestBootVolumeAttachDetach(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)
	original := f.bootVolumeOf(t, inst.ID)

	spare, err := f.compute.CreateBootVolume(f.ctx, ocicompute.BootVolume{
		SourceDetails: ocicompute.SourceDetails{SourceType: "bootVolume", ID: original},
	})
	require.NoError(t, err)

	atts, err := f.compute.ListBootVolumeAttachments(f.ctx, f.compartment, inst.ID, original)
	require.NoError(t, err)
	require.Len(t, atts, 1)

	got, err := f.compute.GetBootVolumeAttachment(f.ctx, atts[0].ID)
	require.NoError(t, err)
	assert.Equal(t, inst.ID, got.InstanceID)

	_, err = f.compute.GetBootVolumeAttachment(f.ctx, "ocid1.bootvolumeattachment.oc1.iad.x")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	// OCI swaps a boot volume only on a stopped instance.
	_, err = f.compute.AttachBootVolume(f.ctx, inst.ID, spare.ID, "")
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err), "attach to a running instance")
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(f.compute.DetachBootVolume(f.ctx, atts[0].ID)),
		"detach from a running instance")

	other := f.launch(t)
	require.NoError(t, f.compute.StopInstances(f.ctx, []string{inst.ID, other.ID}))
	require.NoError(t, f.compute.DetachBootVolume(f.ctx, f.bootAttachmentOf(t, other.ID)))

	for name, tc := range map[string]struct {
		instanceID, bootVolumeID string
		code                     cerrors.Code
	}{
		"already booted":      {inst.ID, spare.ID, cerrors.AlreadyExists},
		"volume in use":       {other.ID, original, cerrors.AlreadyExists},
		"unknown instance":    {missingInstance, spare.ID, cerrors.NotFound},
		"unknown boot volume": {inst.ID, missingBootVolume, cerrors.NotFound},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.compute.AttachBootVolume(f.ctx, tc.instanceID, tc.bootVolumeID, "")
			assert.Equal(t, tc.code, cerrors.GetCode(err))
		})
	}

	require.NoError(t, f.compute.DetachBootVolume(f.ctx, atts[0].ID))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.DetachBootVolume(f.ctx, atts[0].ID)))

	att, err := f.compute.AttachBootVolume(f.ctx, inst.ID, spare.ID, "swap")
	require.NoError(t, err)
	assert.Equal(t, "swap", att.DisplayName)
	assert.Equal(t, "ATTACHED", att.LifecycleState)

	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(f.compute.DeleteBootVolume(f.ctx, spare.ID)))
	require.NoError(t, f.compute.DeleteBootVolume(f.ctx, original))
}

func TestVNICAttachmentLookups(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	primary, err := f.compute.ListVNICAttachments(f.ctx, f.compartment, inst.ID, "")
	require.NoError(t, err)
	require.Len(t, primary, 1)

	got, err := f.compute.GetVNICAttachment(f.ctx, primary[0].ID)
	require.NoError(t, err)
	assert.Equal(t, inst.ID, got.InstanceID)
	assert.Equal(t, 0, got.NICIndex)

	_, err = f.compute.GetVNICAttachment(f.ctx, "ocid1.vnicattachment.oc1.iad.x")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	other, err := f.vcn.CreateSubnet(f.ctx, netdriver.SubnetConfig{VPCID: f.vcnID, CIDRBlock: "10.0.2.0/24"})
	require.NoError(t, err)

	att, err := f.compute.AttachVNIC(f.ctx, inst.ID, other.ID, "secondary", "web1b", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, att.NICIndex)

	vnics, err := f.vcn.DescribeVNICs(f.ctx, []string{att.VNICID})
	require.NoError(t, err)
	require.Len(t, vnics, 1)
	assert.Equal(t, "web1b", vnics[0].Hostname)

	_, err = f.compute.AttachVNIC(f.ctx, missingInstance, other.ID, "", "", nil)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = f.compute.AttachVNIC(f.ctx, inst.ID, "ocid1.subnet.oc1.iad.missing", "", "", nil)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	assert.Equal(t, cerrors.NotFound,
		cerrors.GetCode(f.compute.DetachVNIC(f.ctx, "ocid1.vnicattachment.oc1.iad.x")))
}

// newBareMock is a compute mock with no VCN service wired in.
func newBareMock(t *testing.T) (*ocicompute.Mock, string) {
	t.Helper()

	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))),
		config.WithRegion("us-ashburn-1"))
	m := ocicompute.New(opts)

	images, err := m.ListImages(t.Context(), opts.CompartmentID, "", "")
	require.NoError(t, err)
	require.NotEmpty(t, images)

	return m, images[0].ID
}

func TestLaunchWithoutNetworkingGetsASyntheticAddress(t *testing.T) {
	m, image := newBareMock(t)

	out, err := m.RunInstances(t.Context(), driver.InstanceConfig{ImageID: image, InstanceType: shape}, 2)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "10.0.0.1", out[0].PrivateIP)
	assert.Equal(t, "10.0.0.2", out[1].PrivateIP)
	assert.Empty(t, out[0].VPCID)

	_, err = m.AttachVNIC(t.Context(), out[0].ID, "ocid1.subnet.oc1.iad.x", "", "", nil)
	assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(err))
}

// putOnlyMonitoring is a portable monitoring driver, which cannot take OCI's
// reserved oci_ namespaces.
type putOnlyMonitoring struct {
	mondriver.Monitoring
	data []mondriver.MetricDatum
}

func (r *putOnlyMonitoring) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	r.data = append(r.data, data...)

	return nil
}

func TestInstanceMetricsReachMonitoring(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	mon := ocimonitoring.New(config.NewOptions(config.WithClock(config.NewFakeClock(now))))
	f.compute.SetMonitoring(mon)

	inst := f.launch(t)

	metrics, err := mon.ListOCIMetrics(f.ctx, f.compartment, ocimonitoring.OCIMetricFilter{
		Namespace: "oci_computeagent", Dimensions: map[string]string{"resourceId": inst.ID},
	})
	require.NoError(t, err)
	require.Len(t, metrics, 5, "the launch published every compute agent metric")

	f.compute.SetScope(inst.ID, scope.Scope{Compartment: otherCompartment})
	require.NoError(t, f.compute.StopInstances(f.ctx, []string{inst.ID}))

	cpu, err := mon.SummarizeOCIMetrics(f.ctx, otherCompartment, ocimonitoring.OCIMetricQuery{
		Namespace: "oci_computeagent",
		Query:     "CpuUtilization[1m].max()",
		StartTime: now.Add(-time.Minute),
		EndTime:   now.Add(time.Minute),
	})
	require.NoError(t, err)
	require.Len(t, cpu, 1, "the stop landed in the instance's current compartment")
	assert.Equal(t, inst.ID, cpu[0].Dimensions["resourceId"])
	assert.Equal(t, []float64{0}, cpu[0].Values)

	placed, err := f.compute.RunInstances(f.ctx, driver.InstanceConfig{
		ImageID: f.image, InstanceType: shape, SubnetID: f.subnet,
		Tags: map[string]string{ocicompute.TagCompartmentID: otherCompartment},
	}, 1)
	require.NoError(t, err)
	assert.NotContains(t, placed[0].Tags, ocicompute.TagCompartmentID)

	for _, id := range append(f.compute.LaunchedResourceIDs(placed[0].ID), placed[0].ID) {
		assert.Equal(t, otherCompartment, f.compute.Scope(id).Compartment, id)
	}

	launchMetrics, err := mon.ListOCIMetrics(f.ctx, otherCompartment, ocimonitoring.OCIMetricFilter{
		Namespace: "oci_computeagent", Dimensions: map[string]string{"resourceId": placed[0].ID},
	})
	require.NoError(t, err)
	assert.Len(t, launchMetrics, 5, "a launch publishes into the compartment it names")

	put := &putOnlyMonitoring{}
	f.compute.SetMonitoring(put)
	require.NoError(t, f.compute.StartInstances(f.ctx, []string{inst.ID}))
	assert.Empty(t, put.data, "a monitoring driver with no service ingest gets nothing")
}

func TestSubnetSecurityListsTagGovernsTheInstance(t *testing.T) {
	f := newFixture(t)

	subnet, err := f.vcn.CreateSubnet(f.ctx, netdriver.SubnetConfig{
		VPCID: f.vcnID, CIDRBlock: "10.0.3.0/24",
		Tags: map[string]string{"cloudemu:ociSecurityListIds": " sl-a , ,sl-b"},
	})
	require.NoError(t, err)

	blank, err := f.vcn.CreateSubnet(f.ctx, netdriver.SubnetConfig{
		VPCID: f.vcnID, CIDRBlock: "10.0.4.0/24",
		Tags: map[string]string{"cloudemu:ociSecurityListIds": " , "},
	})
	require.NoError(t, err)

	tagged, err := f.compute.RunInstances(f.ctx, driver.InstanceConfig{
		ImageID: f.image, InstanceType: shape, SubnetID: subnet.ID,
	}, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{"sl-a", "sl-b"}, tagged[0].SecurityGroups)

	fallback, err := f.compute.RunInstances(f.ctx, driver.InstanceConfig{
		ImageID: f.image, InstanceType: shape, SubnetID: blank.ID,
	}, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{f.vcn.Defaults(f.vcnID).SecurityListID}, fallback[0].SecurityGroups,
		"a tag naming no list falls back to the VCN default")
}

func TestVolumeUpdateAndSource(t *testing.T) {
	f := newFixture(t)
	id := f.volume(t, 50)

	updated, err := f.compute.UpdateBlockVolume(f.ctx, id, ocicompute.Update{
		DisplayName: ptr("data"), Tags: map[string]string{"env": "prod"},
	}, 100, 20)
	require.NoError(t, err)
	assert.Equal(t, 100, updated.Size)
	assert.Equal(t, 20, updated.IOPS)
	assert.Equal(t, "data", updated.Tags[ocicompute.TagDisplayName])
	assert.Equal(t, "prod", updated.Tags["env"])

	cleared, err := f.compute.UpdateBlockVolume(f.ctx, id, ocicompute.Update{DisplayName: ptr("")}, 0, 0)
	require.NoError(t, err)
	assert.NotContains(t, cleared.Tags, ocicompute.TagDisplayName)
	assert.Equal(t, 100, cleared.Size)

	_, err = f.compute.UpdateBlockVolume(f.ctx, id, ocicompute.Update{}, 50, 0)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = f.compute.UpdateBlockVolume(f.ctx, "ocid1.volume.oc1.iad.x", ocicompute.Update{}, 0, 0)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	source, vpus := f.compute.VolumeSource(id)
	assert.Equal(t, ocicompute.SourceDetails{}, source)
	assert.Equal(t, 20, vpus)

	source, vpus = f.compute.VolumeSource("ocid1.volume.oc1.iad.x")
	assert.Equal(t, ocicompute.SourceDetails{}, source)
	assert.Zero(t, vpus)

	clone, err := f.compute.CreateVolumeFrom(f.ctx, driver.VolumeConfig{},
		ocicompute.SourceDetails{SourceType: "volume", ID: id})
	require.NoError(t, err)
	assert.Equal(t, 100, clone.Size, "a clone without a size takes its source's")

	source, _ = f.compute.VolumeSource(clone.ID)
	assert.Equal(t, ocicompute.SourceDetails{SourceType: "volume", ID: id}, source)

	fromBoot, err := f.compute.CreateVolumeFrom(f.ctx, driver.VolumeConfig{},
		ocicompute.SourceDetails{SourceType: "bootVolume", ID: f.bootVolumeOf(t, f.launch(t).ID)})
	require.NoError(t, err)
	assert.Equal(t, 50, fromBoot.Size)

	for name, tc := range map[string]struct {
		source ocicompute.SourceDetails
		code   cerrors.Code
	}{
		"unknown volume":      {ocicompute.SourceDetails{SourceType: "volume", ID: "x"}, cerrors.NotFound},
		"unknown backup":      {ocicompute.SourceDetails{SourceType: "volumeBackup", ID: "x"}, cerrors.NotFound},
		"unknown boot volume": {ocicompute.SourceDetails{SourceType: "bootVolume", ID: "x"}, cerrors.NotFound},
		"unsupported":         {ocicompute.SourceDetails{SourceType: "image", ID: f.image}, cerrors.InvalidArgument},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.compute.CreateVolumeFrom(f.ctx, driver.VolumeConfig{Size: 50}, tc.source)
			assert.Equal(t, tc.code, cerrors.GetCode(err))
		})
	}

	_, err = f.compute.CreateVolume(f.ctx, driver.VolumeConfig{})
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err), "a size is required")
}

func TestVolumeAttachmentLookup(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)
	vol := f.volume(t, 50)

	att, err := f.compute.AttachVolumeToInstance(f.ctx, ocicompute.VolumeAttachment{
		InstanceID: inst.ID, VolumeID: vol, Device: "/dev/oracleoci/oraclevdb",
	})
	require.NoError(t, err)

	got, err := f.compute.GetVolumeAttachment(f.ctx, att.ID)
	require.NoError(t, err)
	assert.Equal(t, vol, got.VolumeID)
	assert.Equal(t, "/dev/oracleoci/oraclevdb", got.Device)

	require.NoError(t, f.compute.DetachVolumeAttachment(f.ctx, att.ID))

	_, err = f.compute.GetVolumeAttachment(f.ctx, att.ID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestVolumeGroupGetAndUpdate(t *testing.T) {
	f := newFixture(t)
	first := f.volume(t, 50)
	second := f.volume(t, 100)
	boot := f.bootVolumeOf(t, f.launch(t).ID)

	group, err := f.compute.CreateVolumeGroup(f.ctx, ocicompute.VolumeGroup{
		DisplayName: "cg", VolumeIDs: []string{first, boot},
	})
	require.NoError(t, err)
	assert.Equal(t, 100, group.SizeInGBs, "a boot volume member counts towards the size")

	got, err := f.compute.GetVolumeGroup(f.ctx, group.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{first, boot}, got.VolumeIDs)

	updated, err := f.compute.UpdateVolumeGroup(f.ctx, group.ID, ocicompute.Update{
		DisplayName: ptr("cg2"), Tags: map[string]string{"app": "db"},
	}, []string{first, second})
	require.NoError(t, err)
	assert.Equal(t, "cg2", updated.DisplayName)
	assert.Equal(t, 150, updated.SizeInGBs)
	assert.Equal(t, []string{first, second}, updated.VolumeIDs)
	assert.Equal(t, map[string]string{"app": "db"}, updated.Tags)

	renamed, err := f.compute.UpdateVolumeGroup(f.ctx, group.ID, ocicompute.Update{DisplayName: ptr("cg3")}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{first, second}, renamed.VolumeIDs, "nil membership leaves it alone")

	_, err = f.compute.UpdateVolumeGroup(f.ctx, group.ID, ocicompute.Update{}, []string{"ocid1.volume.oc1.iad.x"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	cloned, err := f.compute.CreateVolumeGroup(f.ctx, ocicompute.VolumeGroup{
		SourceType: "volumeGroup", SourceID: group.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{first, second}, cloned.VolumeIDs)

	const missing = "ocid1.volumegroup.oc1.iad.missing"

	_, err = f.compute.CreateVolumeGroup(f.ctx, ocicompute.VolumeGroup{SourceType: "volumeGroup", SourceID: missing})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = f.compute.GetVolumeGroup(f.ctx, missing)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = f.compute.UpdateVolumeGroup(f.ctx, missing, ocicompute.Update{}, nil)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.DeleteVolumeGroup(f.ctx, missing)))
}

func TestVolumeBackupUpdate(t *testing.T) {
	f := newFixture(t)
	vol := f.volume(t, 50)

	backup, err := f.compute.CreateVolumeBackup(f.ctx, vol, "nightly", "", false, map[string]string{"a": "1"})
	require.NoError(t, err)

	backupType, _, ok := f.compute.VolumeBackupDetails(backup.ID)
	require.True(t, ok)
	assert.Equal(t, ocicompute.BackupIncremental, backupType, "the type defaults to INCREMENTAL")

	updated, err := f.compute.UpdateVolumeBackup(f.ctx, backup.ID, ocicompute.Update{
		DisplayName: ptr("weekly"), Tags: map[string]string{"b": "2"},
	})
	require.NoError(t, err)
	assert.Equal(t, "weekly", updated.Description)
	assert.Equal(t, map[string]string{"b": "2"}, updated.Tags, "an update replaces the tags")

	_, err = f.compute.UpdateVolumeBackup(f.ctx, "ocid1.volumebackup.oc1.iad.x", ocicompute.Update{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, _, ok = f.compute.VolumeBackupDetails("ocid1.volumebackup.oc1.iad.x")
	assert.False(t, ok)

	_, err = f.compute.CreateVolumeBackup(f.ctx, missingBootVolume, "", ocicompute.BackupFull, true, nil)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestImageUpdate(t *testing.T) {
	f := newFixture(t)

	custom, err := f.compute.CreateImage(f.ctx, driver.ImageConfig{InstanceID: f.launch(t).ID, Name: "golden"})
	require.NoError(t, err)

	updated, err := f.compute.UpdateImage(f.ctx, custom.ID, ocicompute.Update{
		DisplayName: ptr("golden-v2"), Tags: map[string]string{"tier": "web"},
	})
	require.NoError(t, err)
	assert.Equal(t, "golden-v2", updated.DisplayName)
	assert.Equal(t, "web", updated.Tags["tier"])

	_, err = f.compute.UpdateImage(f.ctx, f.image, ocicompute.Update{DisplayName: ptr("mine")})
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	_, err = f.compute.UpdateImage(f.ctx, "ocid1.image.oc1.iad.x", ocicompute.Update{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = f.compute.GetImage(f.ctx, "ocid1.image.oc1.iad.x")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestInstanceDetailsAndModify(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)
	held, _ := f.compute.InstanceDetails(inst.ID)

	launched := f.compute.LaunchedResourceIDs(inst.ID)
	assert.Len(t, launched, 3, "the boot volume and the boot-volume and VNIC attachments")
	assert.Contains(t, launched, held.BootVolumeID)
	assert.Empty(t, f.compute.LaunchedResourceIDs(missingInstance))

	require.NoError(t, f.compute.SetInstanceDetails(inst.ID, ocicompute.InstanceDetails{
		DisplayName: "renamed", AvailabilityDomain: "cloudemu:US-ASHBURN-1-AD-3",
	}))

	d, ok := f.compute.InstanceDetails(inst.ID)
	require.True(t, ok)
	assert.Equal(t, "renamed", d.DisplayName)
	assert.Equal(t, held.BootVolumeID, d.BootVolumeID, "launch-time facts are carried over")
	assert.Equal(t, held.VNICID, d.VNICID)

	got, err := f.compute.DescribeInstances(f.ctx, []string{inst.ID}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"cloudemu:US-ASHBURN-1-AD-3"}, got[0].Zones)

	assert.Equal(t, cerrors.NotFound,
		cerrors.GetCode(f.compute.SetInstanceDetails(missingInstance, ocicompute.InstanceDetails{})))

	const other = "VM.Standard.E5.Flex"

	require.NoError(t, f.compute.ModifyInstance(f.ctx, inst.ID, driver.ModifyInstanceInput{
		InstanceType: shape, Tags: map[string]string{"env": "prod"},
	}))

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(
		f.compute.ModifyInstance(f.ctx, inst.ID, driver.ModifyInstanceInput{InstanceType: "VM.Nope.1"})))

	// OCI reshapes a running instance by rebooting it into the new shape.
	require.NoError(t, f.compute.ModifyInstance(f.ctx, inst.ID, driver.ModifyInstanceInput{InstanceType: other}))

	got, err = f.compute.DescribeInstances(f.ctx, []string{inst.ID}, nil)
	require.NoError(t, err)
	assert.Equal(t, other, got[0].InstanceType)
	assert.Equal(t, "running", got[0].State)
	assert.Equal(t, map[string]string{"env": "prod"}, got[0].Tags)

	assert.Equal(t, cerrors.NotFound,
		cerrors.GetCode(f.compute.ModifyInstance(f.ctx, missingInstance, driver.ModifyInstanceInput{})))
}

func TestLaunchFromABootVolume(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)
	boot := f.bootVolumeOf(t, inst.ID)

	require.NoError(t, f.compute.TerminateInstance(f.ctx, inst.ID, true))

	out, err := f.compute.RunInstances(f.ctx, driver.InstanceConfig{
		ImageID: boot, InstanceType: shape, SubnetID: f.subnet,
	}, 1)
	require.NoError(t, err)
	assert.Equal(t, f.image, out[0].ImageID, "the image is the boot volume's own")

	d, ok := f.compute.InstanceDetails(out[0].ID)
	require.True(t, ok)
	assert.Equal(t, boot, d.BootVolumeID, "the named boot volume is attached, not a new one")
	assert.Equal(t, ocicompute.SourceDetails{SourceType: "bootVolume", ID: boot}, d.SourceDetails)
	assert.Equal(t, out[0].ID, func() string {
		atts, err := f.compute.ListBootVolumeAttachments(f.ctx, f.compartment, "", boot)
		require.NoError(t, err)
		require.Len(t, atts, 1)

		return atts[0].InstanceID
	}())

	vols, err := f.compute.ListBootVolumes(f.ctx, f.compartment)
	require.NoError(t, err)
	assert.Len(t, vols, 1, "no second boot volume was created")

	_, err = f.compute.RunInstances(f.ctx, driver.InstanceConfig{ImageID: boot, InstanceType: shape}, 1)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err), "a boot volume backs one instance")

	// A failed batch keeps the caller's boot volume.
	require.NoError(t, f.compute.TerminateInstance(f.ctx, out[0].ID, true))

	_, err = f.compute.RunInstances(f.ctx, driver.InstanceConfig{ImageID: boot, InstanceType: shape}, 2)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	_, err = f.compute.GetBootVolume(f.ctx, boot)
	require.NoError(t, err)

	for name, cfg := range map[string]driver.InstanceConfig{
		"unknown boot volume": {ImageID: missingBootVolume, InstanceType: shape},
		"unknown image":       {ImageID: "ocid1.image.oc1.iad.x", InstanceType: shape},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.compute.RunInstances(f.ctx, cfg, 1)
			assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
		})
	}
}

func TestSetTagsReachesEveryResource(t *testing.T) {
	f := newFixture(t)
	vol := f.volume(t, 50)
	boot := f.bootVolumeOf(t, f.launch(t).ID)

	backup, err := f.compute.CreateVolumeBackup(f.ctx, vol, "", "", false, nil)
	require.NoError(t, err)

	group, err := f.compute.CreateVolumeGroup(f.ctx, ocicompute.VolumeGroup{VolumeIDs: []string{vol}})
	require.NoError(t, err)

	cfg, err := f.compute.CreateInstanceConfiguration(f.ctx, "tpl", ocicompute.LaunchSpec{
		Shape: shape, ImageID: f.image, SubnetID: f.subnet,
	}, nil)
	require.NoError(t, err)

	pool, err := f.compute.CreateInstancePool(f.ctx, "web", cfg.ID, 0, nil, nil)
	require.NoError(t, err)

	tags := map[string]string{"env": "prod"}

	for _, id := range []string{vol, boot, backup.ID, group.ID, cfg.ID, pool.ID} {
		require.NoError(t, f.compute.SetTags(id, tags), id)
	}

	vols, err := f.compute.DescribeVolumes(f.ctx, []string{vol})
	require.NoError(t, err)
	assert.Equal(t, tags, vols[0].Tags)

	bv, err := f.compute.GetBootVolume(f.ctx, boot)
	require.NoError(t, err)
	assert.Equal(t, tags, bv.Tags)

	snaps, err := f.compute.DescribeSnapshots(f.ctx, []string{backup.ID})
	require.NoError(t, err)
	assert.Equal(t, tags, snaps[0].Tags)

	g, err := f.compute.GetVolumeGroup(f.ctx, group.ID)
	require.NoError(t, err)
	assert.Equal(t, tags, g.Tags)

	c, err := f.compute.GetInstanceConfiguration(f.ctx, cfg.ID)
	require.NoError(t, err)
	assert.Equal(t, tags, c.Tags)

	p, err := f.compute.GetInstancePool(f.ctx, pool.ID)
	require.NoError(t, err)
	assert.Equal(t, tags, p.Tags)
}

func TestLaunchTemplatesAreInstanceConfigurations(t *testing.T) {
	f := newFixture(t)

	const ad = "cloudemu:US-ASHBURN-1-AD-2"

	lt, err := f.compute.CreateLaunchTemplate(f.ctx, driver.LaunchTemplateConfig{
		Name: "lt",
		InstanceConfig: driver.InstanceConfig{
			ImageID: f.image, InstanceType: shape, SubnetID: f.subnet, Zones: []string{ad},
			UserData: "aGk=", Priority: "Spot",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, lt.Version)

	_, err = f.compute.CreateLaunchTemplate(f.ctx, driver.LaunchTemplateConfig{Name: "lt"})
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	_, err = f.compute.CreateLaunchTemplate(f.ctx, driver.LaunchTemplateConfig{})
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	got, err := f.compute.GetLaunchTemplate(f.ctx, "lt")
	require.NoError(t, err)
	assert.Equal(t, lt.ID, got.ID)
	assert.Equal(t, shape, got.InstanceConfig.InstanceType)

	listed, err := f.compute.ListLaunchTemplates(f.ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	cfg, err := f.compute.GetInstanceConfiguration(f.ctx, lt.ID)
	require.NoError(t, err)
	assert.Equal(t, ad, cfg.Launch.AvailabilityDomain)
	assert.True(t, cfg.Launch.IsPreemptible)
	assert.Equal(t, "aGk=", cfg.Launch.Metadata["user_data"])

	require.NoError(t, f.compute.DeleteLaunchTemplate(f.ctx, "lt"))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.DeleteLaunchTemplate(f.ctx, "lt")))

	_, err = f.compute.GetLaunchTemplate(f.ctx, "lt")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestInstanceConfigurationUpdateAndOverrides(t *testing.T) {
	f := newFixture(t)

	cfg, err := f.compute.CreateInstanceConfiguration(f.ctx, "tpl", ocicompute.LaunchSpec{
		Shape: shape, ImageID: f.image, SubnetID: f.subnet, Tags: map[string]string{"base": "1"},
	}, nil)
	require.NoError(t, err)

	updated, err := f.compute.UpdateInstanceConfiguration(f.ctx, cfg.ID, ocicompute.Update{
		DisplayName: ptr("tpl2"), Tags: map[string]string{"v": "2"},
	})
	require.NoError(t, err)
	assert.Equal(t, "tpl2", updated.DisplayName)
	assert.Equal(t, "tpl2", updated.Launch.DisplayName)
	assert.Equal(t, map[string]string{"v": "2"}, updated.Tags)

	listed, err := f.compute.ListInstanceConfigurations(f.ctx, f.compartment)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	elsewhere, err := f.compute.ListInstanceConfigurations(f.ctx, otherCompartment)
	require.NoError(t, err)
	assert.Empty(t, elsewhere)

	const ad = "cloudemu:US-ASHBURN-1-AD-2"

	inst, err := f.compute.LaunchFromInstanceConfiguration(f.ctx, cfg.ID, &ocicompute.LaunchSpec{
		AvailabilityDomain: ad,
		DisplayName:        "override",
		ShapeConfig:        &ocicompute.ShapeConfig{OCPUs: 2},
		Metadata:           map[string]string{"user_data": "aGk="},
		Tags:               map[string]string{"extra": "y"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{ad}, inst.Zones)
	assert.Equal(t, "override", inst.Tags[ocicompute.TagDisplayName])
	assert.Equal(t, "1", inst.Tags["base"])
	assert.Equal(t, "y", inst.Tags["extra"])

	_, err = f.compute.CreateInstanceConfiguration(f.ctx, "", ocicompute.LaunchSpec{}, nil)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	const missing = "ocid1.instanceconfiguration.oc1.iad.missing"

	_, err = f.compute.GetInstanceConfiguration(f.ctx, missing)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = f.compute.UpdateInstanceConfiguration(f.ctx, missing, ocicompute.Update{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.DeleteInstanceConfiguration(f.ctx, missing)))
}

func TestAutoScalingGroupsArePools(t *testing.T) {
	f := newFixture(t)

	launch := driver.InstanceConfig{ImageID: f.image, InstanceType: shape, SubnetID: f.subnet}

	for name, tc := range map[string]struct {
		cfg  driver.AutoScalingGroupConfig
		code cerrors.Code
	}{
		"no name":        {driver.AutoScalingGroupConfig{}, cerrors.InvalidArgument},
		"negative":       {driver.AutoScalingGroupConfig{Name: "a", MinSize: -1}, cerrors.InvalidArgument},
		"min over max":   {driver.AutoScalingGroupConfig{Name: "a", MinSize: 3, MaxSize: 1}, cerrors.InvalidArgument},
		"over the limit": {driver.AutoScalingGroupConfig{Name: "a", DesiredCapacity: 1001}, cerrors.InvalidArgument},
		"over max": {
			driver.AutoScalingGroupConfig{Name: "a", DesiredCapacity: 3, MaxSize: 2}, cerrors.InvalidArgument,
		},
		"under min": {
			driver.AutoScalingGroupConfig{Name: "a", DesiredCapacity: 1, MinSize: 2}, cerrors.InvalidArgument,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.compute.CreateAutoScalingGroup(f.ctx, tc.cfg)
			assert.Equal(t, tc.code, cerrors.GetCode(err))
		})
	}

	asg, err := f.compute.CreateAutoScalingGroup(f.ctx, driver.AutoScalingGroupConfig{
		Name: "asg", MinSize: 1, MaxSize: 3, DesiredCapacity: 2, InstanceConfig: launch,
		AvailabilityZones: []string{"cloudemu:US-ASHBURN-1-AD-1"},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, asg.CurrentSize)
	assert.Equal(t, []string{"cloudemu:US-ASHBURN-1-AD-1"}, asg.AvailabilityZones)

	_, err = f.compute.CreateAutoScalingGroup(f.ctx, driver.AutoScalingGroupConfig{Name: "asg"})
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	listed, err := f.compute.ListAutoScalingGroups(f.ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	size := func() int {
		t.Helper()

		g, err := f.compute.GetAutoScalingGroup(f.ctx, "asg")
		require.NoError(t, err)

		return g.CurrentSize
	}

	require.NoError(t, f.compute.UpdateAutoScalingGroup(f.ctx, "asg", 3, 1, 3))
	assert.Equal(t, 3, size())

	require.NoError(t, f.compute.SetDesiredCapacity(f.ctx, "asg", 1))
	assert.Equal(t, 1, size())

	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(f.compute.SetDesiredCapacity(f.ctx, "asg", 4)))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.SetDesiredCapacity(f.ctx, "nope", 1)))

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.PutScalingPolicy(f.ctx,
		driver.ScalingPolicy{Name: "p", AutoScalingGroup: "nope"})))
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(f.compute.PutScalingPolicy(f.ctx,
		driver.ScalingPolicy{AutoScalingGroup: "asg"})))

	for _, tc := range []struct {
		policy driver.ScalingPolicy
		want   int
	}{
		{driver.ScalingPolicy{Name: "exact", AdjustmentType: "ExactCapacity", ScalingAdjustment: 2}, 2},
		{driver.ScalingPolicy{Name: "up", AdjustmentType: "ChangeInCapacity", ScalingAdjustment: 5}, 3},
		{driver.ScalingPolicy{Name: "half", AdjustmentType: "PercentChangeInCapacity", ScalingAdjustment: -50}, 2},
		{driver.ScalingPolicy{Name: "zero", AdjustmentType: "ExactCapacity", ScalingAdjustment: 0}, 1},
	} {
		tc.policy.AutoScalingGroup = "asg"
		require.NoError(t, f.compute.PutScalingPolicy(f.ctx, tc.policy))
		require.NoError(t, f.compute.ExecuteScalingPolicy(f.ctx, "asg", tc.policy.Name))
		assert.Equal(t, tc.want, size(), tc.policy.Name)
	}

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.ExecuteScalingPolicy(f.ctx, "asg", "ghost")))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.ExecuteScalingPolicy(f.ctx, "nope", "up")))

	require.NoError(t, f.compute.DeleteScalingPolicy(f.ctx, "asg", "up"))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.DeleteScalingPolicy(f.ctx, "asg", "up")))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.DeleteScalingPolicy(f.ctx, "nope", "up")))

	pools, err := f.compute.ListInstancePools(f.ctx, f.compartment)
	require.NoError(t, err)
	require.Len(t, pools, 1)

	_, err = f.compute.InstancePoolAction(f.ctx, pools[0].ID, ocicompute.PoolActionStop)
	require.NoError(t, err)

	stopped, err := f.compute.GetAutoScalingGroup(f.ctx, "asg")
	require.NoError(t, err)
	assert.Equal(t, "Stopped", stopped.Status)

	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(f.compute.DeleteAutoScalingGroup(f.ctx, "asg", false)),
		"a non-empty pool is only deleted when forced")
	require.NoError(t, f.compute.DeleteAutoScalingGroup(f.ctx, "asg", true))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.DeleteAutoScalingGroup(f.ctx, "asg", true)))

	_, err = f.compute.GetAutoScalingGroup(f.ctx, "asg")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestInstancePoolPlacementAndErrors(t *testing.T) {
	f := newFixture(t)

	cfg, err := f.compute.CreateInstanceConfiguration(f.ctx, "tpl", ocicompute.LaunchSpec{
		Shape: shape, ImageID: f.image, SubnetID: f.subnet,
	}, nil)
	require.NoError(t, err)

	const ad = "cloudemu:US-ASHBURN-1-AD-2"

	pool, err := f.compute.CreateInstancePool(f.ctx, "", cfg.ID, 1,
		[]ocicompute.PoolPlacement{{AvailabilityDomain: ad, PrimarySubnetID: f.subnet}}, map[string]string{"a": "1"})
	require.NoError(t, err)
	assert.Equal(t, cfg.ID, pool.DisplayName, "an unnamed pool takes its configuration's OCID")
	assert.Equal(t, cfg.ID, pool.InstanceConfigurationID)

	members, err := f.compute.ListInstancePoolInstances(f.ctx, pool.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, ad, members[0].AvailabilityDomain, "members land in the pool's placement")

	updated, err := f.compute.UpdateInstancePool(f.ctx, pool.ID, ocicompute.Update{
		DisplayName: ptr("web"), Tags: map[string]string{"b": "2"},
	}, -1)
	require.NoError(t, err)
	assert.Equal(t, "web", updated.DisplayName)
	assert.Equal(t, 1, updated.Size, "a negative size leaves it alone")
	assert.Equal(t, map[string]string{"b": "2"}, updated.Tags, "an update replaces the tags")

	_, err = f.compute.UpdateInstancePool(f.ctx, pool.ID, ocicompute.Update{}, 1001)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	for _, action := range []string{ocicompute.PoolActionStart, ocicompute.PoolActionReset} {
		got, err := f.compute.InstancePoolAction(f.ctx, pool.ID, action)
		require.NoError(t, err)
		assert.Equal(t, "RUNNING", got.LifecycleState, action)
	}

	_, err = f.compute.CreateInstancePool(f.ctx, "big", cfg.ID, 1001, nil, nil)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = f.compute.CreateInstancePool(f.ctx, "web", cfg.ID, 0, nil, nil)
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	const missing = "ocid1.instancepool.oc1.iad.missing"

	_, err = f.compute.CreateInstancePool(f.ctx, "x", "ocid1.instanceconfiguration.oc1.iad.x", 1, nil, nil)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = f.compute.GetInstancePool(f.ctx, missing)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = f.compute.UpdateInstancePool(f.ctx, missing, ocicompute.Update{}, -1)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = f.compute.InstancePoolAction(f.ctx, missing, ocicompute.PoolActionStart)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = f.compute.ListInstancePoolInstances(f.ctx, missing)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.TerminateInstancePool(f.ctx, missing)))
}

func TestSpotRequestEdges(t *testing.T) {
	f := newFixture(t)

	reqs, err := f.compute.RequestSpotInstances(f.ctx, driver.SpotRequestConfig{
		InstanceConfig: driver.InstanceConfig{ImageID: f.image, InstanceType: shape, SubnetID: f.subnet},
		Type:           "persistent",
	})
	require.NoError(t, err)
	require.Len(t, reqs, 1, "a zero count launches one")
	assert.Equal(t, "persistent", reqs[0].Type)

	// The instance going first leaves the cancel nothing to terminate.
	require.NoError(t, f.compute.TerminateInstance(f.ctx, reqs[0].InstanceID, false))
	require.NoError(t, f.compute.CancelSpotRequests(f.ctx, []string{reqs[0].ID}))

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(f.compute.CancelSpotRequests(f.ctx, []string{"ghost"})))

	_, err = f.compute.RequestSpotInstances(f.ctx, driver.SpotRequestConfig{
		InstanceConfig: driver.InstanceConfig{ImageID: f.image, InstanceType: "VM.Nope.1"},
	})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestRestoreRejectsMalformedSnapshot(t *testing.T) {
	f := newFixture(t)

	require.Error(t, f.compute.Restore(f.ctx, []byte("{")))
	require.Error(t, f.compute.Restore(f.ctx, []byte(`{"instances":"not a map"}`)))
}
