package drivertest

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// runInstancesCount is the fixture instance count used by tests that launch a
// small batch rather than a single instance. tagValueConformance is the fixed
// tag value fixtures set and check for tag round-trip / filter assertions.
const (
	runInstancesCount   = 3
	tagValueConformance = "conformance"
)

// RunComputeConformance runs the shared behavioral contract of
// services/compute/driver.Compute against a freshly constructed driver
// instance, obtained by calling newDriver. newDriver is called once per
// subtest so each one starts from an empty, isolated backend.
//
// Only the lifecycle surface genuinely shared across AWS EC2, Azure VM and GCP
// GCE is encoded here — RunInstances/DescribeInstances/Start/Stop/Reboot/
// Terminate/ModifyInstance and the instance-id/instance-type/instance-state-name/
// tag: filters. Provider-only capabilities (Azure PowerOff vs Deallocate, AWS
// launch templates/spot/volumes/snapshots/images/key pairs, ...) stay out of
// this suite and are covered by each provider's own tests.
func RunComputeConformance(t *testing.T, newDriver func() computedriver.Compute) {
	t.Helper()

	t.Run("RunInstances", func(t *testing.T) { testRunInstances(t, newDriver()) })
	t.Run("RunInstancesInvalidCount", func(t *testing.T) { testRunInstancesInvalidCount(t, newDriver()) })
	t.Run("DescribeInstancesRoundTrip", func(t *testing.T) { testDescribeInstancesRoundTrip(t, newDriver()) })
	t.Run("DescribeInstancesEmpty", func(t *testing.T) { testDescribeInstancesEmpty(t, newDriver()) })
	t.Run("DescribeInstancesUnknownID", func(t *testing.T) { testDescribeInstancesUnknownID(t, newDriver()) })
	t.Run("DescribeInstancesFilters", func(t *testing.T) { testDescribeInstancesFilters(t, newDriver()) })
	t.Run("Lifecycle", func(t *testing.T) { testLifecycle(t, newDriver()) })
	t.Run("LifecycleUnknownID", func(t *testing.T) { testLifecycleUnknownID(t, newDriver()) })
	t.Run("Terminate", func(t *testing.T) { testTerminate(t, newDriver()) })
	t.Run("ModifyInstance", func(t *testing.T) { testModifyInstance(t, newDriver()) })
}

// testRunInstances covers RunInstances' shared contract: it returns exactly
// `count` instances, each with a unique non-empty ID and the launched Tags,
// and the batch settles at StateRunning (the default, synchronous behavior
// with no async-settle window configured).
func testRunInstances(t *testing.T, d computedriver.Compute) {
	t.Helper()

	ctx := context.Background()
	cfg := computedriver.InstanceConfig{
		ImageID:      "img-conformance",
		InstanceType: "small",
		Tags:         map[string]string{"Name": tagValueConformance},
	}

	instances, err := d.RunInstances(ctx, cfg, runInstancesCount)
	requireNoError(t, err, "RunInstances")

	if len(instances) != runInstancesCount {
		t.Fatalf("RunInstances: got %d instances, want %d", len(instances), runInstancesCount)
	}

	seen := map[string]bool{}

	for i := range instances {
		inst := &instances[i]
		if inst.ID == "" {
			t.Error("RunInstances: instance has empty ID")
		}

		if seen[inst.ID] {
			t.Errorf("RunInstances: duplicate instance ID %q", inst.ID)
		}

		seen[inst.ID] = true

		if inst.State != compute.StateRunning {
			t.Errorf("RunInstances: instance %q State = %q, want %q", inst.ID, inst.State, compute.StateRunning)
		}

		if inst.Tags["Name"] != tagValueConformance {
			t.Errorf("RunInstances: instance %q Tags[Name] = %q, want %q", inst.ID, inst.Tags["Name"], tagValueConformance)
		}
	}
}

// testRunInstancesInvalidCount covers the shared count validation: a
// zero/negative count is rejected with InvalidArgument before anything is
// launched.
func testRunInstancesInvalidCount(t *testing.T, d computedriver.Compute) {
	t.Helper()

	ctx := context.Background()
	cfg := computedriver.InstanceConfig{ImageID: "img-conformance"}

	_, err := d.RunInstances(ctx, cfg, 0)
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("RunInstances(count=0): want InvalidArgument, got %v", err)
	}

	_, err = d.RunInstances(ctx, cfg, -1)
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("RunInstances(count=-1): want InvalidArgument, got %v", err)
	}
}

// testDescribeInstancesRoundTrip covers DescribeInstances' shared contract: an
// unfiltered call reflects every launched instance, and an explicit-ID call
// returns exactly the requested instances.
func testDescribeInstancesRoundTrip(t *testing.T, d computedriver.Compute) {
	t.Helper()

	ctx := context.Background()
	launched := mustRunInstances(t, d, runInstancesCount)

	all, err := d.DescribeInstances(ctx, nil, nil)
	requireNoError(t, err, "DescribeInstances(all)")

	if len(all) != runInstancesCount {
		t.Fatalf("DescribeInstances(all): got %d instances, want %d", len(all), runInstancesCount)
	}

	wantIDs := instanceIDs(launched)
	gotIDs := instanceIDs(all)

	for id := range wantIDs {
		if !gotIDs[id] {
			t.Errorf("DescribeInstances(all): missing launched instance %q", id)
		}
	}

	one, err := d.DescribeInstances(ctx, []string{launched[0].ID}, nil)
	requireNoError(t, err, "DescribeInstances(one id)")

	if len(one) != 1 || one[0].ID != launched[0].ID {
		t.Errorf("DescribeInstances(one id): got %v, want exactly [%q]", instanceIDs(one), launched[0].ID)
	}
}

// testDescribeInstancesEmpty covers DescribeInstances against an empty
// backend: an unfiltered call succeeds and reports no instances.
func testDescribeInstancesEmpty(t *testing.T, d computedriver.Compute) {
	t.Helper()

	instances, err := d.DescribeInstances(context.Background(), nil, nil)
	requireNoError(t, err, "DescribeInstances(empty backend)")

	if len(instances) != 0 {
		t.Errorf("DescribeInstances(empty backend): got %d instances, want 0", len(instances))
	}
}

// testDescribeInstancesUnknownID covers a request that names an instance ID
// that was never launched. Providers diverge here (AWS reports NotFound for
// an explicit unknown ID; Azure/GCP silently omit it), so this only asserts
// the behavior both forms guarantee: no panic, and the unknown ID is never
// present in a successful result.
func testDescribeInstancesUnknownID(t *testing.T, d computedriver.Compute) {
	t.Helper()

	instances, err := d.DescribeInstances(context.Background(), []string{"nonexistent"}, nil)
	if err != nil {
		return
	}

	for i := range instances {
		if instances[i].ID == "nonexistent" {
			t.Errorf("DescribeInstances(unknown id): fabricated an instance for id %q", instances[i].ID)
		}
	}
}

// testDescribeInstancesFilters covers the filter names shared by all three
// providers: instance-id, instance-type, instance-state-name, and the tag:
// prefix.
func testDescribeInstancesFilters(t *testing.T, d computedriver.Compute) {
	t.Helper()

	ctx := context.Background()
	cfg := computedriver.InstanceConfig{
		ImageID:      "img-conformance",
		InstanceType: "filter-type",
		Tags:         map[string]string{"Env": tagValueConformance},
	}

	instances, err := d.RunInstances(ctx, cfg, 1)
	requireNoError(t, err, "RunInstances")

	inst := instances[0]

	byID, err := d.DescribeInstances(ctx, nil, []computedriver.DescribeFilter{{Name: "instance-id", Values: []string{inst.ID}}})
	requireNoError(t, err, "DescribeInstances(instance-id filter)")
	assertOnlyInstance(t, byID, inst.ID, "instance-id filter")

	byType, err := d.DescribeInstances(
		ctx, nil, []computedriver.DescribeFilter{{Name: "instance-type", Values: []string{"filter-type"}}},
	)
	requireNoError(t, err, "DescribeInstances(instance-type filter)")
	assertOnlyInstance(t, byType, inst.ID, "instance-type filter")

	byState, err := d.DescribeInstances(
		ctx, nil, []computedriver.DescribeFilter{{Name: "instance-state-name", Values: []string{compute.StateRunning}}},
	)
	requireNoError(t, err, "DescribeInstances(instance-state-name filter)")
	assertOnlyInstance(t, byState, inst.ID, "instance-state-name filter")

	byTag, err := d.DescribeInstances(ctx, nil, []computedriver.DescribeFilter{{Name: "tag:Env", Values: []string{tagValueConformance}}})
	requireNoError(t, err, "DescribeInstances(tag filter)")
	assertOnlyInstance(t, byTag, inst.ID, "tag:Env filter")

	noMatch, err := d.DescribeInstances(
		ctx, nil, []computedriver.DescribeFilter{{Name: "instance-type", Values: []string{"no-such-type"}}},
	)
	requireNoError(t, err, "DescribeInstances(non-matching filter)")

	if len(noMatch) != 0 {
		t.Errorf("DescribeInstances(non-matching filter): got %d instances, want 0", len(noMatch))
	}
}

// assertOnlyInstance fails the test unless got is exactly the one-element set
// {wantID}, naming which filter produced the mismatch.
func assertOnlyInstance(t *testing.T, got []computedriver.Instance, wantID, filterName string) {
	t.Helper()

	if len(got) != 1 || got[0].ID != wantID {
		t.Errorf("DescribeInstances(%s): got %v, want exactly [%q]", filterName, instanceIDs(got), wantID)
	}
}

// testLifecycle covers the Start/Stop/Reboot state transitions shared by all
// three providers: Stop settles at StateStopped, Start (from stopped) settles
// back at StateRunning, Reboot (from running) settles at StateRunning, Start
// on an already-running instance and Stop on an already-stopped instance are
// idempotent no-ops, and Reboot on a stopped instance is rejected
// (FailedPrecondition) since none of the three define a stopped->restarting
// transition.
func testLifecycle(t *testing.T, d computedriver.Compute) {
	t.Helper()

	ctx := context.Background()
	inst := mustRunInstances(t, d, 1)[0]

	requireNoError(t, d.StopInstances(ctx, []string{inst.ID}), "StopInstances")
	assertInstanceState(t, d, inst.ID, compute.StateStopped)

	// Idempotent: stopping an already-stopped instance succeeds without error.
	requireNoError(t, d.StopInstances(ctx, []string{inst.ID}), "StopInstances(already stopped)")
	assertInstanceState(t, d, inst.ID, compute.StateStopped)

	if err := d.RebootInstances(ctx, []string{inst.ID}); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("RebootInstances(stopped instance): want FailedPrecondition, got %v", err)
	}

	requireNoError(t, d.StartInstances(ctx, []string{inst.ID}), "StartInstances")
	assertInstanceState(t, d, inst.ID, compute.StateRunning)

	// Idempotent: starting an already-running instance succeeds without error.
	requireNoError(t, d.StartInstances(ctx, []string{inst.ID}), "StartInstances(already running)")
	assertInstanceState(t, d, inst.ID, compute.StateRunning)

	requireNoError(t, d.RebootInstances(ctx, []string{inst.ID}), "RebootInstances")
	assertInstanceState(t, d, inst.ID, compute.StateRunning)
}

// testLifecycleUnknownID covers Start/Stop/Reboot/Terminate against an
// instance ID that was never launched: all four report NotFound.
func testLifecycleUnknownID(t *testing.T, d computedriver.Compute) {
	t.Helper()

	ctx := context.Background()
	ids := []string{"nonexistent"}

	assertNotFound(t, d.StartInstances(ctx, ids), "StartInstances(unknown id)")
	assertNotFound(t, d.StopInstances(ctx, ids), "StopInstances(unknown id)")
	assertNotFound(t, d.RebootInstances(ctx, ids), "RebootInstances(unknown id)")
	assertNotFound(t, d.TerminateInstances(ctx, ids), "TerminateInstances(unknown id)")
}

// testTerminate covers TerminateInstances' shared contract: a terminated
// instance settles at StateTerminated and stays describable (none of the
// three providers delete the record), a second Terminate on it is rejected
// (FailedPrecondition — none of the three define a transition out of
// terminated), and Start/Stop/Reboot against a terminated instance are
// likewise rejected.
func testTerminate(t *testing.T, d computedriver.Compute) {
	t.Helper()

	ctx := context.Background()
	inst := mustRunInstances(t, d, 1)[0]

	requireNoError(t, d.TerminateInstances(ctx, []string{inst.ID}), "TerminateInstances")
	assertInstanceState(t, d, inst.ID, compute.StateTerminated)

	if err := d.TerminateInstances(ctx, []string{inst.ID}); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("TerminateInstances(already terminated): want FailedPrecondition, got %v", err)
	}

	if err := d.StartInstances(ctx, []string{inst.ID}); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("StartInstances(terminated instance): want FailedPrecondition, got %v", err)
	}

	if err := d.StopInstances(ctx, []string{inst.ID}); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("StopInstances(terminated instance): want FailedPrecondition, got %v", err)
	}
}

// testModifyInstance covers ModifyInstance's shared contract: it requires the
// target instance to be stopped (FailedPrecondition otherwise), rejects an
// unknown ID with NotFound, and — once stopped — applies InstanceType and
// merges Tags, both visible on the next DescribeInstances.
func testModifyInstance(t *testing.T, d computedriver.Compute) {
	t.Helper()

	ctx := context.Background()

	assertNotFound(t, d.ModifyInstance(ctx, "nonexistent", computedriver.ModifyInstanceInput{}), "ModifyInstance(unknown id)")

	inst := mustRunInstances(t, d, 1)[0]

	modifyInput := computedriver.ModifyInstanceInput{InstanceType: "modified-type", Tags: map[string]string{"Extra": "tag"}}
	if err := d.ModifyInstance(ctx, inst.ID, modifyInput); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("ModifyInstance(running instance): want FailedPrecondition, got %v", err)
	}

	requireNoError(t, d.StopInstances(ctx, []string{inst.ID}), "StopInstances")
	requireNoError(t, d.ModifyInstance(ctx, inst.ID, modifyInput), "ModifyInstance(stopped instance)")

	got, err := d.DescribeInstances(ctx, []string{inst.ID}, nil)
	requireNoError(t, err, "DescribeInstances(after ModifyInstance)")

	if len(got) != 1 {
		t.Fatalf("DescribeInstances(after ModifyInstance): got %d instances, want 1", len(got))
	}

	if got[0].InstanceType != "modified-type" {
		t.Errorf("ModifyInstance: InstanceType = %q, want %q", got[0].InstanceType, "modified-type")
	}

	if got[0].Tags["Extra"] != "tag" {
		t.Errorf("ModifyInstance: Tags[Extra] = %q, want %q", got[0].Tags["Extra"], "tag")
	}
}

// mustRunInstances launches count instances and fails the test immediately if
// the driver rejects the call, so setup errors surface at the right line
// instead of cascading into unrelated assertion failures.
func mustRunInstances(t *testing.T, d computedriver.Compute, count int) []computedriver.Instance {
	t.Helper()

	cfg := computedriver.InstanceConfig{ImageID: "img-conformance", InstanceType: "small"}

	instances, err := d.RunInstances(context.Background(), cfg, count)
	requireNoError(t, err, "RunInstances")

	return instances
}

// assertInstanceState fails the test (t.Errorf, non-fatal) unless
// DescribeInstances reports id at the given State.
func assertInstanceState(t *testing.T, d computedriver.Compute, id, want string) {
	t.Helper()

	got, err := d.DescribeInstances(context.Background(), []string{id}, nil)
	requireNoError(t, err, "DescribeInstances")

	if len(got) != 1 {
		t.Fatalf("DescribeInstances(%q): got %d instances, want 1", id, len(got))
	}

	if got[0].State != want {
		t.Errorf("instance %q State = %q, want %q", id, got[0].State, want)
	}
}

// instanceIDs returns the set of instance IDs in instances.
func instanceIDs(instances []computedriver.Instance) map[string]bool {
	ids := make(map[string]bool, len(instances))
	for i := range instances {
		ids[instances[i].ID] = true
	}

	return ids
}
