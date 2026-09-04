package ec2

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// rootVolumeOf returns the id of the EBS volume the mock materialized for and
// attached to instanceID, or "" if none is found.
func rootVolumeOf(t *testing.T, m *Mock, instanceID string) string {
	t.Helper()

	vols, err := m.DescribeVolumes(context.Background(), nil)
	requireNoError(t, err)

	for i := range vols {
		if vols[i].AttachedTo == instanceID {
			return vols[i].ID
		}
	}

	return ""
}

// TestTerminateVsDetachVolumeNoDataLoss guards the terminate cascade's
// delete-vs-detach decision against a TOCTOU race with a concurrent
// DetachVolume. The invariant: if DetachVolume reports success, the volume must
// survive terminate — a successful detach can never be silently deleted. Run
// under -race; a split read-then-delete would fail this in a fraction of trials.
func TestTerminateVsDetachVolumeNoDataLoss(t *testing.T) {
	ctx := context.Background()

	const trials = 800

	for trial := 0; trial < trials; trial++ {
		m := newTestMock()

		run, err := m.RunInstances(ctx, defaultConfig(), 1)
		requireNoError(t, err)

		instID := run[0].ID

		volID := rootVolumeOf(t, m, instID)
		assertNotEmpty(t, volID)

		var (
			wg        sync.WaitGroup
			detachErr error
		)

		wg.Add(2)

		go func() {
			defer wg.Done()

			detachErr = m.DetachVolume(ctx, volID, instID, "")
		}()

		go func() {
			defer wg.Done()

			_ = m.TerminateInstances(ctx, []string{instID})
		}()

		wg.Wait()

		_, describeErr := m.DescribeVolumes(ctx, []string{volID})
		gone := describeErr != nil

		if detachErr == nil && gone {
			t.Fatalf("trial %d: DetachVolume succeeded but volume %s was deleted by terminate (data loss)",
				trial, volID)
		}
	}
}

// TestTerminateCascadeDeletesRootVolume pins the single-threaded cascade: the
// root volume (DeleteOnTermination=true) is deleted on terminate, while a
// separately created (unattached) volume is untouched.
func TestTerminateCascadeDeletesRootVolume(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	run, err := m.RunInstances(ctx, defaultConfig(), 1)
	requireNoError(t, err)

	instID := run[0].ID
	rootID := rootVolumeOf(t, m, instID)
	assertNotEmpty(t, rootID)

	// A DeleteOnTermination=false volume attached after launch must survive.
	keep, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	requireNoError(t, err)
	requireNoError(t, m.AttachVolume(ctx, keep.ID, instID, "/dev/sdh"))

	requireNoError(t, m.TerminateInstances(ctx, []string{instID}))

	if _, err := m.DescribeVolumes(ctx, []string{rootID}); err == nil {
		t.Fatalf("root volume %s should be deleted on terminate", rootID)
	}

	kept, err := m.DescribeVolumes(ctx, []string{keep.ID})
	requireNoError(t, err)
	assertEqual(t, "available", kept[0].State)
	assertEqual(t, "", kept[0].AttachedTo)
}

// TestRunInstancesRollbackDeletesVolumes proves a mid-batch launch failure
// leaves no orphaned volumes: every volume materialized for an
// already-provisioned instance is torn down along with the instance record.
func TestRunInstancesRollbackDeletesVolumes(t *testing.T) {
	ctx := context.Background()
	eng := &fakeComputeEngine{ip: "10.1.1.1", failProvisionOn: 3}
	m := newEngineMock(eng)

	instances, err := m.RunInstances(ctx, defaultConfig(), 5)
	assertTrue(t, err != nil, "a mid-batch provision failure must surface an error")
	assertTrue(t, instances == nil, "no instances should be returned on a rolled-back batch")

	// Instances 1 and 2 were provisioned (and materialized a root volume each)
	// before the 3rd failed; rollback must delete those volumes.
	vols, derr := m.DescribeVolumes(ctx, nil)
	requireNoError(t, derr)
	assertEqual(t, 0, len(vols))
}
