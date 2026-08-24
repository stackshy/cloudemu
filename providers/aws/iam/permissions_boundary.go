package iam

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// Permissions boundaries are an AWS-only concept: a managed-policy ARN that
// caps the maximum permissions an IAM role or user can be granted. They are
// not part of the portable driver, so the wire layer reaches them through a
// type assertion on the AWS Mock.

// PutRolePermissionsBoundary sets (or replaces) the permissions boundary of a
// role (IAM PutRolePermissionsBoundary).
func (m *Mock) PutRolePermissionsBoundary(_ context.Context, roleName, boundaryARN string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	rd.permissionsBoundary = boundaryARN

	return nil
}

// DeleteRolePermissionsBoundary clears a role's permissions boundary (IAM
// DeleteRolePermissionsBoundary).
func (m *Mock) DeleteRolePermissionsBoundary(_ context.Context, roleName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	rd.permissionsBoundary = ""

	return nil
}

// RolePermissionsBoundary returns a role's permissions-boundary ARN, or the
// empty string when none is set. It exists for the wire layer to populate
// Role.PermissionsBoundary on GetRole/CreateRole.
func (m *Mock) RolePermissionsBoundary(_ context.Context, roleName string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return "", errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	return rd.permissionsBoundary, nil
}

// PutUserPermissionsBoundary sets (or replaces) the permissions boundary of a
// user (IAM PutUserPermissionsBoundary).
func (m *Mock) PutUserPermissionsBoundary(_ context.Context, userName, boundaryARN string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ud, ok := m.users.Get(userName)
	if !ok {
		return errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	ud.permissionsBoundary = boundaryARN

	return nil
}

// DeleteUserPermissionsBoundary clears a user's permissions boundary (IAM
// DeleteUserPermissionsBoundary).
func (m *Mock) DeleteUserPermissionsBoundary(_ context.Context, userName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ud, ok := m.users.Get(userName)
	if !ok {
		return errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	ud.permissionsBoundary = ""

	return nil
}

// UserPermissionsBoundary returns a user's permissions-boundary ARN, or the
// empty string when none is set.
func (m *Mock) UserPermissionsBoundary(_ context.Context, userName string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ud, ok := m.users.Get(userName)
	if !ok {
		return "", errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	return ud.permissionsBoundary, nil
}
