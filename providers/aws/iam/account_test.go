package iam

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestAccountSummary(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "u1"})
	requireNoError(t, err)
	_, err = m.CreateUser(ctx, driver.UserConfig{Name: "u2"})
	requireNoError(t, err)
	_, err = m.CreateRole(ctx, driver.RoleConfig{Name: "r1"})
	requireNoError(t, err)

	summary, err := m.AccountSummary(ctx)
	requireNoError(t, err)

	assertEqual(t, 2, summary["Users"])
	assertEqual(t, 1, summary["Roles"])
	assertEqual(t, maxAccessKeysPerUser, summary["AccessKeysPerUserQuota"])
	assertEqual(t, 0, summary["AccountPasswordPresent"])

	requireNoError(t, m.UpdateAccountPasswordPolicy(ctx, driver.PasswordPolicy{MinimumPasswordLength: 8}))

	summary, err = m.AccountSummary(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, summary["AccountPasswordPresent"])
}

func TestAccountPasswordPolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.GetAccountPasswordPolicy(ctx)
	assertError(t, err, true)

	if cerrors.GetCode(err) != cerrors.NotFound {
		t.Fatalf("get before set: code = %v, want NotFound", cerrors.GetCode(err))
	}

	requireNoError(t, m.UpdateAccountPasswordPolicy(ctx, driver.PasswordPolicy{
		MinimumPasswordLength: 12,
		RequireSymbols:        true,
		MaxPasswordAge:        90,
	}))

	got, err := m.GetAccountPasswordPolicy(ctx)
	requireNoError(t, err)
	assertEqual(t, 12, got.MinimumPasswordLength)
	assertEqual(t, true, got.RequireSymbols)
	assertEqual(t, 90, got.MaxPasswordAge)

	requireNoError(t, m.DeleteAccountPasswordPolicy(ctx))

	_, err = m.GetAccountPasswordPolicy(ctx)
	assertError(t, err, true)

	// Deleting a missing policy errors.
	assertError(t, m.DeleteAccountPasswordPolicy(ctx), true)
}

func TestUpdateAccountPasswordPolicyDefaultsLength(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Omitting MinimumPasswordLength falls back to the AWS default of 6.
	requireNoError(t, m.UpdateAccountPasswordPolicy(ctx, driver.PasswordPolicy{RequireNumbers: true}))

	got, err := m.GetAccountPasswordPolicy(ctx)
	requireNoError(t, err)
	assertEqual(t, defaultMinimumPasswordLength, got.MinimumPasswordLength)
}

func TestMFADevices(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	dev, err := m.CreateVirtualMFADevice(ctx, "root-mfa", "")
	requireNoError(t, err)

	if dev.SerialNumber == "" || len(dev.Base32StringSeed) == 0 || len(dev.QRCodePNG) == 0 {
		t.Fatalf("incomplete virtual MFA device: %+v", dev)
	}

	// Duplicate name conflicts.
	_, err = m.CreateVirtualMFADevice(ctx, "root-mfa", "")
	assertError(t, err, true)

	// Empty name is rejected.
	_, err = m.CreateVirtualMFADevice(ctx, "", "")
	assertError(t, err, true)

	_, err = m.CreateUser(ctx, driver.UserConfig{Name: "u"})
	requireNoError(t, err)

	// A fresh virtual device is unassigned, so the user's list is empty.
	devices, err := m.ListMFADevices(ctx, "u")
	requireNoError(t, err)
	assertEqual(t, 0, len(devices))

	// Unknown user errors.
	_, err = m.ListMFADevices(ctx, "ghost")
	assertError(t, err, true)
}
