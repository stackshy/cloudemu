package ec2

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/compute"
	"github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/config"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	return New(opts)
}

func defaultConfig() driver.InstanceConfig {
	return driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
		Tags:         map[string]string{"env": "test"},
	}
}

func TestRunInstances(t *testing.T) {
	tests := []struct {
		name      string
		count     int
		expectErr bool
		expectLen int
	}{
		{name: "single instance", count: 1, expectLen: 1},
		{name: "multiple instances", count: 3, expectLen: 3},
		{name: "zero count", count: 0, expectErr: true},
		{name: "negative count", count: -1, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			instances, err := m.RunInstances(context.Background(), defaultConfig(), tc.count)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, tc.expectLen, len(instances))

			for _, inst := range instances {
				assertEqual(t, compute.StateRunning, inst.State)
				assertEqual(t, "ami-12345", inst.ImageID)
				assertEqual(t, "t2.micro", inst.InstanceType)
				assertNotEmpty(t, inst.ID)
				assertNotEmpty(t, inst.PrivateIP)
			}
		})
	}
}

func TestDescribeInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 2)

	t.Run("all instances", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, nil, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(result))
	})

	t.Run("by ID", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, []string{instances[0].ID}, nil)
		requireNoError(t, err)
		assertEqual(t, 1, len(result))
		assertEqual(t, instances[0].ID, result[0].ID)
	})

	t.Run("filter by state", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, nil, []driver.DescribeFilter{
			{Name: "instance-state-name", Values: []string{"running"}},
		})
		requireNoError(t, err)
		assertEqual(t, 2, len(result))
	})

	t.Run("filter by instance-type", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, nil, []driver.DescribeFilter{
			{Name: "instance-type", Values: []string{"t2.micro"}},
		})
		requireNoError(t, err)
		assertEqual(t, 2, len(result))
	})

	t.Run("filter by tag", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, nil, []driver.DescribeFilter{
			{Name: "tag:env", Values: []string{"test"}},
		})
		requireNoError(t, err)
		assertEqual(t, 2, len(result))
	})

	t.Run("filter no match", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, nil, []driver.DescribeFilter{
			{Name: "instance-state-name", Values: []string{"stopped"}},
		})
		requireNoError(t, err)
		assertEqual(t, 0, len(result))
	})
}

func TestStartInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	_ = m.StopInstances(ctx, []string{id})

	t.Run("success", func(t *testing.T) {
		err := m.StartInstances(ctx, []string{id})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, compute.StateRunning, result[0].State)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.StartInstances(ctx, []string{"i-nonexistent"})
		assertError(t, err, true)
	})
}

func TestStopInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	t.Run("success", func(t *testing.T) {
		err := m.StopInstances(ctx, []string{id})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, compute.StateStopped, result[0].State)
	})

	t.Run("cannot stop already stopped", func(t *testing.T) {
		err := m.StopInstances(ctx, []string{id})
		assertError(t, err, true)
	})
}

func TestRebootInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	t.Run("success", func(t *testing.T) {
		err := m.RebootInstances(ctx, []string{id})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, compute.StateRunning, result[0].State)
	})
}

func TestTerminateInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	t.Run("success", func(t *testing.T) {
		err := m.TerminateInstances(ctx, []string{id})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, compute.StateTerminated, result[0].State)
	})

	t.Run("cannot terminate terminated", func(t *testing.T) {
		err := m.TerminateInstances(ctx, []string{id})
		assertError(t, err, true)
	})
}

func TestModifyInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	t.Run("must be stopped", func(t *testing.T) {
		err := m.ModifyInstance(ctx, id, driver.ModifyInstanceInput{InstanceType: "t2.large"})
		assertError(t, err, true)
	})

	_ = m.StopInstances(ctx, []string{id})

	t.Run("success - change type", func(t *testing.T) {
		err := m.ModifyInstance(ctx, id, driver.ModifyInstanceInput{InstanceType: "t2.large"})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, "t2.large", result[0].InstanceType)
	})

	t.Run("success - change tags", func(t *testing.T) {
		err := m.ModifyInstance(ctx, id, driver.ModifyInstanceInput{Tags: map[string]string{"new": "tag"}})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, "tag", result[0].Tags["new"])
	})

	t.Run("not found", func(t *testing.T) {
		err := m.ModifyInstance(ctx, "i-nonexistent", driver.ModifyInstanceInput{})
		assertError(t, err, true)
	})
}

func TestMatchesFilters(t *testing.T) {
	inst := &instanceData{
		ID:           "i-123",
		InstanceType: "t2.micro",
		State:        "running",
		Tags:         map[string]string{"env": "prod"},
	}

	tests := []struct {
		name    string
		filters []driver.DescribeFilter
		expect  bool
	}{
		{name: "no filters", filters: nil, expect: true},
		{name: "match instance-id", filters: []driver.DescribeFilter{{Name: "instance-id", Values: []string{"i-123"}}}, expect: true},
		{name: "no match instance-id", filters: []driver.DescribeFilter{{Name: "instance-id", Values: []string{"i-999"}}}, expect: false},
		{name: "match tag", filters: []driver.DescribeFilter{{Name: "tag:env", Values: []string{"prod"}}}, expect: true},
		{name: "no match tag", filters: []driver.DescribeFilter{{Name: "tag:env", Values: []string{"dev"}}}, expect: false},
		{name: "unknown filter passes", filters: []driver.DescribeFilter{{Name: "unknown", Values: []string{"x"}}}, expect: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := matchesFilters(inst, tc.filters)
			assertEqual(t, tc.expect, result)
		})
	}
}

func TestLifecycleStateMachine(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	// running -> stop -> stopped
	requireNoError(t, m.StopInstances(ctx, []string{id}))
	desc, _ := m.DescribeInstances(ctx, []string{id}, nil)
	assertEqual(t, compute.StateStopped, desc[0].State)

	// stopped -> start -> running
	requireNoError(t, m.StartInstances(ctx, []string{id}))
	desc, _ = m.DescribeInstances(ctx, []string{id}, nil)
	assertEqual(t, compute.StateRunning, desc[0].State)

	// running -> reboot -> running
	requireNoError(t, m.RebootInstances(ctx, []string{id}))
	desc, _ = m.DescribeInstances(ctx, []string{id}, nil)
	assertEqual(t, compute.StateRunning, desc[0].State)

	// running -> terminate -> terminated
	requireNoError(t, m.TerminateInstances(ctx, []string{id}))
	desc, _ = m.DescribeInstances(ctx, []string{id}, nil)
	assertEqual(t, compute.StateTerminated, desc[0].State)
}

// =====================================================================
// Volume Tests
// =====================================================================

func TestCreateVolume(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.VolumeConfig
		expectErr bool
	}{
		{
			name:      "success",
			cfg:       driver.VolumeConfig{Size: 100, VolumeType: "gp3", Tags: map[string]string{"env": "test"}},
			expectErr: false,
		},
		{
			name:      "default volume type",
			cfg:       driver.VolumeConfig{Size: 50},
			expectErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			ctx := context.Background()

			vol, err := m.CreateVolume(ctx, tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertNotEmpty(t, vol.ID)
			assertEqual(t, tc.cfg.Size, vol.Size)
			assertEqual(t, "available", vol.State)
			assertNotEmpty(t, vol.CreatedAt)
		})
	}
}

func TestDeleteVolume(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		requireNoError(t, err)

		err = m.DeleteVolume(ctx, vol.ID)
		requireNoError(t, err)

		// Should be gone
		vols, err := m.DescribeVolumes(ctx, []string{vol.ID})
		requireNoError(t, err)
		assertEqual(t, 0, len(vols))
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		err := m.DeleteVolume(ctx, "vol-nonexistent")
		assertError(t, err, true)
	})
}

func TestDescribeVolumes(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	vol1, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	requireNoError(t, err)

	vol2, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 20})
	requireNoError(t, err)

	t.Run("describe all", func(t *testing.T) {
		vols, err := m.DescribeVolumes(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(vols))
	})

	t.Run("describe by ID", func(t *testing.T) {
		vols, err := m.DescribeVolumes(ctx, []string{vol1.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(vols))
		assertEqual(t, vol1.ID, vols[0].ID)
	})

	t.Run("empty list", func(t *testing.T) {
		fresh := newTestMock()
		vols, err := fresh.DescribeVolumes(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 0, len(vols))
	})

	// Keep vol2 referenced to avoid unused variable
	assertNotEmpty(t, vol2.ID)
}

func TestAttachVolume(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		instances, err := m.RunInstances(ctx, defaultConfig(), 1)
		requireNoError(t, err)

		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		requireNoError(t, err)

		err = m.AttachVolume(ctx, vol.ID, instances[0].ID, "/dev/sdf")
		requireNoError(t, err)

		// Verify state changed to in-use
		vols, err := m.DescribeVolumes(ctx, []string{vol.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(vols))
		assertEqual(t, "in-use", vols[0].State)
		assertEqual(t, instances[0].ID, vols[0].AttachedTo)
	})

	t.Run("nonexistent instance", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		requireNoError(t, err)

		err = m.AttachVolume(ctx, vol.ID, "i-nonexistent", "/dev/sdf")
		assertError(t, err, true)
	})

	t.Run("nonexistent volume", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		instances, err := m.RunInstances(ctx, defaultConfig(), 1)
		requireNoError(t, err)

		err = m.AttachVolume(ctx, "vol-nonexistent", instances[0].ID, "/dev/sdf")
		assertError(t, err, true)
	})
}

func TestDetachVolume(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		instances, err := m.RunInstances(ctx, defaultConfig(), 1)
		requireNoError(t, err)

		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		requireNoError(t, err)

		err = m.AttachVolume(ctx, vol.ID, instances[0].ID, "/dev/sdf")
		requireNoError(t, err)

		err = m.DetachVolume(ctx, vol.ID)
		requireNoError(t, err)

		// Verify state changed back to available
		vols, err := m.DescribeVolumes(ctx, []string{vol.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(vols))
		assertEqual(t, "available", vols[0].State)
	})

	t.Run("detach unattached volume", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		requireNoError(t, err)

		err = m.DetachVolume(ctx, vol.ID)
		assertError(t, err, true)
	})
}

// =====================================================================
// Snapshot Tests
// =====================================================================

func TestCreateSnapshot(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 50})
		requireNoError(t, err)

		snap, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{
			VolumeID:    vol.ID,
			Description: "test snapshot",
			Tags:        map[string]string{"env": "test"},
		})
		requireNoError(t, err)

		assertNotEmpty(t, snap.ID)
		assertEqual(t, vol.ID, snap.VolumeID)
		assertEqual(t, "completed", snap.State)
		assertEqual(t, "test snapshot", snap.Description)
		assertEqual(t, 50, snap.Size)
		assertNotEmpty(t, snap.CreatedAt)
	})

	t.Run("nonexistent volume", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{
			VolumeID: "vol-nonexistent",
		})
		assertError(t, err, true)
	})
}

func TestDeleteSnapshot(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		requireNoError(t, err)

		snap, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: vol.ID})
		requireNoError(t, err)

		err = m.DeleteSnapshot(ctx, snap.ID)
		requireNoError(t, err)

		// Should be gone
		snaps, err := m.DescribeSnapshots(ctx, []string{snap.ID})
		requireNoError(t, err)
		assertEqual(t, 0, len(snaps))
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		err := m.DeleteSnapshot(ctx, "snap-nonexistent")
		assertError(t, err, true)
	})
}

func TestDescribeSnapshots(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	requireNoError(t, err)

	snap1, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: vol.ID})
	requireNoError(t, err)

	snap2, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: vol.ID})
	requireNoError(t, err)

	t.Run("describe all", func(t *testing.T) {
		snaps, err := m.DescribeSnapshots(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(snaps))
	})

	t.Run("describe by ID", func(t *testing.T) {
		snaps, err := m.DescribeSnapshots(ctx, []string{snap1.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(snaps))
		assertEqual(t, snap1.ID, snaps[0].ID)
	})

	// Keep snap2 referenced
	assertNotEmpty(t, snap2.ID)
}

// =====================================================================
// Image Tests
// =====================================================================

func TestCreateImage(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		instances, err := m.RunInstances(ctx, defaultConfig(), 1)
		requireNoError(t, err)

		img, err := m.CreateImage(ctx, driver.ImageConfig{
			InstanceID:  instances[0].ID,
			Name:        "my-image",
			Description: "test image",
			Tags:        map[string]string{"env": "test"},
		})
		requireNoError(t, err)

		assertNotEmpty(t, img.ID)
		assertEqual(t, "my-image", img.Name)
		assertEqual(t, "available", img.State)
		assertEqual(t, "test image", img.Description)
		assertNotEmpty(t, img.CreatedAt)
	})

	t.Run("nonexistent instance", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateImage(ctx, driver.ImageConfig{
			InstanceID: "i-nonexistent",
			Name:       "bad-image",
		})
		assertError(t, err, true)
	})
}

func TestDeregisterImage(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		instances, err := m.RunInstances(ctx, defaultConfig(), 1)
		requireNoError(t, err)

		img, err := m.CreateImage(ctx, driver.ImageConfig{
			InstanceID: instances[0].ID,
			Name:       "del-image",
		})
		requireNoError(t, err)

		err = m.DeregisterImage(ctx, img.ID)
		requireNoError(t, err)

		// Should be gone
		imgs, err := m.DescribeImages(ctx, []string{img.ID})
		requireNoError(t, err)
		assertEqual(t, 0, len(imgs))
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		err := m.DeregisterImage(ctx, "ami-nonexistent")
		assertError(t, err, true)
	})
}

func TestDescribeImages(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, err := m.RunInstances(ctx, defaultConfig(), 1)
	requireNoError(t, err)

	img1, err := m.CreateImage(ctx, driver.ImageConfig{
		InstanceID: instances[0].ID,
		Name:       "image-1",
	})
	requireNoError(t, err)

	img2, err := m.CreateImage(ctx, driver.ImageConfig{
		InstanceID: instances[0].ID,
		Name:       "image-2",
	})
	requireNoError(t, err)

	t.Run("describe all", func(t *testing.T) {
		imgs, err := m.DescribeImages(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(imgs))
	})

	t.Run("describe by ID", func(t *testing.T) {
		imgs, err := m.DescribeImages(ctx, []string{img1.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(imgs))
		assertEqual(t, img1.ID, imgs[0].ID)
	})

	// Keep img2 referenced
	assertNotEmpty(t, img2.ID)
}

// --- test helpers ---

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()
	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()
	if s == "" {
		t.Error("expected non-empty string")
	}
}

func assertTrue(t *testing.T, val bool, msg string) {
	t.Helper()
	if !val {
		t.Errorf("expected true: %s", msg)
	}
}

// =====================================================================
// Auto-Scaling Group Tests
// =====================================================================

func TestCreateAutoScalingGroup(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.AutoScalingGroupConfig
		expectErr bool
	}{
		{
			name: "success",
			cfg: driver.AutoScalingGroupConfig{
				Name:              "my-asg",
				MinSize:           1,
				MaxSize:           5,
				DesiredCapacity:   2,
				InstanceConfig:    defaultConfig(),
				HealthCheckType:   "EC2",
				Tags:              map[string]string{"env": "test"},
				AvailabilityZones: []string{"us-east-1a", "us-east-1b"},
			},
		},
		{
			name: "empty name",
			cfg: driver.AutoScalingGroupConfig{
				MinSize:         1,
				MaxSize:         5,
				DesiredCapacity: 2,
				InstanceConfig:  defaultConfig(),
			},
			expectErr: true,
		},
		{
			name: "desired below min",
			cfg: driver.AutoScalingGroupConfig{
				Name:            "bad-desired",
				MinSize:         3,
				MaxSize:         5,
				DesiredCapacity: 1,
				InstanceConfig:  defaultConfig(),
			},
			expectErr: true,
		},
		{
			name: "desired above max",
			cfg: driver.AutoScalingGroupConfig{
				Name:            "bad-desired-high",
				MinSize:         1,
				MaxSize:         3,
				DesiredCapacity: 5,
				InstanceConfig:  defaultConfig(),
			},
			expectErr: true,
		},
		{
			name: "max less than min",
			cfg: driver.AutoScalingGroupConfig{
				Name:            "bad-bounds",
				MinSize:         5,
				MaxSize:         3,
				DesiredCapacity: 4,
				InstanceConfig:  defaultConfig(),
			},
			expectErr: true,
		},
		{
			name: "negative min",
			cfg: driver.AutoScalingGroupConfig{
				Name:            "neg-min",
				MinSize:         -1,
				MaxSize:         3,
				DesiredCapacity: 1,
				InstanceConfig:  defaultConfig(),
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			ctx := context.Background()
			asg, err := m.CreateAutoScalingGroup(ctx, tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, tc.cfg.Name, asg.Name)
			assertEqual(t, tc.cfg.MinSize, asg.MinSize)
			assertEqual(t, tc.cfg.MaxSize, asg.MaxSize)
			assertEqual(t, tc.cfg.DesiredCapacity, asg.DesiredCapacity)
			assertEqual(t, tc.cfg.DesiredCapacity, asg.CurrentSize)
			assertEqual(t, tc.cfg.DesiredCapacity, len(asg.InstanceIDs))
			assertEqual(t, "active", asg.Status)
			assertEqual(t, tc.cfg.HealthCheckType, asg.HealthCheckType)
			assertNotEmpty(t, asg.CreatedAt)
			assertEqual(t, "test", asg.Tags["env"])
			assertEqual(t, 2, len(asg.AvailabilityZones))
		})
	}
}

func TestCreateAutoScalingGroupDuplicate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := driver.AutoScalingGroupConfig{
		Name:            "dup-asg",
		MinSize:         0,
		MaxSize:         2,
		DesiredCapacity: 1,
		InstanceConfig:  defaultConfig(),
	}

	_, err := m.CreateAutoScalingGroup(ctx, cfg)
	requireNoError(t, err)

	_, err = m.CreateAutoScalingGroup(ctx, cfg)
	assertError(t, err, true)
}

func TestDeleteAutoScalingGroup(t *testing.T) {
	t.Run("force delete with instances", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		cfg := driver.AutoScalingGroupConfig{
			Name:            "del-force",
			MinSize:         1,
			MaxSize:         3,
			DesiredCapacity: 2,
			InstanceConfig:  defaultConfig(),
		}
		asg, err := m.CreateAutoScalingGroup(ctx, cfg)
		requireNoError(t, err)
		assertEqual(t, 2, len(asg.InstanceIDs))

		err = m.DeleteAutoScalingGroup(ctx, "del-force", true)
		requireNoError(t, err)

		// ASG should be gone
		_, err = m.GetAutoScalingGroup(ctx, "del-force")
		assertError(t, err, true)
	})

	t.Run("no force with instances fails", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		cfg := driver.AutoScalingGroupConfig{
			Name:            "del-noforce",
			MinSize:         1,
			MaxSize:         3,
			DesiredCapacity: 2,
			InstanceConfig:  defaultConfig(),
		}
		_, err := m.CreateAutoScalingGroup(ctx, cfg)
		requireNoError(t, err)

		err = m.DeleteAutoScalingGroup(ctx, "del-noforce", false)
		assertError(t, err, true)
	})

	t.Run("no force with zero instances succeeds", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		// Create with 1 instance, then scale down to 0 via update
		cfg := driver.AutoScalingGroupConfig{
			Name:            "del-empty",
			MinSize:         0,
			MaxSize:         3,
			DesiredCapacity: 1,
			InstanceConfig:  defaultConfig(),
		}
		_, err := m.CreateAutoScalingGroup(ctx, cfg)
		requireNoError(t, err)

		// Scale down to 0
		err = m.UpdateAutoScalingGroup(ctx, "del-empty", 0, 0, 3)
		requireNoError(t, err)

		err = m.DeleteAutoScalingGroup(ctx, "del-empty", false)
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		err := m.DeleteAutoScalingGroup(ctx, "nonexistent", true)
		assertError(t, err, true)
	})
}

func TestGetAutoScalingGroup(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		cfg := driver.AutoScalingGroupConfig{
			Name:            "get-asg",
			MinSize:         1,
			MaxSize:         5,
			DesiredCapacity: 2,
			InstanceConfig:  defaultConfig(),
		}
		_, err := m.CreateAutoScalingGroup(ctx, cfg)
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "get-asg")
		requireNoError(t, err)
		assertEqual(t, "get-asg", asg.Name)
		assertEqual(t, 2, asg.DesiredCapacity)
		assertEqual(t, 2, asg.CurrentSize)
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.GetAutoScalingGroup(ctx, "missing")
		assertError(t, err, true)
	})
}

func TestListAutoScalingGroups(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		asgs, err := m.ListAutoScalingGroups(ctx)
		requireNoError(t, err)
		assertEqual(t, 0, len(asgs))
	})

	t.Run("multiple", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		for _, name := range []string{"asg-a", "asg-b", "asg-c"} {
			_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
				Name:            name,
				MinSize:         1,
				MaxSize:         2,
				DesiredCapacity: 1,
				InstanceConfig:  defaultConfig(),
			})
			requireNoError(t, err)
		}

		asgs, err := m.ListAutoScalingGroups(ctx)
		requireNoError(t, err)
		assertEqual(t, 3, len(asgs))
	})
}

func TestUpdateAutoScalingGroup(t *testing.T) {
	t.Run("scale up", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "update-up",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 2,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.UpdateAutoScalingGroup(ctx, "update-up", 5, 1, 10)
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "update-up")
		requireNoError(t, err)
		assertEqual(t, 5, asg.CurrentSize)
		assertEqual(t, 5, asg.DesiredCapacity)
		assertEqual(t, 5, len(asg.InstanceIDs))
	})

	t.Run("scale down", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "update-down",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 5,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.UpdateAutoScalingGroup(ctx, "update-down", 2, 1, 10)
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "update-down")
		requireNoError(t, err)
		assertEqual(t, 2, asg.CurrentSize)
		assertEqual(t, 2, asg.DesiredCapacity)
		assertEqual(t, 2, len(asg.InstanceIDs))
	})

	t.Run("no change", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "update-same",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 3,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.UpdateAutoScalingGroup(ctx, "update-same", 3, 1, 10)
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "update-same")
		requireNoError(t, err)
		assertEqual(t, 3, asg.CurrentSize)
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		err := m.UpdateAutoScalingGroup(ctx, "missing", 2, 1, 5)
		assertError(t, err, true)
	})

	t.Run("invalid bounds", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "update-bad",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 2,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		// desired outside new bounds
		err = m.UpdateAutoScalingGroup(ctx, "update-bad", 20, 1, 10)
		assertError(t, err, true)
	})
}

func TestSetDesiredCapacity(t *testing.T) {
	t.Run("scale up", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "setcap-up",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 2,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.SetDesiredCapacity(ctx, "setcap-up", 5)
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "setcap-up")
		requireNoError(t, err)
		assertEqual(t, 5, asg.CurrentSize)
		assertEqual(t, 5, asg.DesiredCapacity)
	})

	t.Run("scale down terminates newest", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		created, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "setcap-down",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 4,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)
		assertEqual(t, 4, len(created.InstanceIDs))

		// Keep the first two instance IDs - they should survive
		firstTwo := created.InstanceIDs[:2]

		err = m.SetDesiredCapacity(ctx, "setcap-down", 2)
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "setcap-down")
		requireNoError(t, err)
		assertEqual(t, 2, asg.CurrentSize)
		assertEqual(t, 2, len(asg.InstanceIDs))
		// The oldest instances should remain
		assertEqual(t, firstTwo[0], asg.InstanceIDs[0])
		assertEqual(t, firstTwo[1], asg.InstanceIDs[1])
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		err := m.SetDesiredCapacity(ctx, "missing", 3)
		assertError(t, err, true)
	})

	t.Run("desired below min", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "setcap-low",
			MinSize:         2,
			MaxSize:         10,
			DesiredCapacity: 3,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.SetDesiredCapacity(ctx, "setcap-low", 1)
		assertError(t, err, true)
	})

	t.Run("desired above max", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "setcap-high",
			MinSize:         1,
			MaxSize:         5,
			DesiredCapacity: 3,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.SetDesiredCapacity(ctx, "setcap-high", 10)
		assertError(t, err, true)
	})
}

// =====================================================================
// Scaling Policy Tests
// =====================================================================

func TestPutScalingPolicy(t *testing.T) {
	t.Run("create new policy", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "policy-asg",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 2,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name:              "scale-up",
			AutoScalingGroup:  "policy-asg",
			AdjustmentType:    "ChangeInCapacity",
			ScalingAdjustment: 2,
		})
		requireNoError(t, err)
	})

	t.Run("update existing policy", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "policy-upd",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 2,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name:              "my-policy",
			AutoScalingGroup:  "policy-upd",
			AdjustmentType:    "ChangeInCapacity",
			ScalingAdjustment: 1,
		})
		requireNoError(t, err)

		// Overwrite with new adjustment
		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name:              "my-policy",
			AutoScalingGroup:  "policy-upd",
			AdjustmentType:    "ExactCapacity",
			ScalingAdjustment: 5,
		})
		requireNoError(t, err)

		// Execute to verify it was updated
		err = m.ExecuteScalingPolicy(ctx, "policy-upd", "my-policy")
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "policy-upd")
		requireNoError(t, err)
		assertEqual(t, 5, asg.DesiredCapacity)
	})

	t.Run("ASG not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		err := m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name:             "orphan",
			AutoScalingGroup: "nonexistent",
		})
		assertError(t, err, true)
	})
}

func TestDeleteScalingPolicy(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "delpol-asg",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 2,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name:              "to-delete",
			AutoScalingGroup:  "delpol-asg",
			AdjustmentType:    "ChangeInCapacity",
			ScalingAdjustment: 1,
		})
		requireNoError(t, err)

		err = m.DeleteScalingPolicy(ctx, "delpol-asg", "to-delete")
		requireNoError(t, err)

		// Executing deleted policy should fail
		err = m.ExecuteScalingPolicy(ctx, "delpol-asg", "to-delete")
		assertError(t, err, true)
	})

	t.Run("policy not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "delpol-asg2",
			MinSize:         1,
			MaxSize:         5,
			DesiredCapacity: 1,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.DeleteScalingPolicy(ctx, "delpol-asg2", "nonexistent")
		assertError(t, err, true)
	})

	t.Run("ASG not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		err := m.DeleteScalingPolicy(ctx, "no-asg", "no-policy")
		assertError(t, err, true)
	})
}

func TestExecuteScalingPolicy(t *testing.T) {
	t.Run("ChangeInCapacity positive", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "exec-change",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 3,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name:              "add-two",
			AutoScalingGroup:  "exec-change",
			AdjustmentType:    "ChangeInCapacity",
			ScalingAdjustment: 2,
		})
		requireNoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "exec-change", "add-two")
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "exec-change")
		requireNoError(t, err)
		assertEqual(t, 5, asg.DesiredCapacity)
	})

	t.Run("ChangeInCapacity negative (scale down)", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "exec-neg",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 5,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name:              "remove-two",
			AutoScalingGroup:  "exec-neg",
			AdjustmentType:    "ChangeInCapacity",
			ScalingAdjustment: -2,
		})
		requireNoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "exec-neg", "remove-two")
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "exec-neg")
		requireNoError(t, err)
		assertEqual(t, 3, asg.DesiredCapacity)
	})

	t.Run("ExactCapacity", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "exec-exact",
			MinSize:         1,
			MaxSize:         10,
			DesiredCapacity: 3,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name:              "set-seven",
			AutoScalingGroup:  "exec-exact",
			AdjustmentType:    "ExactCapacity",
			ScalingAdjustment: 7,
		})
		requireNoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "exec-exact", "set-seven")
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "exec-exact")
		requireNoError(t, err)
		assertEqual(t, 7, asg.DesiredCapacity)
	})

	t.Run("PercentChangeInCapacity", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "exec-pct",
			MinSize:         1,
			MaxSize:         20,
			DesiredCapacity: 10,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name:              "grow-50pct",
			AutoScalingGroup:  "exec-pct",
			AdjustmentType:    "PercentChangeInCapacity",
			ScalingAdjustment: 50,
		})
		requireNoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "exec-pct", "grow-50pct")
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "exec-pct")
		requireNoError(t, err)
		// 10 + (10*50/100) = 15
		assertEqual(t, 15, asg.DesiredCapacity)
	})

	t.Run("clamped to max", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "exec-clamp-max",
			MinSize:         1,
			MaxSize:         5,
			DesiredCapacity: 4,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name:              "add-ten",
			AutoScalingGroup:  "exec-clamp-max",
			AdjustmentType:    "ChangeInCapacity",
			ScalingAdjustment: 10,
		})
		requireNoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "exec-clamp-max", "add-ten")
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "exec-clamp-max")
		requireNoError(t, err)
		assertEqual(t, 5, asg.DesiredCapacity)
	})

	t.Run("clamped to min", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "exec-clamp-min",
			MinSize:         2,
			MaxSize:         10,
			DesiredCapacity: 3,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name:              "remove-ten",
			AutoScalingGroup:  "exec-clamp-min",
			AdjustmentType:    "ChangeInCapacity",
			ScalingAdjustment: -10,
		})
		requireNoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "exec-clamp-min", "remove-ten")
		requireNoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "exec-clamp-min")
		requireNoError(t, err)
		assertEqual(t, 2, asg.DesiredCapacity)
	})

	t.Run("ASG not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		err := m.ExecuteScalingPolicy(ctx, "missing", "policy")
		assertError(t, err, true)
	})

	t.Run("policy not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name:            "exec-nopol",
			MinSize:         1,
			MaxSize:         5,
			DesiredCapacity: 1,
			InstanceConfig:  defaultConfig(),
		})
		requireNoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "exec-nopol", "missing")
		assertError(t, err, true)
	})
}

// =====================================================================
// Spot Instance Tests
// =====================================================================

func TestRequestSpotInstances(t *testing.T) {
	t.Run("single one-time", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		reqs, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
			InstanceConfig: defaultConfig(),
			MaxPrice:       0.05,
			Count:          1,
			Type:           "one-time",
		})
		requireNoError(t, err)
		assertEqual(t, 1, len(reqs))
		assertEqual(t, "active", reqs[0].Status)
		assertEqual(t, "one-time", reqs[0].Type)
		assertNotEmpty(t, reqs[0].ID)
		assertNotEmpty(t, reqs[0].InstanceID)
		assertNotEmpty(t, reqs[0].CreatedAt)
		assertTrue(t, reqs[0].MaxPrice == 0.05, "max price should be 0.05")
	})

	t.Run("multiple persistent", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		reqs, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
			InstanceConfig: defaultConfig(),
			MaxPrice:       0.10,
			Count:          3,
			Type:           "persistent",
		})
		requireNoError(t, err)
		assertEqual(t, 3, len(reqs))

		for _, req := range reqs {
			assertEqual(t, "active", req.Status)
			assertEqual(t, "persistent", req.Type)
			assertNotEmpty(t, req.InstanceID)
		}
	})

	t.Run("zero count", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
			InstanceConfig: defaultConfig(),
			MaxPrice:       0.05,
			Count:          0,
		})
		assertError(t, err, true)
	})

	t.Run("negative count", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
			InstanceConfig: defaultConfig(),
			MaxPrice:       0.05,
			Count:          -1,
		})
		assertError(t, err, true)
	})

	t.Run("zero max price", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
			InstanceConfig: defaultConfig(),
			MaxPrice:       0,
			Count:          1,
		})
		assertError(t, err, true)
	})

	t.Run("negative max price", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
			InstanceConfig: defaultConfig(),
			MaxPrice:       -1.0,
			Count:          1,
		})
		assertError(t, err, true)
	})
}

func TestCancelSpotRequests(t *testing.T) {
	t.Run("one-time terminates instance", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		reqs, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
			InstanceConfig: defaultConfig(),
			MaxPrice:       0.05,
			Count:          1,
			Type:           "one-time",
		})
		requireNoError(t, err)

		instanceID := reqs[0].InstanceID

		err = m.CancelSpotRequests(ctx, []string{reqs[0].ID})
		requireNoError(t, err)

		// Verify spot request is canceled
		desc, err := m.DescribeSpotRequests(ctx, []string{reqs[0].ID})
		requireNoError(t, err)
		assertEqual(t, "canceled", desc[0].Status)

		// Verify instance is terminated
		instances, err := m.DescribeInstances(ctx, []string{instanceID}, nil)
		requireNoError(t, err)
		assertEqual(t, 1, len(instances))
		assertEqual(t, "terminated", instances[0].State)
	})

	t.Run("persistent does not terminate instance", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		reqs, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
			InstanceConfig: defaultConfig(),
			MaxPrice:       0.05,
			Count:          1,
			Type:           "persistent",
		})
		requireNoError(t, err)

		instanceID := reqs[0].InstanceID

		err = m.CancelSpotRequests(ctx, []string{reqs[0].ID})
		requireNoError(t, err)

		// Verify spot request is canceled
		desc, err := m.DescribeSpotRequests(ctx, []string{reqs[0].ID})
		requireNoError(t, err)
		assertEqual(t, "canceled", desc[0].Status)

		// Verify instance is still running
		instances, err := m.DescribeInstances(ctx, []string{instanceID}, nil)
		requireNoError(t, err)
		assertEqual(t, 1, len(instances))
		assertEqual(t, "running", instances[0].State)
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		err := m.CancelSpotRequests(ctx, []string{"sir-nonexistent"})
		assertError(t, err, true)
	})
}

func TestDescribeSpotRequests(t *testing.T) {
	t.Run("all requests (empty filter)", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
			InstanceConfig: defaultConfig(),
			MaxPrice:       0.05,
			Count:          2,
			Type:           "one-time",
		})
		requireNoError(t, err)

		_, err = m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
			InstanceConfig: defaultConfig(),
			MaxPrice:       0.10,
			Count:          1,
			Type:           "persistent",
		})
		requireNoError(t, err)

		all, err := m.DescribeSpotRequests(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 3, len(all))
	})

	t.Run("empty IDs returns all", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		all, err := m.DescribeSpotRequests(ctx, []string{})
		requireNoError(t, err)
		assertEqual(t, 0, len(all))
	})

	t.Run("filter by specific IDs", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		reqs, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
			InstanceConfig: defaultConfig(),
			MaxPrice:       0.05,
			Count:          3,
			Type:           "one-time",
		})
		requireNoError(t, err)

		filtered, err := m.DescribeSpotRequests(ctx, []string{reqs[0].ID, reqs[2].ID})
		requireNoError(t, err)
		assertEqual(t, 2, len(filtered))
	})

	t.Run("ID not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.DescribeSpotRequests(ctx, []string{"sir-nonexistent"})
		assertError(t, err, true)
	})
}

// =====================================================================
// Launch Template Tests
// =====================================================================

func TestCreateLaunchTemplate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		tmpl, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			Name:           "my-template",
			InstanceConfig: defaultConfig(),
		})
		requireNoError(t, err)
		assertEqual(t, "my-template", tmpl.Name)
		assertNotEmpty(t, tmpl.ID)
		assertNotEmpty(t, tmpl.CreatedAt)
		assertTrue(t, tmpl.Version > 0, "version should be positive")
		assertEqual(t, "ami-12345", tmpl.InstanceConfig.ImageID)
	})

	t.Run("empty name", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			InstanceConfig: defaultConfig(),
		})
		assertError(t, err, true)
	})

	t.Run("duplicate name", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			Name:           "dup-tmpl",
			InstanceConfig: defaultConfig(),
		})
		requireNoError(t, err)

		_, err = m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			Name:           "dup-tmpl",
			InstanceConfig: defaultConfig(),
		})
		assertError(t, err, true)
	})

	t.Run("version numbering increments", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		tmpl1, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			Name:           "tmpl-v1",
			InstanceConfig: defaultConfig(),
		})
		requireNoError(t, err)

		tmpl2, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			Name:           "tmpl-v2",
			InstanceConfig: defaultConfig(),
		})
		requireNoError(t, err)

		assertTrue(t, tmpl2.Version > tmpl1.Version, "second template version should be greater")
	})
}

func TestDeleteLaunchTemplate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			Name:           "del-tmpl",
			InstanceConfig: defaultConfig(),
		})
		requireNoError(t, err)

		err = m.DeleteLaunchTemplate(ctx, "del-tmpl")
		requireNoError(t, err)

		// Should be gone
		_, err = m.GetLaunchTemplate(ctx, "del-tmpl")
		assertError(t, err, true)
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		err := m.DeleteLaunchTemplate(ctx, "nonexistent")
		assertError(t, err, true)
	})
}

func TestGetLaunchTemplate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		created, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			Name:           "get-tmpl",
			InstanceConfig: defaultConfig(),
		})
		requireNoError(t, err)

		fetched, err := m.GetLaunchTemplate(ctx, "get-tmpl")
		requireNoError(t, err)
		assertEqual(t, created.ID, fetched.ID)
		assertEqual(t, created.Name, fetched.Name)
		assertEqual(t, created.Version, fetched.Version)
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.GetLaunchTemplate(ctx, "missing")
		assertError(t, err, true)
	})
}

func TestListLaunchTemplates(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		templates, err := m.ListLaunchTemplates(ctx)
		requireNoError(t, err)
		assertEqual(t, 0, len(templates))
	})

	t.Run("multiple", func(t *testing.T) {
		m := newTestMock()
		ctx := context.Background()

		for _, name := range []string{"tmpl-a", "tmpl-b", "tmpl-c"} {
			_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
				Name:           name,
				InstanceConfig: defaultConfig(),
			})
			requireNoError(t, err)
		}

		templates, err := m.ListLaunchTemplates(ctx)
		requireNoError(t, err)
		assertEqual(t, 3, len(templates))
	})
}
