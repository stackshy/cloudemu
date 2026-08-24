package iam

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestRolePermissionsBoundary(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	const arn = "arn:aws:iam::aws:policy/PowerUserAccess"

	_, err := m.CreateRole(ctx, driver.RoleConfig{Name: "r"})
	requireNoError(t, err)

	// Unset boundary reads as empty.
	got, err := m.RolePermissionsBoundary(ctx, "r")
	requireNoError(t, err)
	assertEqual(t, "", got)

	requireNoError(t, m.PutRolePermissionsBoundary(ctx, "r", arn))

	got, err = m.RolePermissionsBoundary(ctx, "r")
	requireNoError(t, err)
	assertEqual(t, arn, got)

	requireNoError(t, m.DeleteRolePermissionsBoundary(ctx, "r"))

	got, err = m.RolePermissionsBoundary(ctx, "r")
	requireNoError(t, err)
	assertEqual(t, "", got)

	// Missing role errors on every op.
	assertError(t, m.PutRolePermissionsBoundary(ctx, "ghost", arn), true)
	assertError(t, m.DeleteRolePermissionsBoundary(ctx, "ghost"), true)

	_, err = m.RolePermissionsBoundary(ctx, "ghost")
	assertError(t, err, true)
}

func TestUserPermissionsBoundary(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	const arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "u"})
	requireNoError(t, err)

	requireNoError(t, m.PutUserPermissionsBoundary(ctx, "u", arn))

	got, err := m.UserPermissionsBoundary(ctx, "u")
	requireNoError(t, err)
	assertEqual(t, arn, got)

	requireNoError(t, m.DeleteUserPermissionsBoundary(ctx, "u"))

	got, err = m.UserPermissionsBoundary(ctx, "u")
	requireNoError(t, err)
	assertEqual(t, "", got)

	assertError(t, m.PutUserPermissionsBoundary(ctx, "ghost", arn), true)
}

func TestAccessKeyLimit(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "u"})
	requireNoError(t, err)

	for range maxAccessKeysPerUser {
		_, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "u"})
		requireNoError(t, err)
	}

	_, err = m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "u"})
	assertError(t, err, true)

	if cerrors.GetCode(err) != cerrors.ResourceExhausted {
		t.Fatalf("third key error code = %v, want ResourceExhausted", cerrors.GetCode(err))
	}
}
