package ec2

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// bdmByDevice indexes an image's block device mappings by device name.
func bdmByDevice(bdms []driver.ImageBlockDeviceMapping) map[string]driver.ImageBlockDeviceMapping {
	out := make(map[string]driver.ImageBlockDeviceMapping, len(bdms))
	for _, b := range bdms {
		out[b.DeviceName] = b
	}

	return out
}

// runWithVolumes launches one instance backed by a boot volume plus the given
// extra data volumes, returning its id.
func runWithVolumes(t *testing.T, m *Mock, extra ...driver.BlockDeviceMapping) string {
	t.Helper()

	cfg := defaultConfig()
	cfg.BlockDeviceMappings = append([]driver.BlockDeviceMapping{
		{DeviceName: defaultRootDeviceName, Boot: true, AutoDelete: true, Size: 8, Type: "gp3"},
	}, extra...)

	insts, err := m.RunInstances(context.Background(), cfg, 1)
	requireNoError(t, err)

	return insts[0].ID
}

// TestCreateImageSnapshotsAttachedVolumes pins that the base CreateImage backs
// the AMI with a real snapshot of every attached volume (root + data), and that
// each referenced snapshot exists and points back at its source volume.
func TestCreateImageSnapshotsAttachedVolumes(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	id := runWithVolumes(t, m, driver.BlockDeviceMapping{
		DeviceName: "/dev/sdf", AutoDelete: false, Size: 20, Type: "gp2",
	})

	img, err := m.CreateImage(ctx, driver.ImageConfig{InstanceID: id, Name: "multi"})
	requireNoError(t, err)

	if len(img.BlockDeviceMappings) != 2 {
		t.Fatalf("BlockDeviceMappings = %d, want 2", len(img.BlockDeviceMappings))
	}

	byDev := bdmByDevice(img.BlockDeviceMappings)

	root := byDev[defaultRootDeviceName]
	assertNotEmpty(t, root.SnapshotID)
	assertEqual(t, 8, root.VolumeSize)
	assertTrue(t, root.DeleteOnTermination, "root DeleteOnTermination should be true")

	data := byDev["/dev/sdf"]
	assertNotEmpty(t, data.SnapshotID)
	assertEqual(t, 20, data.VolumeSize)
	assertEqual(t, "gp2", data.VolumeType)
	assertTrue(t, !data.DeleteOnTermination, "data DeleteOnTermination should be false")

	// Every referenced snapshot must exist and point back at an attached volume.
	vols, err := m.DescribeVolumes(ctx, nil)
	requireNoError(t, err)

	volByDevice := make(map[string]string)
	for i := range vols {
		if vols[i].AttachedTo == id {
			volByDevice[vols[i].Device] = vols[i].ID
		}
	}

	for _, b := range img.BlockDeviceMappings {
		snaps, err := m.DescribeSnapshots(ctx, []string{b.SnapshotID})
		requireNoError(t, err)
		assertEqual(t, 1, len(snaps))
		assertEqual(t, volByDevice[b.DeviceName], snaps[0].VolumeID)
	}
}

// TestCreateImageWithOptionsOverrideAndSuppress pins that a client
// BlockDeviceMapping override replaces an attached volume's size / type /
// DeleteOnTermination on the AMI, and a NoDevice entry suppresses its device.
func TestCreateImageWithOptionsOverrideAndSuppress(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	id := runWithVolumes(t, m, driver.BlockDeviceMapping{
		DeviceName: "/dev/sdf", AutoDelete: false, Size: 20, Type: "gp2",
	})

	img, err := m.CreateImageWithOptions(ctx, driver.ImageConfig{InstanceID: id, Name: "override"}, true,
		[]driver.ImageBlockDeviceMapping{{
			DeviceName: defaultRootDeviceName, VolumeSize: 30, VolumeType: "gp3", DeleteOnTermination: false,
		}},
		[]string{"/dev/sdf"},
		[]string{defaultRootDeviceName}, // client explicitly sent DeleteOnTermination=false
	)
	requireNoError(t, err)

	if len(img.BlockDeviceMappings) != 1 {
		t.Fatalf("BlockDeviceMappings = %d, want 1 (data device suppressed)", len(img.BlockDeviceMappings))
	}

	root := img.BlockDeviceMappings[0]
	assertEqual(t, defaultRootDeviceName, root.DeviceName)
	assertEqual(t, 30, root.VolumeSize)
	assertEqual(t, "gp3", root.VolumeType)
	assertTrue(t, !root.DeleteOnTermination, "override should have set DeleteOnTermination false")
	// The override keeps the backing snapshot taken from the real volume.
	assertNotEmpty(t, root.SnapshotID)
}

// TestCreateImageWithOptionsPreservesUnsetDoT pins the Packer/Terraform
// root-resize pattern: a client BDM override that sets only VolumeSize (and does
// not send DeleteOnTermination) must resize the AMI's root device while keeping
// the source volume's DeleteOnTermination (true for a default root) rather than
// silently clearing it to false. When the client DOES send DeleteOnTermination,
// it is applied.
func TestCreateImageWithOptionsPreservesUnsetDoT(t *testing.T) {
	ctx := context.Background()

	// Size-only override (no device in dotSet): source DoT (true) preserved.
	t.Run("unset preserves source", func(t *testing.T) {
		m := newTestMock()
		id := runWithVolumes(t, m)

		img, err := m.CreateImageWithOptions(ctx, driver.ImageConfig{InstanceID: id, Name: "resize"}, true,
			[]driver.ImageBlockDeviceMapping{{DeviceName: defaultRootDeviceName, VolumeSize: 100}},
			nil, nil, // DeleteOnTermination not sent
		)
		requireNoError(t, err)

		root := bdmByDevice(img.BlockDeviceMappings)[defaultRootDeviceName]
		assertEqual(t, 100, root.VolumeSize)
		assertTrue(t, root.DeleteOnTermination,
			"size-only override must preserve source DeleteOnTermination=true")
	})

	// Explicit DeleteOnTermination=false (device in dotSet): applied.
	t.Run("explicit false applies", func(t *testing.T) {
		m := newTestMock()
		id := runWithVolumes(t, m)

		img, err := m.CreateImageWithOptions(ctx, driver.ImageConfig{InstanceID: id, Name: "detach"}, true,
			[]driver.ImageBlockDeviceMapping{{DeviceName: defaultRootDeviceName, DeleteOnTermination: false}},
			nil, []string{defaultRootDeviceName},
		)
		requireNoError(t, err)

		root := bdmByDevice(img.BlockDeviceMappings)[defaultRootDeviceName]
		assertTrue(t, !root.DeleteOnTermination,
			"explicit DeleteOnTermination=false must be applied")
	})
}

// TestCreateImageWithOptionsAddsMapping pins that a client mapping for a device
// that has no backing volume is appended to the AMI's block device mapping.
func TestCreateImageWithOptionsAddsMapping(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	id := runWithVolumes(t, m)

	img, err := m.CreateImageWithOptions(ctx, driver.ImageConfig{InstanceID: id, Name: "add"}, true,
		[]driver.ImageBlockDeviceMapping{{DeviceName: "/dev/sdg", VolumeSize: 50, DeleteOnTermination: true}},
		nil, []string{"/dev/sdg"},
	)
	requireNoError(t, err)

	byDev := bdmByDevice(img.BlockDeviceMappings)
	if _, ok := byDev[defaultRootDeviceName]; !ok {
		t.Fatalf("root mapping missing: %+v", img.BlockDeviceMappings)
	}

	added, ok := byDev["/dev/sdg"]
	if !ok {
		t.Fatalf("added mapping /dev/sdg missing: %+v", img.BlockDeviceMappings)
	}

	assertEqual(t, 50, added.VolumeSize)
	assertEqual(t, defaultVolumeType, added.VolumeType) // defaulted when client omits type
	assertEqual(t, "", added.SnapshotID)                // a brand-new device has no snapshot
}

// TestCreateImageRebootHonorsNoReboot pins that CreateImage reboots a running
// source instance (emitting lifecycle metrics) when NoReboot is false, and does
// not when NoReboot is true. The instance stays running either way.
func TestCreateImageRebootHonorsNoReboot(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	mon := &captureMonitoring{}
	m.SetMonitoring(mon)

	id := runWithVolumes(t, m)

	// NoReboot=true: no reboot, no new lifecycle metrics.
	before := metricCount(mon)

	_, err := m.CreateImageWithOptions(ctx, driver.ImageConfig{InstanceID: id, Name: "noreboot"}, true, nil, nil, nil)
	requireNoError(t, err)

	if got := metricCount(mon); got != before {
		t.Fatalf("NoReboot=true emitted %d metric datums, want 0", got-before)
	}

	// NoReboot=false: the running instance is rebooted, emitting one lifecycle
	// batch (5 datums), and remains running.
	before = metricCount(mon)

	_, err = m.CreateImageWithOptions(ctx, driver.ImageConfig{InstanceID: id, Name: "reboot"}, false, nil, nil, nil)
	requireNoError(t, err)

	if got := metricCount(mon) - before; got != 5 {
		t.Fatalf("NoReboot=false emitted %d metric datums, want 5 (reboot lifecycle batch)", got)
	}

	insts, err := m.DescribeInstances(ctx, []string{id}, nil, driver.DescribeInstancesOptions{})
	requireNoError(t, err)
	assertEqual(t, "running", insts[0].State)
}

// metricCount returns the number of metric datums captured so far.
func metricCount(c *captureMonitoring) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.data)
}
