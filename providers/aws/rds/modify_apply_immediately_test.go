package rds

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// seedModifyInstance creates a standalone db.t3.micro instance the
// ApplyImmediately tests modify.
func seedModifyInstance(t *testing.T, m *Mock) {
	t.Helper()

	_, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{
		ID:               "db1",
		Engine:           "mysql",
		MasterUsername:   "admin",
		InstanceClass:    "db.t3.micro",
		AllocatedStorage: 20,
	})
	requireNoError(t, err)
}

// TestModifyInstanceApplyImmediatelyTrue: the target fields update now and no
// pending changes are recorded.
func TestModifyInstanceApplyImmediatelyTrue(t *testing.T) {
	m := newTestMock()
	seedModifyInstance(t, m)

	out, err := m.ModifyInstance(context.Background(), "db1", rdsdriver.ModifyInstanceInput{
		InstanceClass:    "db.t3.large",
		AllocatedStorage: 50,
		ApplyImmediately: true,
	})
	requireNoError(t, err)

	assertEqual(t, "db.t3.large", out.InstanceClass)
	assertEqual(t, 50, out.AllocatedStorage)

	if out.PendingModifiedValues != nil {
		t.Fatalf("expected no pending values with ApplyImmediately, got %+v", out.PendingModifiedValues)
	}

	// Describe reflects the applied change with nothing pending.
	got := describeOne(t, m, "db1")
	assertEqual(t, "db.t3.large", got.InstanceClass)

	if got.PendingModifiedValues != nil {
		t.Fatalf("describe: expected no pending values, got %+v", got.PendingModifiedValues)
	}
}

// TestModifyInstanceApplyImmediatelyFalse: the current values stay unchanged and
// the requested changes are recorded in PendingModifiedValues, reflected on
// describe.
func TestModifyInstanceApplyImmediatelyFalse(t *testing.T) {
	m := newTestMock()
	seedModifyInstance(t, m)

	out, err := m.ModifyInstance(context.Background(), "db1", rdsdriver.ModifyInstanceInput{
		InstanceClass:    "db.t3.large",
		AllocatedStorage: 50,
	})
	requireNoError(t, err)

	// Current values are untouched.
	assertEqual(t, "db.t3.micro", out.InstanceClass)
	assertEqual(t, 20, out.AllocatedStorage)

	if out.PendingModifiedValues == nil {
		t.Fatal("expected PendingModifiedValues to be recorded")
	}

	assertEqual(t, "db.t3.large", out.PendingModifiedValues.InstanceClass)
	assertEqual(t, 50, out.PendingModifiedValues.AllocatedStorage)

	// Describe returns the same populated pending block over the unchanged row.
	got := describeOne(t, m, "db1")
	assertEqual(t, "db.t3.micro", got.InstanceClass)

	if got.PendingModifiedValues == nil {
		t.Fatal("describe: expected PendingModifiedValues to be reflected")
	}

	assertEqual(t, "db.t3.large", got.PendingModifiedValues.InstanceClass)
	assertEqual(t, 50, got.PendingModifiedValues.AllocatedStorage)
}

// TestModifyInstancePendingPasswordMasked: a deferred password change is stored
// masked and the plaintext is never exposed anywhere.
func TestModifyInstancePendingPasswordMasked(t *testing.T) {
	m := newTestMock()
	seedModifyInstance(t, m)

	const secret = "SuperSecret123!"

	out, err := m.ModifyInstance(context.Background(), "db1", rdsdriver.ModifyInstanceInput{
		MasterUserPassword: secret,
	})
	requireNoError(t, err)

	if out.PendingModifiedValues == nil {
		t.Fatal("expected PendingModifiedValues for a deferred password change")
	}

	assertEqual(t, rdsdriver.MaskedPassword, out.PendingModifiedValues.MasterUserPassword)

	if out.PendingModifiedValues.MasterUserPassword == secret {
		t.Fatal("plaintext password leaked into PendingModifiedValues")
	}

	// A deferred change must not rotate the stored engine credential.
	if pw, ok := m.rootPasswords["db1"]; ok && pw == secret {
		t.Fatal("deferred password change unexpectedly rotated the engine credential")
	}
}

// TestModifyInstanceNoOpNotPending: requesting a value equal to the current one
// records nothing (real RDS omits no-op changes).
func TestModifyInstanceNoOpNotPending(t *testing.T) {
	m := newTestMock()
	seedModifyInstance(t, m)

	// Same class as current, plus a real storage change.
	out, err := m.ModifyInstance(context.Background(), "db1", rdsdriver.ModifyInstanceInput{
		InstanceClass:    "db.t3.micro",
		AllocatedStorage: 100,
	})
	requireNoError(t, err)

	if out.PendingModifiedValues == nil {
		t.Fatal("expected PendingModifiedValues for the storage change")
	}

	assertEqual(t, "", out.PendingModifiedValues.InstanceClass)
	assertEqual(t, 100, out.PendingModifiedValues.AllocatedStorage)
}

// TestModifyInstanceNoDeferrableChangeClearsPending: an all-no-op deferred modify
// leaves PendingModifiedValues nil.
func TestModifyInstanceNoDeferrableChangeClearsPending(t *testing.T) {
	m := newTestMock()
	seedModifyInstance(t, m)

	out, err := m.ModifyInstance(context.Background(), "db1", rdsdriver.ModifyInstanceInput{
		InstanceClass:    "db.t3.micro",
		AllocatedStorage: 20,
	})
	requireNoError(t, err)

	if out.PendingModifiedValues != nil {
		t.Fatalf("expected nil PendingModifiedValues for a no-op modify, got %+v", out.PendingModifiedValues)
	}
}

func describeOne(t *testing.T, m *Mock, id string) rdsdriver.Instance {
	t.Helper()

	insts, err := m.DescribeInstances(context.Background(), []string{id})
	requireNoError(t, err)

	if len(insts) != 1 {
		t.Fatalf("expected 1 instance for %q, got %d", id, len(insts))
	}

	return insts[0]
}
