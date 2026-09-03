package iam

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestVirtualMFADeviceLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	requireNoError(t, err)

	dev, err := m.CreateVirtualMFADevice(ctx, "alice-mfa", "")
	requireNoError(t, err)
	assertEqual(t, "arn:aws:iam::123456789012:mfa/alice-mfa", dev.SerialNumber)

	// Duplicate name is rejected.
	_, err = m.CreateVirtualMFADevice(ctx, "alice-mfa", "")
	assertError(t, err, true)
	if !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create error = %v, want AlreadyExists", err)
	}

	// A freshly created device is unassigned: absent from ListMFADevices and
	// listed as Unassigned by ListVirtualMFADevices.
	devices, err := m.ListMFADevices(ctx, "alice")
	requireNoError(t, err)
	assertEqual(t, 0, len(devices))

	unassigned, err := m.ListVirtualMFADevices(ctx, "Unassigned")
	requireNoError(t, err)
	assertEqual(t, 1, len(unassigned))
	assertEqual(t, dev.SerialNumber, unassigned[0].SerialNumber)
	if unassigned[0].AssignedUser != nil {
		t.Fatalf("unassigned device reports an assigned user: %+v", unassigned[0].AssignedUser)
	}

	// Two identical codes are rejected without reaching the device lookup.
	err = m.EnableMFADevice(ctx, "alice", dev.SerialNumber, "111111", "111111")
	assertError(t, err, true)
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("equal-codes error = %v, want InvalidArgument", err)
	}

	// Enabling for an unknown user is NotFound.
	err = m.EnableMFADevice(ctx, "ghost", dev.SerialNumber, "111111", "222222")
	assertError(t, err, true)
	if !errors.IsNotFound(err) {
		t.Fatalf("unknown-user error = %v, want NotFound", err)
	}

	// Enabling an unknown serial is NotFound.
	err = m.EnableMFADevice(ctx, "alice", "arn:aws:iam::123456789012:mfa/ghost", "111111", "222222")
	assertError(t, err, true)
	if !errors.IsNotFound(err) {
		t.Fatalf("unknown-serial error = %v, want NotFound", err)
	}

	// Enable succeeds and shows up in ListMFADevices for the user.
	requireNoError(t, m.EnableMFADevice(ctx, "alice", dev.SerialNumber, "111111", "222222"))

	devices, err = m.ListMFADevices(ctx, "alice")
	requireNoError(t, err)
	assertEqual(t, 1, len(devices))
	assertEqual(t, dev.SerialNumber, devices[0].SerialNumber)
	if devices[0].EnableDate == "" {
		t.Fatal("EnableDate is empty after EnableMFADevice")
	}

	assigned, err := m.ListVirtualMFADevices(ctx, "Assigned")
	requireNoError(t, err)
	assertEqual(t, 1, len(assigned))
	if assigned[0].AssignedUser == nil || assigned[0].AssignedUser.Name != "alice" {
		t.Fatalf("assigned device's user = %+v, want alice", assigned[0].AssignedUser)
	}

	// A second user cannot enable the same, already-assigned device.
	_, err = m.CreateUser(ctx, driver.UserConfig{Name: "bob"})
	requireNoError(t, err)

	err = m.EnableMFADevice(ctx, "bob", dev.SerialNumber, "333333", "444444")
	assertError(t, err, true)
	if !errors.IsAlreadyExists(err) {
		t.Fatalf("cross-user enable error = %v, want AlreadyExists", err)
	}

	// DeleteUser is blocked while the device is still enabled.
	err = m.DeleteUser(ctx, "alice")
	assertError(t, err, true)
	if !errors.IsFailedPrecondition(err) {
		t.Fatalf("delete-user-with-mfa error = %v, want FailedPrecondition", err)
	}

	// Deleting an enabled device is a DeleteConflict (FailedPrecondition).
	err = m.DeleteVirtualMFADevice(ctx, dev.SerialNumber)
	assertError(t, err, true)
	if !errors.IsFailedPrecondition(err) {
		t.Fatalf("delete-enabled-device error = %v, want FailedPrecondition", err)
	}

	// Deactivating for the wrong user is NotFound.
	err = m.DeactivateMFADevice(ctx, "bob", dev.SerialNumber)
	assertError(t, err, true)
	if !errors.IsNotFound(err) {
		t.Fatalf("deactivate-wrong-user error = %v, want NotFound", err)
	}

	requireNoError(t, m.DeactivateMFADevice(ctx, "alice", dev.SerialNumber))

	devices, err = m.ListMFADevices(ctx, "alice")
	requireNoError(t, err)
	assertEqual(t, 0, len(devices))

	// Now that it's deactivated, DeleteUser and DeleteVirtualMFADevice both succeed.
	requireNoError(t, m.DeleteVirtualMFADevice(ctx, dev.SerialNumber))

	// Deleting an already-deleted device is NotFound.
	err = m.DeleteVirtualMFADevice(ctx, dev.SerialNumber)
	assertError(t, err, true)
	if !errors.IsNotFound(err) {
		t.Fatalf("delete-missing-device error = %v, want NotFound", err)
	}

	requireNoError(t, m.DeleteUser(ctx, "alice"))
}

func TestListVirtualMFADevicesInvalidAssignmentStatus(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.ListVirtualMFADevices(ctx, "Bogus")
	assertError(t, err, true)
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("invalid AssignmentStatus error = %v, want InvalidArgument", err)
	}

	// The empty filter and "Any" both return everything.
	_, err = m.CreateVirtualMFADevice(ctx, "any-mfa", "")
	requireNoError(t, err)

	all, err := m.ListVirtualMFADevices(ctx, "")
	requireNoError(t, err)
	assertEqual(t, 1, len(all))

	all, err = m.ListVirtualMFADevices(ctx, "Any")
	requireNoError(t, err)
	assertEqual(t, 1, len(all))
}
